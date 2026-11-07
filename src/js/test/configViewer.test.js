// The Configs page's read-only viewer: the config's details, its text as YAML
// or JSON, copying it, and links to the experiments that use it.
import { flushPromises } from '@vue/test-utils';
import YAML from 'js-yaml';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, notify, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/errorNotif', modules.errorNotif);

import ConfigViewer from '@/components/configs/ConfigViewer.vue';
import { buttons, mountComponent, textOf, toasts } from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';

const topology = {
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Topology',
  metadata: {
    name: 'site',
    created: '2025-01-01T00:00:00Z',
    updated: '2025-01-02T03:04:05Z',
    annotations: {
      'builder-doc': '{"cells":[]}',
      'builder-xml': '<SNIPPED>',
      owner: 'alice',
    },
  },
  spec: { nodes: [{ general: { hostname: 'h1' } }] },
};

const experiment = {
  apiVersion: 'phenix.sandia.gov/v2',
  kind: 'Experiment',
  metadata: { name: 'exp1', annotations: { topology: 'site' } },
  spec: {},
};

beforeEach(() => resetMocks({ store: {} }));
afterEach(() => vi.useRealTimers());

const mountViewer = (props = {}) =>
  mountComponent(ConfigViewer, {
    props: { title: 'Topology/site', obj: topology, ...props },
    attachTo: document.body,
  });

const text = (wrapper) => wrapper.get('textarea');

// each detail as "label: value"
const details = (wrapper) =>
  wrapper
    .findAll('[data-testid="viewer-meta"] > div')
    .map((d) => `${textOf(d.find('dt'))}: ${textOf(d.find('dd'))}`);

const copyButton = (wrapper) => wrapper.get('[data-testid="viewer-copy"]');

describe('the viewer', () => {
  it("shows the config's details, its annotations, and its text as YAML", async () => {
    vi.useFakeTimers({
      toFake: ['Date'],
      now: Date.parse('2025-01-02T06:04:05Z'),
    });
    const wrapper = await mountViewer();

    expect(details(wrapper)).toEqual([
      'Kind: Topology',
      'Name: site',
      'Created: 2025-01-01T00:00:00Z (1 day ago)',
      'Updated: 2025-01-02T03:04:05Z (3 hours ago)',
    ]);
    // the Builder's document shows its key only; its legacy diagram not at all
    expect(
      wrapper.findAll('.config-annotation').map((chip) => textOf(chip)),
    ).toEqual(['builder-doc', 'owner: alice']);
    // the annotations sit in the header, beside the title
    expect(wrapper.find('.modal-card-head .config-annotations').exists()).toBe(
      true,
    );
    expect(wrapper.text()).not.toContain('API version');
    expect(text(wrapper).attributes('aria-label')).toBe('Config, as YAML');
    expect(YAML.load(text(wrapper).element.value)).toEqual(topology);
    expect(text(wrapper).attributes()).toHaveProperty('readonly');
  });

  it('shows Loading… and no details until the config arrives', async () => {
    const wrapper = await mountViewer({ obj: null });

    expect(text(wrapper).element.value).toBe('Loading…');
    expect(wrapper.find('[data-testid="viewer-meta"]').exists()).toBe(false);
    expect(copyButton(wrapper).attributes()).toHaveProperty('disabled');
  });

  it("names an unknown time, and shows a config's lack of annotations", async () => {
    const wrapper = await mountViewer({
      obj: { kind: 'Role', metadata: { name: 'r' } },
    });

    expect(details(wrapper)).toEqual([
      'Kind: Role',
      'Name: r',
      'Created: unknown',
      'Updated: unknown',
    ]);
    expect(wrapper.find('.config-annotations').exists()).toBe(false);
  });

  it('switches between YAML and JSON, and between wrapped and long lines', async () => {
    const wrapper = await mountViewer();
    const format = (label) =>
      wrapper
        .findAll('[aria-label="Format"] button')
        .find((b) => textOf(b) === label);

    expect(format('YAML').attributes('aria-pressed')).toBe('true');
    await format('JSON').trigger('click');
    expect(format('JSON').attributes('aria-pressed')).toBe('true');
    expect(format('YAML').attributes('aria-pressed')).toBe('false');
    expect(text(wrapper).attributes('aria-label')).toBe('Config, as JSON');
    expect(text(wrapper).element.value).toBe(JSON.stringify(topology, null, 2));

    expect(text(wrapper).attributes('wrap')).toBe('soft');
    await wrapper.get('input[type="checkbox"]').setValue(false);
    expect(text(wrapper).attributes('wrap')).toBe('off');
    expect(text(wrapper).classes()).toContain('is-unwrapped');
  });
});

describe('the Copy button', () => {
  const clipboard = (writeText) =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
  const status = (wrapper) => textOf(wrapper.get('[role="status"]'));

  beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout'] }));

  it('copies the text shown, and says Copied for a moment', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    clipboard(writeText);
    const wrapper = await mountViewer();

    await copyButton(wrapper).trigger('click');
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith(YAML.dump(topology));
    expect(textOf(copyButton(wrapper))).toBe('Copied');
    expect(status(wrapper)).toBe('Copied the config as YAML.');

    await vi.advanceTimersByTimeAsync(2000);
    expect(textOf(copyButton(wrapper))).toBe('Copy');
    expect(status(wrapper)).toBe('');
  });

  it.each([
    [true, 'Copied', 'Copied the config as YAML.', []],
    [
      false,
      'Copy',
      '',
      [
        [
          'The browser did not copy the config.',
          'Select the text and copy it by hand.',
        ],
      ],
    ],
  ])(
    'copies the selection without the Clipboard API (copied: %s)',
    async (copied, label, said, errors) => {
      clipboard(() => Promise.reject(new Error('insecure')));
      document.execCommand = vi.fn(() => copied);
      const wrapper = await mountViewer();

      await copyButton(wrapper).trigger('click');
      await flushPromises();
      expect(document.execCommand).toHaveBeenCalledWith('copy');
      expect(textOf(copyButton(wrapper))).toBe(label);
      // a failure opens an error notification, which announces itself
      expect(status(wrapper)).toBe(said);
      expect(notify.show.mock.calls).toEqual(errors);
      expect(toasts()).toEqual([]);
    },
  );

  it('forgets Copied when another config is shown', async () => {
    clipboard(() => Promise.resolve());
    const wrapper = await mountViewer();
    await copyButton(wrapper).trigger('click');
    await flushPromises();

    await wrapper.setProps({ obj: experiment });
    expect(textOf(copyButton(wrapper))).toBe('Copy');
  });
});

describe('the links', () => {
  it.each([
    ['may get it', () => true, '/experiment/exp1'],
    ['may not get it', denyOnly(['experiments', 'get']), null],
  ])(
    "an experiment's config links its page for a role that %s",
    async (_, role, href) => {
      rbac.allowed = role;
      const wrapper = await mountViewer({
        title: 'Experiment/exp1',
        obj: experiment,
      });
      const link = wrapper.find('[data-testid="viewer-experiment"]');

      expect(link.exists() ? link.attributes('href') : null).toBe(href);
      if (href) {
        // the experiment icon beside the title, named for the experiment
        expect(link.element.closest('header')).not.toBeNull();
        expect(textOf(link)).toBe('');
        expect(link.attributes('aria-label')).toBe('Go to experiment exp1');
        expect(link.find('[data-icon="flask"]').exists()).toBe(true);
      }
      if (href) {
        await link.trigger('click');
        expect(wrapper.emitted('leave')).toHaveLength(1);
      }
      expect(wrapper.find('[data-testid="viewer-used-by"]').exists()).toBe(
        false,
      );
    },
  );

  it('lists the experiments that use a topology, linked for a role that may get them', async () => {
    rbac.allowed = denyOnly(['experiments', 'get', 'b']);
    const usedBy = ['a', 'b', ...Array.from({ length: 10 }, (_, i) => `x${i}`)];
    const wrapper = await mountViewer({ usedBy });
    const used = wrapper.get('[data-testid="viewer-used-by"]');
    const items = used.findAll('li');

    expect(textOf(used)).toMatch(/^Experiments that use this topology:/);
    expect(items).toHaveLength(11);
    expect(items[0].get('a').attributes('href')).toBe('/experiment/a');
    expect(items[1].find('a').exists()).toBe(false);
    expect(textOf(items[1])).toBe('b');
    expect(textOf(items[10])).toBe('and 2 more');

    await items[0].get('a').trigger('click');
    expect(wrapper.emitted('leave')).toHaveLength(1);
  });

  it('says when no experiment uses a scenario', async () => {
    const wrapper = await mountViewer({
      obj: { kind: 'Scenario', metadata: { name: 's' } },
      usedBy: [],
    });

    expect(textOf(wrapper.get('[data-testid="viewer-used-by"]'))).toBe(
      'Experiments that use this scenario: none',
    );
  });
});

describe('the action buttons', () => {
  // Download and Edit sit right of Copy; the footer holds only the Builder.
  it.each([
    [{}, ['YAML', 'JSON', 'Copy', 'Download'], null],
    [
      { builder: 'open', canEdit: true },
      ['YAML', 'JSON', 'Copy', 'Download', 'Edit'],
      ['Open in Builder'],
    ],
    [
      { builder: 'import', downloading: true },
      ['YAML', 'JSON', 'Copy', 'Download (spinning)'],
      ['Import into Builder'],
    ],
  ])('with %o shows %o and the footer %o', async (props, tools, footer) => {
    const wrapper = await mountViewer(props);
    expect(buttons(wrapper.get('.config-tools'))).toEqual(tools);
    const foot = wrapper.find('footer');
    expect(foot.exists() ? buttons(foot) : null).toEqual(footer);
  });

  it('reports each button the page acts on', async () => {
    const wrapper = await mountViewer({ builder: 'open', canEdit: true });
    for (const id of ['viewer-builder', 'viewer-edit', 'viewer-download']) {
      await wrapper.get(`[data-testid="${id}"]`).trigger('click');
    }

    expect(Object.keys(wrapper.emitted())).toEqual(
      expect.arrayContaining(['builder', 'edit', 'download']),
    );
    expect(wrapper.emitted()).not.toHaveProperty('close');
  });
});
