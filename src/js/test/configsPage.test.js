// The Configs page: viewer, uploads, downloads, deletes, and the editor's save
// and edit-loss guard. Configs are named Kind/name, so kinds may share names.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { h, nextTick } from 'vue';
import { RouterView } from 'vue-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  loaderSpy,
  modules,
  notify,
  rbac,
  resetMocks,
  saver,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('file-saver', modules.fileSaver);
vi.mock('@/store.js', modules.store);
// the list comes from the tests
vi.mock('@/utils/pageLoader.js', capturing);
// the editor's Ace loads in a browser
vi.mock('@/utils/loadAce.js', () => ({ loadAce: () => Promise.resolve() }));

import AceEditor from '@/components/configs/AceEditor.vue';
import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';
import ConfigsList from '@/components/configs/ConfigsList.vue';
import {
  clearConfigCache,
  configKey,
  findConfig,
} from '@/utils/configCache.js';
import { CONFIG_KINDS, configTotals } from '@/utils/pageTotals.js';
import Configs from '@/views/Configs.vue';
import { deferredGets, never } from './helpers/async.js';
import { allowAll, denyOnly } from './helpers/permissions.js';
import { componentDeclarations } from './helpers/css.js';
import {
  byLabel,
  buttons,
  confirmDialog,
  dialogs,
  mountComponent,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipPositions,
} from './helpers/mount.js';

// two list rows sharing a name
const topology = {
  kind: 'Topology',
  metadata: { name: 'web', updated: '2025-01-02T03:04:05Z' },
};
const scenario = {
  kind: 'Scenario',
  metadata: { name: 'web', updated: '2025-01-02T03:04:05Z' },
};

// the full config the server returns for a list row
const full = (row, spec = {}, annotations) => ({
  apiVersion: 'phenix.sandia.gov/v1',
  kind: row.kind,
  metadata: { ...row.metadata, ...(annotations && { annotations }) },
  spec,
});

beforeEach(() => {
  clearConfigCache();
  resetMocks({ store: {} });
});

// the open modal, or null; a closed b-modal stays in place, hidden
const openModal = (wrapper) =>
  wrapper
    .findAll('.modal.is-active')
    .find((modal) => modal.element.style.display !== 'none') ?? null;

// clicks the button with this text
async function click(where, text) {
  await where
    .findAll('button')
    .find((b) => textOf(b) === text)
    .trigger('click');
  await flushPromises();
}

// the title, message and confirm button of a Buefy dialog
const dialogText = (dialog) => ({
  title: textOf(dialog.querySelector('.modal-card-title')),
  message: textOf(dialog.querySelector('.media-content')),
  confirmText: textOf(dialog.querySelector('footer .button:last-child')),
});

describe('the config list', () => {
  // the list showing these rows
  const list = (configs = [topology, scenario]) =>
    mountPage(ConfigsList, {
      route: '/configs/',
      data: { configs, loaded: true },
    });

  // the cells of the row of this kind
  const row = (wrapper, kind) =>
    tableRows(wrapper).find((cells) => textOf(cells.Kind) === kind);

  const kinds = (wrapper) => tableRows(wrapper).map((r) => textOf(r.Kind));

  // opens the viewer by clicking the config's name
  const view = (wrapper, kind) =>
    row(wrapper, kind).Name.find('button.config-name').trigger('click');

  const download = (wrapper, kind) =>
    byLabel(row(wrapper, kind).Actions, `Download ${kind} web`).trigger(
      'click',
    );

  // selects every config with the header's checkbox
  const selectAll = (wrapper) =>
    wrapper.find('thead input[type="checkbox"]').setValue(true);

  // the open viewer: its title, its text, and whether its download spins
  function viewer(wrapper) {
    const modal = openModal(wrapper);
    if (!modal) return null;
    return {
      title: textOf(modal.find('.modal-card-title')),
      text: modal.find('textarea').element.value,
      downloading: byLabel(modal, 'Download config').classes('is-loading'),
    };
  }

  it('shows when each config changed as sent, then how long ago', async () => {
    vi.useFakeTimers({
      toFake: ['Date'],
      now: Date.parse('2025-01-02T06:04:05Z'),
    });
    try {
      const wrapper = await list([topology]);
      const updated = row(wrapper, 'Topology')['Last Updated'];
      expect(textOf(updated)).toBe('2025-01-02T03:04:05Z (3 hours ago)');
      expect(updated.find('time').attributes('datetime')).toBe(
        '2025-01-02T03:04:05Z',
      );
    } finally {
      vi.useRealTimers();
    }
  });

  // whether each row's download button spins, by kind
  const rowSpinners = (wrapper) =>
    Object.fromEntries(
      tableRows(wrapper).map((cells) => [
        textOf(cells.Kind),
        byLabel(cells.Actions, `Download ${textOf(cells.Kind)} web`).classes(
          'is-loading',
        ),
      ]),
    );

  // whether the table shows it is waiting on a change
  const waiting = (wrapper) => wrapper.find('.loading-overlay').exists();

  describe('viewer', () => {
    it('opens at once and shows the config opened last, however late another answers', async () => {
      const requests = deferredGets(axios.get);
      const answer = (url) => requests.find((r) => r.url == url);
      const wrapper = await list();

      await view(wrapper, 'Topology');
      await view(wrapper, 'Scenario');
      expect(viewer(wrapper)).toEqual({
        title: 'Scenario/web',
        text: 'Loading…',
        downloading: false,
      });
      expect(axios.get).toHaveBeenCalledWith('configs/Scenario/web', {
        headers: { Accept: 'application/json' },
      });

      answer('configs/Scenario/web').resolve({
        data: full(scenario, { apps: [{ name: 'soh' }] }),
      });
      await flushPromises();
      answer('configs/Topology/web').resolve({
        data: full(topology, { nodes: [] }),
      });
      await flushPromises();

      const shown = viewer(wrapper);
      expect(shown.title).toBe('Scenario/web');
      expect(shown.text).toContain('kind: Scenario');
      expect(shown.text).toContain('name: soh');
      expect(shown.text).not.toContain('Topology');
    });

    it("shows a Builder topology without its diagram's XML", async () => {
      axios.get.mockResolvedValue({
        data: full(topology, {}, { 'builder-xml': '<mxGraphModel/>' }),
      });
      const wrapper = await list();

      await view(wrapper, 'Topology');
      await flushPromises();
      const { text } = viewer(wrapper);
      expect(text).toContain('builder-xml: <SNIPPED>');
      expect(text).not.toContain('mxGraphModel');
    });

    it('closes and reports a config that fails to load', async () => {
      const err = new Error('forbidden');
      axios.get.mockRejectedValue(err);
      const wrapper = await list();

      await view(wrapper, 'Topology');
      await flushPromises();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(viewer(wrapper)).toBeNull();
    });
  });

  describe('viewer links', () => {
    // the list opened at /configs/?view=<key>, before or after it loaded
    const linked = (key, loaded = true) =>
      mountPage(ConfigsList, {
        route: `/configs/?view=${encodeURIComponent(key)}&kind=x`,
        data: loaded ? { configs: [topology, scenario], loaded } : {},
      });
    const query = (wrapper) => wrapper.router.currentRoute.value.query;

    it.each([
      ['its exact name', 'Scenario/web', 'Scenario/web'],
      ['its name in another case', 'Topology/WEB', 'Topology/web'],
    ])(
      'open the config a link names by %s, and leave the address',
      async (_, key, title) => {
        axios.get.mockImplementation(never);
        const wrapper = await linked(key);
        await flushPromises();

        expect(viewer(wrapper)?.title).toBe(title);
        expect(query(wrapper)).toEqual({ kind: 'x' });
        expect(notify.show).not.toHaveBeenCalled();
      },
    );

    it('wait for the list before opening the config', async () => {
      axios.get.mockImplementation(never);
      const wrapper = await linked('Topology/web', false);
      expect(viewer(wrapper)).toBeNull();

      loaderSpy.options.apply([topology, scenario]);
      await flushPromises();
      expect(viewer(wrapper)?.title).toBe('Topology/web');
    });

    it('keep a link to a config a cached list lacks until the fresh list', async () => {
      axios.get.mockImplementation(never);
      const fresh = Promise.withResolvers();
      loaderSpy.start = () => fresh.promise;
      const wrapper = await mountPage(ConfigsList, {
        route: '/configs/?view=Topology/new&kind=x',
      });
      const added = { kind: 'Topology', metadata: { name: 'new' } };

      loaderSpy.options.apply([topology]);
      await flushPromises();
      expect(viewer(wrapper)).toBeNull();
      expect(notify.show).not.toHaveBeenCalled();
      expect(query(wrapper).view).toBe('Topology/new');

      loaderSpy.options.apply([topology, added]);
      fresh.resolve(true);
      await flushPromises();
      expect(viewer(wrapper)?.title).toBe('Topology/new');
      expect(query(wrapper)).toEqual({ kind: 'x' });
      expect(notify.show).not.toHaveBeenCalled();
    });

    it('report a link a cached list lacks once the fresh load ends without it', async () => {
      const fresh = Promise.withResolvers();
      loaderSpy.start = () => fresh.promise;
      const wrapper = await mountPage(ConfigsList, {
        route: '/configs/?view=Topology/gone',
      });
      loaderSpy.options.apply([topology]);
      await flushPromises();
      expect(notify.show).not.toHaveBeenCalled();

      fresh.resolve(false);
      await flushPromises();
      expect(viewer(wrapper)).toBeNull();
      expect(notify.show).toHaveBeenCalledWith(
        'Config not found',
        'The config list has no config Topology/gone that your role can view.',
      );
      expect(query(wrapper)).toEqual({});
    });

    it.each([
      ['an unlisted config', 'Topology/gone', allowAll],
      ['no kind', 'web', allowAll],
      [
        'a config the role may not view',
        'Scenario/web',
        denyOnly(['configs', 'get', 'Scenario/web']),
      ],
    ])('report a link to %s, opening nothing', async (_, key, role) => {
      rbac.allowed = role;
      const wrapper = await linked(key);
      await flushPromises();

      expect(viewer(wrapper)).toBeNull();
      expect(axios.get).not.toHaveBeenCalled();
      expect(notify.show).toHaveBeenCalledWith(
        'Config not found',
        `The config list has no config ${key} that your role can view.`,
      );
      expect(query(wrapper)).toEqual({ kind: 'x' });
    });

    it('find a listed config by its own key, whatever its name', () => {
      fc.assert(
        fc.property(
          fc.uniqueArray(
            fc.record({
              kind: fc.constantFrom('Topology', 'Scenario', 'Experiment'),
              metadata: fc.record({ name: fc.string({ minLength: 1 }) }),
            }),
            { selector: configKey },
          ),
          (configs) => {
            for (const cfg of configs) {
              expect(findConfig(configs, configKey(cfg))).toBe(cfg);
            }
          },
        ),
      );
    });
  });

  describe('downloads', () => {
    it("ask for one config by its Kind/name, spinning its buttons until it's saved", async () => {
      const answer = Promise.withResolvers();
      axios.post.mockReturnValue(answer.promise);
      axios.get.mockResolvedValue({ data: full(topology) });
      const wrapper = await list();
      await view(wrapper, 'Topology');
      await flushPromises();

      await download(wrapper, 'Topology');
      expect(axios.post).toHaveBeenCalledWith(
        'configs/download',
        '["Topology/web"]',
        {
          headers: {
            'Content-Type': 'application/json',
            Accept: 'application/x-yaml',
          },
          responseType: 'blob',
        },
      );
      expect(rowSpinners(wrapper)).toEqual({ Topology: true, Scenario: false });
      expect(viewer(wrapper).downloading).toBe(true);

      answer.resolve({ data: 'kind: Topology\n' });
      await flushPromises();
      const [[blob, fileName]] = saver.saveAs.mock.calls;
      expect(fileName).toBe('Topology-web.yml');
      expect(await blob.text()).toBe('kind: Topology\n');
      expect(rowSpinners(wrapper)).toEqual({
        Topology: false,
        Scenario: false,
      });
      expect(viewer(wrapper).downloading).toBe(false);
    });

    it('save several configs as one zip', async () => {
      const zip = new Blob(['PK']);
      axios.post.mockResolvedValue({ data: zip });
      const wrapper = await list();

      await selectAll(wrapper);
      await byLabel(wrapper, 'Download selected configs').trigger('click');
      await flushPromises();
      expect(axios.post.mock.calls[0][1]).toBe(
        '["Topology/web","Scenario/web"]',
      );
      expect(saver.saveAs).toHaveBeenCalledWith(zip, 'configs.zip');
    });

    it('report a failure and stop spinning', async () => {
      const err = new Error('forbidden');
      axios.post.mockRejectedValue(err);
      const wrapper = await list();

      await download(wrapper, 'Topology');
      await flushPromises();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(saver.saveAs).not.toHaveBeenCalled();
      expect(rowSpinners(wrapper).Topology).toBe(false);
    });
  });

  describe('deletes', () => {
    it('remove one config by its Kind/name after confirming, leaving its namesake', async () => {
      axios.delete.mockResolvedValue({});
      const wrapper = await list();

      await byLabel(
        row(wrapper, 'Topology').Actions,
        'Delete Topology web',
      ).trigger('click');
      expect(dialogs().map(dialogText)).toEqual([
        {
          title: 'Delete the Config',
          message:
            'This will delete the Topology/web config. Are you sure you want to do this?',
          confirmText: 'Delete',
        },
      ]);
      expect(axios.delete).not.toHaveBeenCalled();

      await confirmDialog();
      expect(axios.delete).toHaveBeenCalledWith('configs/Topology/web');
      expect(kinds(wrapper)).toEqual(['Scenario']);
      expect(toasts()[0].text).toBe(
        'The Topology/web config has been deleted.',
      );
    });

    it('remove several configs after one confirmation', async () => {
      axios.delete.mockResolvedValue({});
      const wrapper = await list();

      await selectAll(wrapper);
      await byLabel(wrapper, 'Delete selected configs').trigger('click');
      expect(dialogText(dialogs()[0])).toMatchObject({
        title: 'Delete the Configs',
        message:
          'This will delete 2 configs. Are you sure you want to do this?',
      });
      await confirmDialog();

      expect(axios.delete.mock.calls).toEqual([
        ['configs/Topology/web'],
        ['configs/Scenario/web'],
      ]);
      expect(kinds(wrapper)).toEqual([]);
      expect(toasts()[0].text).toBe('The configs have been deleted.');
    });

    it('keep the row and report a delete the server refuses', async () => {
      const err = new Error('forbidden');
      axios.delete.mockRejectedValue(err);
      const wrapper = await list();

      await byLabel(
        row(wrapper, 'Topology').Actions,
        'Delete Topology web',
      ).trigger('click');
      await confirmDialog();

      expect(notify.error).toHaveBeenCalledWith(err);
      expect(kinds(wrapper)).toEqual(['Scenario', 'Topology']);
      expect(waiting(wrapper)).toBe(false);
    });
  });

  describe('uploads', () => {
    const file = (name) => new File(['kind: Topology\n'], name);

    // picks this file in the upload window
    async function upload(wrapper, picked) {
      await byLabel(wrapper, 'Upload a new config').trigger('click');
      const input = openModal(wrapper).find('input[type="file"]');
      Object.defineProperty(input.element, 'files', { value: [picked] });
      await input.trigger('change');
    }

    it('send a YAML or JSON file, then list the configs again', async () => {
      axios.post.mockResolvedValue({});
      const wrapper = await list();

      await upload(wrapper, file('web.yml'));
      const [url, form] = axios.post.mock.calls[0];
      expect(url).toBe('configs');
      expect(form.get('fileupload').name).toBe('web.yml');
      expect(openModal(wrapper)).toBeNull();
      await flushPromises();

      expect(toasts()[0].text).toBe('The file web.yml was uploaded');
      expect(loaderSpy.load).toHaveBeenCalled();
    });

    it('refuse any other file', async () => {
      const wrapper = await list();

      await upload(wrapper, file('notes.txt'));
      expect(axios.post).not.toHaveBeenCalled();
      expect(notify.show).toHaveBeenCalledWith(
        'Valid file types are .yaml, .yml, and .json',
      );
    });

    const base = fc.stringMatching(/^[a-z0-9 ._-]{1,8}$/);
    const valid = fc.mixedCase(fc.constantFrom('yaml', 'yml', 'json'));
    const endsValid = (name) => /\.(yaml|yml|json)$/i.test(name);

    // returns how many times this file name was sent to the server
    async function sends(name) {
      const wrapper = await list();
      const before = axios.post.mock.calls.length;
      try {
        await upload(wrapper, file(name));
        return axios.post.mock.calls.length - before;
      } finally {
        wrapper.unmount();
      }
    }

    it('send any file whose name ends in .yaml, .yml or .json, in any case', async () => {
      axios.post.mockResolvedValue({});
      await fc.assert(
        fc.asyncProperty(base, valid, async (name, ext) => {
          expect(await sends(`${name}.${ext}`)).toBe(1);
        }),
        { numRuns: 15 },
      );
    });

    it('refuse a file whose name has no yaml, yml or json in it', async () => {
      const name = fc
        .tuple(base, fc.stringMatching(/^[a-zA-Z]{0,5}$/))
        .map(([b, e]) => `${b}.${e}`)
        .filter((n) => !/yaml|yml|json/i.test(n));
      await fc.assert(
        fc.asyncProperty(name, async (n) => {
          expect(await sends(n)).toBe(0);
        }),
        { numRuns: 15 },
      );
    });

    it('send a file only when its name ends in .yaml, .yml or .json', async () => {
      axios.post.mockResolvedValue({});
      const ext = fc.oneof(
        valid,
        fc.stringMatching(/^[a-zA-Z]{0,5}$/),
        fc
          .tuple(
            fc.constantFrom('yaml', 'yml'),
            fc.stringMatching(/^[a-z.]{1,4}$/),
          )
          .map(([e, s]) => e + s),
      );
      await fc.assert(
        fc.asyncProperty(base, ext, async (name, e) => {
          const full = `${name}.${e}`;
          expect(await sends(full)).toBe(endsValid(full) ? 1 : 0);
        }),
        { numRuns: 15, examples: [['web', 'yml.txt']] },
      );
    });

    // the explained lines and a hint indented under one, as the server sends
    const validation = [
      'nodes[0] "ADServer" (line 14): property "image" is missing',
      '  hint: "image:" is on line 12 under nodes[0].hardware',
    ].join('\n');

    it('show why the server refused a config in the error dialog', async () => {
      const wrapper = await list();
      axios.post.mockRejectedValueOnce({
        response: { status: 400, data: { metadata: { validation } } },
      });
      await upload(wrapper, file('bad.yaml'));
      await flushPromises();

      const modal = openModal(wrapper);
      expect(textOf(modal.find('.modal-card-title'))).toBe('Validation Error');
      const text = byLabel(modal, 'Validation errors');
      expect(text.attributes('readonly')).toBeDefined();
      expect(text.attributes('style')).toContain('font-family: monospace');
      expect(text.element.value).toBe(validation);
      expect(notify.error).not.toHaveBeenCalled();
      expect(notify.show).not.toHaveBeenCalled();

      await click(modal, 'Exit');
      expect(openModal(wrapper)).toBeNull();
    });

    it.each([
      [
        'a refusal without validation lines',
        { response: { status: 400, data: { message: 'already exists' } } },
      ],
      ['a network error', new Error('Network Error')],
    ])('report %s as an error', async (_, err) => {
      const wrapper = await list();
      axios.post.mockRejectedValueOnce(err);
      await upload(wrapper, file('web.json'));
      await flushPromises();

      expect(notify.error).toHaveBeenCalledWith(err);
      expect(openModal(wrapper)).toBeNull();
    });
  });

  // the column ends at the window's right edge
  it('opens the Actions tooltips to the left, inside the window', async () => {
    const positions = tooltipPositions(row(await list(), 'Topology').Actions);
    expect(positions).toEqual(['is-left', 'is-left', 'is-left']);
  });

  // at 1024 px the three buttons wrapped onto two rows in every row
  it('keeps the Actions buttons on one row', async () => {
    expect(row(await list(), 'Scenario').Actions.classes()).toContain(
      'config-actions',
    );
    expect(
      componentDeclarations(
        'src/components/configs/ConfigsList.vue',
        ':deep(td.config-actions)',
      ),
    ).toEqual({ 'white-space': 'nowrap' });
  });

  it('finds the configs whose name holds the search as plain text, in any case', async () => {
    // few letters, so searches often match, with a regular expression's own
    const text = (max) => fc.stringMatching(new RegExp(`^[aAb.*(]{0,${max}}$`));
    const configs = fc.uniqueArray(
      fc.record({
        kind: fc.constantFrom('Topology', 'Scenario'),
        metadata: fc.record({ name: text(3).filter(Boolean) }),
      }),
      { selector: (c) => `${c.kind}/${c.metadata.name}`, maxLength: 6 },
    );
    const shown = (wrapper) =>
      tableRows(wrapper)
        .map(
          (r) =>
            `${textOf(r.Kind)}/${textOf(r.Name.find('button.config-name'))}`,
        )
        .sort();
    await fc.assert(
      fc.asyncProperty(configs, text(2), async (rows, search) => {
        const wrapper = await list(rows);
        await wrapper
          .find('input[placeholder="Find a Config"]')
          .setValue(search);
        expect(shown(wrapper)).toEqual(
          rows
            .filter((c) =>
              c.metadata.name.toLowerCase().includes(search.toLowerCase()),
            )
            .map((c) => `${c.kind}/${c.metadata.name}`)
            .sort(),
        );
        wrapper.unmount();
      }),
      { numRuns: 12 },
    );
  });

  it("offers each row the actions the role has on that config's Kind/name", async () => {
    rbac.allowed = (resource, verb, name) =>
      resource === 'configs' &&
      (verb === 'get' || name === 'Scenario/web' || name === undefined);
    const wrapper = await list();

    const actions = (cells) =>
      cells.Actions.findAll('button').map(
        (b) => b.attributes('aria-label').split(' ')[0],
      );
    expect(actions(row(wrapper, 'Topology'))).toEqual(['Download']);
    expect(actions(row(wrapper, 'Scenario'))).toEqual([
      'Edit',
      'Download',
      'Delete',
    ]);
  });
});

describe('the config totals', () => {
  // the strip's stats, as "label: value"
  const totalsText = (wrapper) =>
    wrapper
      .findAll('[aria-label="Config totals"] .stat')
      .map((s) => `${textOf(s.find('dt'))}: ${textOf(s.find('dd'))}`);

  const cfg = (kind, name) => ({ kind, metadata: { name } });

  it('counts every config and each kind present, in the kind filter order, whatever the filters', async () => {
    const configs = [
      cfg('Role', 'admin'),
      cfg('Topology', 'a'),
      cfg('Experiment', 'x'),
      cfg('Topology', 'b'),
      cfg('Role', 'viewer'),
      cfg('User', 'alice'),
    ];
    const wrapper = await mountPage(ConfigsList, {
      route: '/configs/',
      data: { configs, loaded: true },
    });
    const want = [
      'Configs: 6',
      'Topology: 2',
      'Experiment: 1',
      'User: 1',
      'Role: 2',
    ];
    expect(totalsText(wrapper)).toEqual(want);

    await wrapper.find('input[placeholder="Find a Config"]').setValue('a');
    await wrapper
      .find('select[aria-label="Filter configs by kind"]')
      .setValue('Role');
    expect(tableRows(wrapper)).toHaveLength(1);
    expect(totalsText(wrapper)).toEqual(want);
  });

  it('shows only the total, unknown, before the list loads', async () => {
    const wrapper = await mountPage(ConfigsList, {
      route: '/configs/',
      data: { configs: [], loaded: false },
    });
    expect(totalsText(wrapper)).toEqual(['Configs: …']);
  });

  it('lists a kind it does not know after the known ones', () => {
    const got = configTotals([
      cfg('Zebra', 'z'),
      cfg('Alpha', 'a'),
      cfg('Image', 'i'),
    ]);
    expect(got.kinds.map((k) => k.kind)).toEqual(['Image', 'Alpha', 'Zebra']);
  });

  it('counts each kind once, in a stable order, adding up to the total', () => {
    const kind = fc.constantFrom(...CONFIG_KINDS, 'Custom', 'Other');
    fc.assert(
      fc.property(fc.array(kind, { maxLength: 40 }), (kinds) => {
        const configs = kinds.map((k, i) => cfg(k, `c${i}`));
        const got = configTotals(configs);
        expect(got.configs).toBe(configs.length);
        expect(got.kinds.reduce((sum, k) => sum + k.count, 0)).toBe(
          configs.length,
        );
        expect(new Set(got.kinds.map((k) => k.kind)).size).toBe(
          got.kinds.length,
        );
        for (const { kind: k, count } of got.kinds) {
          expect(count).toBe(kinds.filter((x) => x === k).length);
        }
        // the order does not depend on the order of the list
        expect(configTotals([...configs].reverse()).kinds).toEqual(got.kinds);
      }),
    );
  });
});

describe('the config editor', () => {
  // Ace needs a browser; a stand-in takes its place
  const stubs = { AceEditor: true };

  // the editor opened on a config, as the page mounts it
  const editor = (mode = 'edit', editorConfig = topology) =>
    mountPage(ConfigsEditor, { props: { mode, editorConfig }, stubs });

  // the text in the editor, and typing over it
  const text = (wrapper) => wrapper.findComponent(AceEditor).props('value');
  async function type(wrapper, str) {
    wrapper.findComponent(AceEditor).vm.$emit('update:value', str);
    await nextTick();
  }

  // an editor with the config open and its text changed
  async function edited() {
    axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
    const wrapper = await editor();
    await type(wrapper, text(wrapper).replace('nodes: []', 'nodes: [a]'));
    return wrapper;
  }

  const closed = (wrapper) => wrapper.emitted('is-done') ?? [];

  it('opens a config the viewer loaded without loading it again', async () => {
    axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
    const list = await mountPage(ConfigsList, {
      route: '/configs/',
      data: { configs: [topology], loaded: true },
    });
    await tableRows(list)[0].Name.find('button.config-name').trigger('click');
    await flushPromises();

    const wrapper = await editor();
    expect(axios.get).toHaveBeenCalledTimes(1);
    expect(text(wrapper)).toContain('kind: Topology');
    expect(wrapper.find('.loading-overlay').exists()).toBe(false);
  });

  it("fills a new config's spec with a sample of its kind's schema", async () => {
    axios.get.mockImplementation(async (url) => ({
      data:
        url === 'schemas/v1'
          ? {}
          : {
              type: 'object',
              properties: {
                rules: { type: 'array', items: { type: 'string' } },
              },
            },
    }));
    const wrapper = await editor('create');

    vi.useFakeTimers();
    try {
      await wrapper.find('select').setValue('Role');
      await vi.advanceTimersByTimeAsync(500);
    } finally {
      vi.useRealTimers();
    }
    await flushPromises();

    // the full schema of the kind's own version, which its schema refers to
    expect(axios.get.mock.calls.map(([url]) => url)).toEqual([
      'schemas/v1',
      'schemas/Role/v1',
    ]);
    // the sampler loads with the schemas
    await vi.waitFor(() =>
      expect(text(wrapper)).toContain('spec:\n  rules:\n    - string'),
    );
    expect(text(wrapper)).toContain('kind: Role');
    expect(wrapper.find('.loading-overlay').exists()).toBe(false);
  });

  describe('leaving', () => {
    // the router view, drawing nothing off the Configs page
    const Host = {
      render() {
        return this.$route.name === 'configs' ? h(RouterView) : null;
      },
    };

    // The Configs page in a router view, as the app shows it, so leaving
    // the page runs its route guard; `open` opens its editor.
    async function configsPage(open) {
      const app = await mountComponent(Host, { stubs });
      app.router.addRoute({
        path: '/configs/',
        name: 'configs',
        component: Configs,
      });
      await app.router.push('/configs/');
      await flushPromises();
      if (!open) return { app };
      await open(app.findComponent(ConfigsList));
      await vi.waitFor(() =>
        expect(app.findComponent(ConfigsEditor).exists()).toBe(true),
      );
      await flushPromises();
      return { app, editor: app.findComponent(ConfigsEditor) };
    }

    // leaves for the Experiments page; resolves to the route then shown
    async function leave(app) {
      const left = app.router.push('/experiments');
      await flushPromises();
      return left;
    }

    const route = (app) => app.router.currentRoute.value.name;

    // lists the topology, then clicks its Edit button
    async function editTopology(list) {
      loaderSpy.options.apply([topology]);
      await nextTick();
      await byLabel(list, 'Edit Topology web').trigger('click');
    }

    it('closes without asking when nothing was edited', async () => {
      axios.get.mockResolvedValue({ data: full(topology) });

      for (const open of [
        editTopology,
        (list) => byLabel(list, 'Create a new config').trigger('click'),
      ]) {
        const { app, editor } = await configsPage(open);
        await leave(app);
        expect(route(app)).toBe('experiments');
        expect(dialogs()).toEqual([]);
        expect(closed(editor)).toEqual([['', '']]);
        app.unmount();
      }
    });

    it('asks before dropping edits, and stays on Cancel', async () => {
      axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
      const { app, editor } = await configsPage(editTopology);
      await type(editor, text(editor).replace('nodes: []', 'nodes: [a]'));

      const canceled = leave(app);
      await flushPromises();
      expect(dialogText(dialogs()[0]).title).toBe('Edits in Progress');
      dialogs()[0].querySelector('footer .button:first-child').click();
      await canceled;
      expect(route(app)).toBe('configs');
      expect(closed(editor)).toEqual([]);

      await vi.waitFor(() => expect(dialogs()).toEqual([]));
      const left = leave(app);
      await flushPromises();
      await confirmDialog();
      await left;
      expect(route(app)).toBe('experiments');
      expect(closed(editor)).toEqual([['', '']]);
    });

    it('opens the editor without Vue warnings', async () => {
      const warn = vi.spyOn(console, 'warn');
      const { app } = await configsPage((list) =>
        byLabel(list, 'Create a new config').trigger('click'),
      );
      const warnings = [...warn.mock.calls];
      warn.mockRestore();
      app.unmount();
      expect(warnings).toEqual([]);
    });

    it('lets the page go without the editor open', async () => {
      const { app } = await configsPage();

      await leave(app);
      expect(route(app)).toBe('experiments');
    });

    it('asks before a browser reload only with edits', async () => {
      // whether the editor asked to keep the page
      const reload = () => {
        const event = new Event('beforeunload', { cancelable: true });
        window.dispatchEvent(event);
        return event.defaultPrevented;
      };
      axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
      const unchanged = await editor();
      expect(reload()).toBe(false);
      unchanged.unmount();

      await edited();
      expect(reload()).toBe(true);
    });
  });

  describe('saving', () => {
    // clicks Save, then confirms
    async function save(wrapper) {
      await click(wrapper, 'Save');
      await confirmDialog();
    }

    it('overwrites the config under its Kind/name after confirming, then closes', async () => {
      axios.put.mockResolvedValue({});
      const wrapper = await edited();

      await click(wrapper, 'Save');
      expect(dialogText(dialogs()[0]).title).toBe('Modify the Config');
      await confirmDialog();

      const [url, body] = axios.put.mock.calls[0];
      expect(url).toBe('configs/Topology/web');
      expect(JSON.parse(body).spec).toEqual({ nodes: ['a'] });
      expect(closed(wrapper)).toEqual([
        ['config Topology/web has been edited', 'Topology/web'],
      ]);
    });

    describe('a config at a known revision', () => {
      const revised = {
        kind: 'Topology',
        metadata: { name: 'web', updated: '2025-01-02T03:04:05Z', revision: 7 },
      };

      // an editor with the revised config open and edited, saved and
      // confirmed
      async function saved() {
        axios.get.mockResolvedValue({ data: full(revised, { nodes: [] }) });
        const wrapper = await editor('edit', revised);
        await type(wrapper, text(wrapper).replace('nodes: []', 'nodes: [a]'));
        await save(wrapper);
        return wrapper;
      }

      const ifMatch = (call) => call[2].headers['If-Match'];

      it('is saved only if unchanged since it was opened', async () => {
        axios.put.mockResolvedValue({});
        const wrapper = await saved();

        expect(ifMatch(axios.put.mock.calls[0])).toBe('"r7"');
        expect(closed(wrapper)).toEqual([
          ['config Topology/web has been edited', 'Topology/web'],
        ]);
      });

      it('asks what to do when it changed meanwhile, and can overwrite it', async () => {
        axios.put.mockRejectedValueOnce({
          response: { status: 412, data: { metadata: { revision: '9' } } },
        });
        const wrapper = await saved();

        expect(closed(wrapper)).toEqual([]);
        expect(notify.error).not.toHaveBeenCalled();
        expect(textOf(openModal(wrapper))).toBe(
          'Config Changed Topology/web changed since you opened it ' +
            '(revision 7 → 9). Reload it (your edits are lost) ' +
            'Overwrite anyway Cancel',
        );

        // overwriting sends the same config without the precondition
        axios.put.mockResolvedValueOnce({});
        await click(openModal(wrapper), 'Overwrite anyway');
        expect(axios.put).toHaveBeenCalledTimes(2);
        expect(ifMatch(axios.put.mock.calls[1])).toBeUndefined();
        expect(axios.put.mock.calls[1][1]).toBe(axios.put.mock.calls[0][1]);
        expect(closed(wrapper)).toHaveLength(1);
      });

      it('reloads the newer config, dropping the edits', async () => {
        axios.put.mockRejectedValueOnce({
          response: { status: 412, data: { metadata: { revision: '9' } } },
        });
        const wrapper = await saved();

        axios.get.mockResolvedValue({
          data: full(
            { ...revised, metadata: { ...revised.metadata, revision: 9 } },
            { nodes: ['b'] },
          ),
        });
        await click(openModal(wrapper), 'Reload it (your edits are lost)');

        expect(openModal(wrapper)).toBeNull();
        expect(text(wrapper)).toContain('- b');
        // the next save is made against the reloaded revision
        axios.put.mockResolvedValue({});
        await save(wrapper);
        expect(ifMatch(axios.put.mock.calls.at(-1))).toBe('"r9"');
      });
    });

    // a failed save or create, on an editor opened for it
    const saves = [
      [
        'save',
        async (err) => {
          axios.put.mockRejectedValue(err);
          const wrapper = await edited();
          await save(wrapper);
          return wrapper;
        },
      ],
      [
        'create',
        async (err) => {
          axios.post.mockRejectedValue(err);
          // the kind's template never arrives
          axios.get.mockImplementation(never);
          const wrapper = await editor('create', null);
          await type(wrapper, 'kind: Topology\nmetadata:\n  name: db\n');
          await click(wrapper, 'Create');
          return wrapper;
        },
      ],
    ];

    // the editor's error window: its title, text and buttons
    function errorWindow(wrapper) {
      const modal = openModal(wrapper);
      return (
        modal &&
        [
          textOf(modal.find('.modal-card-title')),
          modal.find('textarea').element.value,
          ...buttons(modal.find('footer')),
        ].join(' ')
      );
    }

    it.each(saves)(
      'shows why the server refused a %s, keeping the editor open',
      async (_, failed) => {
        const err = {
          response: {
            data: { metadata: { validation: 'spec.nodes: expected object' } },
          },
        };
        const wrapper = await failed(err);

        expect(errorWindow(wrapper)).toBe(
          'Validation Error spec.nodes: expected object Exit',
        );
        expect(notify.error).not.toHaveBeenCalled();
        expect(closed(wrapper)).toEqual([]);
      },
    );

    it.each(saves)(
      'reports any other failed %s as usual, keeping the editor open',
      async (_, failed) => {
        const err = new Error('Network Error');
        const wrapper = await failed(err);

        expect(notify.error).toHaveBeenCalledWith(err);
        expect(errorWindow(wrapper)).toBeNull();
        expect(closed(wrapper)).toEqual([]);
      },
    );
  });
});
