// The Configs page: viewer, uploads, downloads, deletes, and the editor's save
// and edit-loss guard. Configs are named Kind/name, so kinds may share names.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks, saver } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('file-saver', modules.fileSaver);
vi.mock('@/store.js', modules.store);
// the list comes from the tests
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
// the editor's Ace loads in a browser
vi.mock('@/utils/loadAce.js', () => ({ loadAce: () => Promise.resolve() }));

import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';
import ConfigsList from '@/components/configs/ConfigsList.vue';
import { clearConfigCache } from '@/utils/configCache.js';
import Configs from '@/views/Configs.vue';
import { deferredGets, flush } from './helpers/async.js';
import { dialogs, fakeBuefy, toasts } from './helpers/buefy.js';
import { makeContext } from './helpers/context.js';
import {
  openModal,
  renderSSR,
  stubBrowser,
  tableRows,
  textOf,
} from './helpers/render.js';

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
  stubBrowser();
  clearConfigCache();
  resetMocks({ store: {} });
});

afterEach(() => vi.unstubAllGlobals());

describe('the config list', () => {
  // the list showing these rows
  function list(configs = [topology, scenario]) {
    const ctx = makeContext(ConfigsList, {
      $emit: vi.fn(),
      $buefy: fakeBuefy(),
    });
    ctx.configs = configs;
    ctx.loaded = true;
    return ctx;
  }

  // the page as the list's state has it
  const render = (ctx) =>
    renderSSR(
      ConfigsList,
      {},
      {
        route: '/configs/',
        tables: true,
        data: {
          configs: ctx.configs,
          loaded: true,
          viewer: ctx.viewer,
          downloading: ctx.downloading,
        },
      },
    );

  // the open viewer: its title, its text, and whether its download spins
  function viewer(html) {
    const modal = openModal(html);
    if (!modal) return null;
    return {
      title: textOf(modal.match(/modal-card-title[^>]*>([\s\S]*?)<\/p>/)[1]),
      text: textOf(modal.match(/<textarea[^>]*>([\s\S]*?)<\/textarea>/)[1]),
      downloading:
        /class="[^"]*is-loading[^"]*" aria-label="Download config"/.test(modal),
    };
  }

  // whether each row's download button spins, by kind
  const rowSpinners = (html) =>
    Object.fromEntries(
      tableRows(html).map((row) => [
        textOf(row.kind),
        /<button[^>]*is-loading[^>]*aria-label="Download config/.test(
          row.Actions,
        ),
      ]),
    );

  describe('viewer', () => {
    it('opens at once and shows the config opened last, however late another answers', async () => {
      const requests = deferredGets(axios.get);
      const answer = (url) => requests.find((r) => r.url == url);
      const ctx = list();

      ctx.viewConfig(topology);
      ctx.viewConfig(scenario);
      expect(viewer(await render(ctx))).toEqual({
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
      await flush();
      answer('configs/Topology/web').resolve({
        data: full(topology, { nodes: [] }),
      });
      await flush();

      const shown = viewer(await render(ctx));
      expect(shown.title).toBe('Scenario/web');
      expect(shown.text).toContain('kind: Scenario');
      expect(shown.text).toContain('name: soh');
      expect(shown.text).not.toContain('Topology');
    });

    it("shows a Builder topology without its diagram's XML", async () => {
      axios.get.mockResolvedValue({
        data: full(topology, {}, { 'builder-xml': '<mxGraphModel/>' }),
      });
      const ctx = list();

      await ctx.viewConfig(topology);
      const { text } = viewer(await render(ctx));
      expect(text).toContain('builder-xml: <SNIPPED>');
      expect(text).not.toContain('mxGraphModel');
    });

    it('closes and reports a config that fails to load', async () => {
      const err = new Error('forbidden');
      axios.get.mockRejectedValue(err);
      const ctx = list();

      await ctx.viewConfig(topology);
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(viewer(await render(ctx))).toBeNull();
    });
  });

  describe('downloads', () => {
    it("ask for one config by its Kind/name, spinning its buttons until it's saved", async () => {
      const answer = Promise.withResolvers();
      axios.post.mockReturnValue(answer.promise);
      axios.get.mockResolvedValue({ data: full(topology) });
      const ctx = list();
      await ctx.viewConfig(topology);

      ctx.download([topology]);
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
      let html = await render(ctx);
      expect(rowSpinners(html)).toEqual({ Topology: true, Scenario: false });
      expect(viewer(html).downloading).toBe(true);

      answer.resolve({ data: 'kind: Topology\n' });
      await flush();
      const [[blob, fileName]] = saver.saveAs.mock.calls;
      expect(fileName).toBe('Topology-web.yml');
      expect(await blob.text()).toBe('kind: Topology\n');
      html = await render(ctx);
      expect(rowSpinners(html)).toEqual({ Topology: false, Scenario: false });
      expect(viewer(html).downloading).toBe(false);
    });

    it('save several configs as one zip', async () => {
      const zip = new Blob(['PK']);
      axios.post.mockResolvedValue({ data: zip });

      list().download([topology, scenario]);
      await flush();
      expect(axios.post.mock.calls[0][1]).toBe(
        '["Topology/web","Scenario/web"]',
      );
      expect(saver.saveAs).toHaveBeenCalledWith(zip, 'configs.zip');
    });

    it('report a failure and stop spinning', async () => {
      const err = new Error('forbidden');
      axios.post.mockRejectedValue(err);
      const ctx = list();

      ctx.download([topology]);
      await flush();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(saver.saveAs).not.toHaveBeenCalled();
      expect(rowSpinners(await render(ctx)).Topology).toBe(false);
    });
  });

  describe('deletes', () => {
    it('remove one config by its Kind/name after confirming, leaving its namesake', async () => {
      axios.delete.mockResolvedValue({});
      const ctx = list();

      ctx.deleteConfigs([topology]);
      expect(dialogs(ctx)[0]).toMatchObject({
        title: 'Delete the Config',
        message:
          'This will delete the Topology/web config. Are you sure you want to do this?',
        confirmText: 'Delete',
      });
      expect(axios.delete).not.toHaveBeenCalled();

      dialogs(ctx)[0].onConfirm();
      expect(axios.delete).toHaveBeenCalledWith('configs/Topology/web');
      await flush();

      expect(ctx.configs).toEqual([scenario]);
      expect(toasts(ctx)[0].message).toBe(
        'The Topology/web config has been deleted.',
      );
      expect(tableRows(await render(ctx)).map((r) => textOf(r.kind))).toEqual([
        'Scenario',
      ]);
    });

    it('remove several configs after one confirmation', async () => {
      axios.delete.mockResolvedValue({});
      const ctx = list();

      ctx.deleteConfigs([topology, scenario]);
      expect(dialogs(ctx)[0]).toMatchObject({
        title: 'Delete the Configs',
        message:
          'This will delete 2 configs. Are you sure you want to do this?',
      });
      dialogs(ctx)[0].onConfirm();
      await flush();

      expect(axios.delete.mock.calls).toEqual([
        ['configs/Topology/web'],
        ['configs/Scenario/web'],
      ]);
      expect(ctx.configs).toEqual([]);
      expect(toasts(ctx)[0].message).toBe('The configs have been deleted.');
    });

    it('keep the row and report a delete the server refuses', async () => {
      const err = new Error('forbidden');
      axios.delete.mockRejectedValue(err);
      const ctx = list();

      ctx.deleteConfigs([topology]);
      dialogs(ctx)[0].onConfirm();
      await flush();

      expect(notify.error).toHaveBeenCalledWith(err);
      expect(ctx.configs).toEqual([topology, scenario]);
      expect(ctx.isWaiting).toBe(false);
    });
  });

  describe('uploads', () => {
    const file = (name) => new File(['kind: Topology\n'], name);

    it('send a YAML or JSON file, then list the configs again', async () => {
      axios.post.mockResolvedValue({});
      const ctx = list();
      ctx.loader = { load: vi.fn() };
      ctx.isUploaderModalActive = true;

      ctx.uploadFile(file('web.yml'));
      const [url, form] = axios.post.mock.calls[0];
      expect(url).toBe('configs');
      expect(form.get('fileupload').name).toBe('web.yml');
      expect(ctx.isUploaderModalActive).toBe(false);
      await flush();

      expect(toasts(ctx)[0].message).toBe('The file web.yml was uploaded');
      expect(ctx.loader.load).toHaveBeenCalled();
    });

    it('refuse any other file', () => {
      const ctx = list();
      ctx.uploadFile(file('notes.txt'));
      expect(axios.post).not.toHaveBeenCalled();
      expect(toasts(ctx)[0]).toMatchObject({
        message: 'Valid file types are .yaml, .yml, and .json',
        type: 'is-danger',
      });
    });

    // the explained lines and a hint indented under one, as the server sends
    const validation = [
      'nodes[0] "ADServer" (line 14): property "image" is missing',
      '  hint: "image:" is on line 12 under nodes[0].hardware',
    ].join('\n');

    it('show why the server refused a config in the error dialog', async () => {
      const ctx = list();
      axios.post.mockRejectedValueOnce({
        response: { status: 400, data: { metadata: { validation } } },
      });
      ctx.uploadFile(file('bad.yaml'));
      await flush();

      expect(ctx.error).toEqual({
        modal: true,
        title: 'Validation Error',
        msg: validation,
      });
      expect(notify.error).not.toHaveBeenCalled();
      expect(notify.show).not.toHaveBeenCalled();

      const html = await renderSSR(
        ConfigsList,
        {},
        { route: '/configs/', tables: true, data: { error: ctx.error } },
      );
      const [, attrs, text] =
        html.match(
          /<textarea([^>]*aria-label="Validation errors"[^>]*)>([^<]*)<\/textarea>/,
        ) ?? [];
      expect(attrs).toContain(' readonly');
      expect(attrs).toContain('font-family:monospace');
      expect(text).toBe(validation.replaceAll('"', '&quot;'));

      ctx.resetErrorModal();
      expect(ctx.error).toEqual({ modal: false, title: null, msg: null });
    });

    it.each([
      [
        'a refusal without validation lines',
        { response: { status: 400, data: { message: 'already exists' } } },
      ],
      ['a network error', new Error('Network Error')],
    ])('report %s as an error', async (_, err) => {
      const ctx = list();
      axios.post.mockRejectedValueOnce(err);
      ctx.uploadFile(file('web.json'));
      await flush();

      expect(notify.error).toHaveBeenCalledWith(err);
      expect(ctx.error.modal).toBe(false);
    });
  });

  it("offers each row the actions the role has on that config's Kind/name", async () => {
    rbac.allowed = (resource, verb, name) =>
      resource === 'configs' &&
      (verb === 'get' || name === 'Scenario/web' || name === undefined);
    const rows = tableRows(await render(list()));

    const actions = (row) =>
      [...row.Actions.matchAll(/aria-label="(\w+) config/g)].map((m) => m[1]);
    expect(rows.map(actions)).toEqual([
      ['Download'],
      ['Edit', 'Download', 'Delete'],
    ]);
  });
});

describe('the config editor', () => {
  // the editor opened on a config, as the page mounts it
  async function editor(mode = 'edit', editorConfig = topology) {
    const ctx = makeContext(ConfigsEditor, {
      mode,
      editorConfig,
      $emit: vi.fn(),
      $buefy: fakeBuefy(),
    });
    ConfigsEditor.created.call(ctx);
    ConfigsEditor.mounted.call(ctx);
    await flush();
    return ctx;
  }

  // an editor with the config open and its text changed
  async function edited() {
    axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
    const ctx = await editor();
    ctx.config.str = ctx.config.str.replace('nodes: []', 'nodes: [a]');
    return ctx;
  }

  const closed = (ctx) =>
    ctx.$emit.mock.calls.filter(([event]) => event === 'is-done');

  it('opens a config the viewer loaded without loading it again', async () => {
    axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
    const list = makeContext(ConfigsList, { $emit: vi.fn() });
    await list.viewConfig(topology);

    const ctx = await editor();
    expect(axios.get).toHaveBeenCalledTimes(1);
    expect(ctx.config.str).toContain('kind: Topology');
    expect(ctx.editor.isLoading).toBe(false);
  });

  describe('leaving', () => {
    // leaving the Configs page with this editor open
    const leave = (ctx) =>
      Configs.beforeRouteLeave.call({
        editorActive: true,
        $refs: { editor: ctx },
      });

    it('closes without asking when nothing was edited', async () => {
      axios.get.mockResolvedValue({ data: full(topology) });

      for (const ctx of [await editor(), await editor('create', null)]) {
        expect(await leave(ctx)).toBe(true);
        expect(dialogs(ctx)).toEqual([]);
        expect(closed(ctx)).toEqual([['is-done', '']]);
      }
    });

    it('asks before dropping edits, and stays on Cancel', async () => {
      const ctx = await edited();

      const canceled = leave(ctx);
      expect(dialogs(ctx)[0].title).toBe('Edits in Progress');
      dialogs(ctx)[0].onCancel();
      expect(await canceled).toBe(false);
      expect(closed(ctx)).toEqual([]);

      const left = leave(ctx);
      dialogs(ctx)[1].onConfirm();
      expect(await left).toBe(true);
      expect(closed(ctx)).toEqual([['is-done', '']]);
    });

    it('lets the page go without the editor open', async () => {
      expect(await Configs.beforeRouteLeave.call({ editorActive: false })).toBe(
        true,
      );
    });

    it('asks before a browser reload only with edits', async () => {
      axios.get.mockResolvedValue({ data: full(topology, { nodes: [] }) });
      const unchanged = await editor();
      const event = { preventDefault: vi.fn() };
      unchanged.handlePageReload(event);
      expect(event.preventDefault).not.toHaveBeenCalled();

      (await edited()).handlePageReload(event);
      expect(event.preventDefault).toHaveBeenCalled();
    });
  });

  describe('saving', () => {
    it('overwrites the config under its Kind/name after confirming, then closes', async () => {
      axios.put.mockResolvedValue({});
      const ctx = await edited();

      ctx.saveConfig();
      expect(dialogs(ctx)[0].title).toBe('Modify the Config');
      dialogs(ctx)[0].onConfirm();
      await flush();

      const [url, body] = axios.put.mock.calls[0];
      expect(url).toBe('configs/Topology/web');
      expect(JSON.parse(body).spec).toEqual({ nodes: ['a'] });
      expect(closed(ctx)).toEqual([
        ['is-done', 'config Topology/web has been edited'],
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
        const ctx = await editor('edit', revised);
        ctx.config.str = ctx.config.str.replace('nodes: []', 'nodes: [a]');
        ctx.saveConfig();
        dialogs(ctx)[0].onConfirm();
        await flush();
        return ctx;
      }

      const ifMatch = (call) => call[2].headers['If-Match'];

      it('is saved only if unchanged since it was opened', async () => {
        axios.put.mockResolvedValue({});
        const ctx = await saved();

        expect(ifMatch(axios.put.mock.calls[0])).toBe('"r7"');
        expect(closed(ctx)).toEqual([
          ['is-done', 'config Topology/web has been edited'],
        ]);
      });

      it('asks what to do when it changed meanwhile, and can overwrite it', async () => {
        axios.put.mockRejectedValueOnce({
          response: { status: 412, data: { metadata: { revision: '9' } } },
        });
        const ctx = await saved();

        expect(ctx.conflict).toMatchObject({
          modal: true,
          name: 'Topology/web',
          from: 7,
          to: 9,
        });
        expect(closed(ctx)).toEqual([]);
        expect(notify.error).not.toHaveBeenCalled();

        const html = await renderSSR(
          ConfigsEditor,
          { mode: 'edit', editorConfig: revised },
          { data: { conflict: ctx.conflict } },
        );
        expect(textOf(openModal(html))).toBe(
          'Config Changed Topology/web changed since you opened it ' +
            '(revision 7 → 9). Reload it (your edits are lost) ' +
            'Overwrite anyway Cancel',
        );

        // overwriting sends the same config without the precondition
        axios.put.mockResolvedValueOnce({});
        await ctx.overwriteConflicted();
        expect(axios.put).toHaveBeenCalledTimes(2);
        expect(ifMatch(axios.put.mock.calls[1])).toBeUndefined();
        expect(axios.put.mock.calls[1][1]).toBe(axios.put.mock.calls[0][1]);
        expect(closed(ctx)).toHaveLength(1);
      });

      it('reloads the newer config, dropping the edits', async () => {
        axios.put.mockRejectedValueOnce({
          response: { status: 412, data: { metadata: { revision: '9' } } },
        });
        const ctx = await saved();

        axios.get.mockResolvedValue({
          data: full(
            { ...revised, metadata: { ...revised.metadata, revision: 9 } },
            { nodes: ['b'] },
          ),
        });
        ctx.reloadConflicted();
        await flush();

        expect(ctx.conflict.modal).toBe(false);
        expect(ctx.loadedRevision).toBe(9);
        expect(ctx.config.str).toContain('- b');
      });
    });

    // a failed save or create, on an editor opened for it
    const saves = [
      [
        'save',
        async (err) => {
          axios.put.mockRejectedValue(err);
          const ctx = await edited();
          ctx.saveConfig();
          dialogs(ctx)[0].onConfirm();
          return ctx;
        },
      ],
      [
        'create',
        async (err) => {
          axios.post.mockRejectedValue(err);
          const ctx = await editor('create', null);
          ctx.config.str = 'kind: Topology\nmetadata:\n  name: db\n';
          ctx.createConfig();
          return ctx;
        },
      ],
    ];

    // the editor's error window, as the editor's state has it
    async function errorWindow(ctx) {
      const html = await renderSSR(
        ConfigsEditor,
        { mode: ctx.mode, editorConfig: ctx.editorConfig },
        { data: { error: ctx.error } },
      );
      const modal = openModal(html);
      return modal && textOf(modal);
    }

    it.each(saves)(
      'shows why the server refused a %s, keeping the editor open',
      async (_, save) => {
        const err = {
          response: {
            data: { metadata: { validation: 'spec.nodes: expected object' } },
          },
        };
        const ctx = await save(err);
        await flush();

        expect(await errorWindow(ctx)).toBe(
          'Validation Error spec.nodes: expected object Exit',
        );
        expect(notify.error).not.toHaveBeenCalled();
        expect(closed(ctx)).toEqual([]);
      },
    );

    it.each(saves)(
      'reports any other failed %s as usual, keeping the editor open',
      async (_, save) => {
        const err = new Error('Network Error');
        const ctx = await save(err);
        await flush();

        expect(notify.error).toHaveBeenCalledWith(err);
        expect(await errorWindow(ctx)).toBeNull();
        expect(closed(ctx)).toEqual([]);
      },
    );
  });
});
