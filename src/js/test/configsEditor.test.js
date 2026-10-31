// @vitest-environment happy-dom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';
import YAML from 'js-yaml';
import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';
import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif';

const role = {
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Role',
  metadata: { name: 'viewer' },
  spec: { policies: [] },
};

vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.resolve({ data: structuredClone(role) })),
    put: vi.fn(() => Promise.resolve({ data: {} })),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));

enableAutoUnmount(afterEach);

// Mounts the editor with the real Buefy and without the Ace editor.
function mountEditor(props) {
  return mount(ConfigsEditor, {
    attachTo: document.body,
    props,
    global: { plugins: [Buefy], stubs: { AceEditor: true } },
  });
}

describe('ConfigsEditor heading', () => {
  test('editing a config focuses a heading that names it', async () => {
    const wrapper = mountEditor({
      mode: 'edit',
      editorConfig: structuredClone(role),
    });
    await flushPromises();

    const heading = wrapper.get('h1');
    expect(heading.text()).toBe('Edit Role/viewer');
    expect(heading.attributes('tabindex')).toBe('-1');
    expect(document.activeElement).toBe(heading.element);
  });

  test('a new config focuses the New config heading', async () => {
    const wrapper = mountEditor({ mode: 'create' });
    await flushPromises();

    const heading = wrapper.get('h1');
    expect(heading.text()).toBe('New config');
    expect(document.activeElement).toBe(heading.element);
  });

  test('the editor says how to leave it with the keyboard', async () => {
    const wrapper = mountEditor({ mode: 'create' });
    await flushPromises();

    expect(wrapper.get('p.help').text()).toContain(
      'Escape to leave it (twice in Vim mode)',
    );
  });
});

describe('ConfigsEditor save', () => {
  test('saving a renamed config reports the new name', async () => {
    const wrapper = mountEditor({
      mode: 'edit',
      editorConfig: structuredClone(role),
    });
    await flushPromises();
    vi.spyOn(wrapper.vm.$buefy.dialog, 'confirm').mockImplementation((opts) =>
      opts.onConfirm(),
    );

    wrapper.vm.config.str = wrapper.vm.config.str.replace(
      'name: viewer',
      'name: auditor',
    );
    wrapper.vm.saveConfig('yaml');
    await flushPromises();

    expect(axiosInstance.put).toHaveBeenCalledWith(
      'configs/Role/viewer',
      expect.any(String),
      expect.anything(),
    );
    expect(wrapper.emitted('is-done').at(-1)).toEqual([
      'config Role/viewer has been edited',
      'Role/auditor',
    ]);
  });
});

// specs holds the embedded schema files the server serves at
// GET /schemas/{version}, keyed by version.
const specs = Object.fromEntries(
  ['v1', 'v2'].map((version) => [
    version,
    YAML.load(
      readFileSync(
        resolve(
          import.meta.dirname,
          `../../go/types/version/schemas/${version}.yaml`,
        ),
        'utf8',
      ),
    ),
  ]),
);

// serveSchemas answers the editor's schema requests as the server does: the
// whole spec of a version, or one kind's schema from that version's file.
function serveSchemas(url) {
  const [, first, second] = url.split('/');
  const spec = specs[second ?? first];
  const data = second ? spec?.components.schemas[first] : spec;
  if (!data) {
    return Promise.reject(new Error(`no schema for ${url}`));
  }
  return Promise.resolve({ data });
}

// Runs the component's options against a plain object, without a mount.
function newConfigsEditor() {
  const vm = ConfigsEditor.data();
  for (const [name, method] of Object.entries(ConfigsEditor.methods)) {
    vm[name] = method.bind(vm);
  }
  return vm;
}

// updateConfigTemplate does not return its request promise, so wait for it to
// settle.
async function updateTemplate(vm, config) {
  vm.updateConfigTemplate(config);
  await new Promise((resolve) => setTimeout(resolve));
}

describe('ConfigsEditor config templates', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    axiosInstance.get.mockImplementation(serveSchemas);
  });

  // Put back the mock's first answer, the role, for the other tests.
  afterEach(() => axiosInstance.get.mockReset());

  test.each([
    ['Experiment', 'v1'],
    ['Topology', 'v1'],
    ['Image', 'v1'],
    ['User', 'v1'],
    ['Role', 'v1'],
    ['Scenario', 'v2'],
  ])(
    'samples a %s from the schemas of its own version, %s',
    async (kind, version) => {
      const vm = newConfigsEditor();
      const config = {
        apiVersion: `phenix.sandia.gov/${version}`,
        kind,
        metadata: { name: 'TODO' },
        spec: {},
      };

      await updateTemplate(vm, config);

      expect(useErrorNotification).not.toHaveBeenCalled();
      expect(axiosInstance.get.mock.calls.map(([url]) => url)).toEqual([
        `schemas/${version}`,
        `schemas/${kind}/${version}`,
      ]);
      expect(Object.keys(config.spec)).not.toHaveLength(0);
      expect(vm.config.str).toContain(`kind: ${kind}`);
      expect(vm.editor.isLoading).toBe(false);
      expect(vm.pastConfigKind).toBe(kind);
    },
  );

  test('resolves an experiment scenario from the v1 schemas', async () => {
    const vm = newConfigsEditor();
    const config = {
      apiVersion: 'phenix.sandia.gov/v1',
      kind: 'Experiment',
      metadata: { name: 'TODO' },
      spec: {},
    };

    await updateTemplate(vm, config);

    expect(config.spec.scenario.apps[0].name).toBe('example-app');
    expect(config.spec.topology.nodes).toHaveLength(1);
  });

  test('reports a version without schemas and stops loading', async () => {
    const vm = newConfigsEditor();
    const config = {
      apiVersion: 'phenix.sandia.gov/v9',
      kind: 'Topology',
      metadata: { name: 'TODO' },
      spec: {},
    };

    await updateTemplate(vm, config);

    expect(axiosInstance.get).toHaveBeenCalledTimes(1);
    expect(useErrorNotification).toHaveBeenCalledTimes(1);
    expect(vm.editor.isLoading).toBe(false);
  });
});
