import { readFileSync } from 'node:fs';
import { beforeEach, describe, expect, test, vi } from 'vitest';
import YAML from 'js-yaml';
import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif';
import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';

// The real modules import the Pinia store, the router or Buefy, which need a
// browser. Vitest hoists vi.mock above the imports.
vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));

// specs holds the embedded schema files the server serves at
// GET /schemas/{version}, keyed by version.
const specs = Object.fromEntries(
  ['v1', 'v2'].map((version) => [
    version,
    YAML.load(
      readFileSync(
        new URL(
          `../../go/types/version/schemas/${version}.yaml`,
          import.meta.url,
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

// The repo has no DOM test environment or @vue/test-utils, so run the
// component's options against a plain object.
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
