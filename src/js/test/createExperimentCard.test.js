// The Experiments page's create card: rendered to a string with the real Buefy
// components, and its options exercised with a plain object standing in for
// the component instance.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const axios = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
const notify = vi.hoisted(() => vi.fn());
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: notify }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import CreateExperimentCard from '@/components/CreateExperimentCard.vue';
import {
  annotationErrors,
  annotationRow,
  customNodeValue,
  experimentAnnotationsPayload,
  MANAGED_EXPERIMENT_ANNOTATIONS,
  NODE_ANNOTATIONS,
  nodeAnnotationsPayload,
} from '@/utils/experimentAnnotations.js';
import { renderSSR } from './helpers/render.js';

// the card while its topology list is still loading
function render(props) {
  axios.get.mockReturnValue(new Promise(() => {}));
  return renderSSR(CreateExperimentCard, props);
}

const { computed, methods } = CreateExperimentCard;

beforeEach(() => {
  axios.get.mockReset();
  notify.mockReset();
});

describe('CreateExperimentCard', () => {
  it('shows the scenario field before a topology is chosen', async () => {
    const html = await render();

    expect(html).toMatch(/<label id="create-exp-scenario"[^>]*>/);
    const select = html.match(
      /<select[^>]*aria-labelledby="create-exp-scenario"[^>]*>[\s\S]*?<\/select>/,
    )[0];
    expect(select).toContain('disabled');
    expect(select).toContain('Select a topology first');
  });

  it('puts a small help button right after each label', async () => {
    const html = await render();

    for (const label of [
      'Experiment Name',
      'Experiment Topology',
      'Experiment Scenario',
      'Deployment Mode',
      'Default Bridge Name',
      'VLAN Range',
      'Git Workflow Branch Name',
      'Annotations for Every VM',
      'Experiment Annotations',
    ]) {
      const after = html.slice(html.indexOf(`>${label}</label>`));
      expect(after, label).toMatch(
        // the tooltip's hidden text comes first, then its trigger
        new RegExp(
          `^>${label}</label><div[^>]*b-tooltip[^>]*>(?:(?!<label)[\\s\\S])*?` +
            `<div class="tooltip-trigger"[^>]*><!--\\[-->` +
            `<button type="button" class="field-help-button" ` +
            `aria-label="About ${label}"[^>]*><span class="icon is-small"`,
        ),
      );
    }
  });

  it('links to the docs section on creating experiments', async () => {
    const html = await render();

    const link = html.match(/<a[^>]*create-exp-docs[^>]*>/)[0];
    expect(link).toContain(
      'href="https://phenix.sceptre.dev/latest/experiments/#create-a-new-experiment"',
    );
    expect(link).toContain('target="_blank"');
    expect(link).toContain('rel="noopener"');
  });

  it('names the options section Advanced Options', async () => {
    const html = await render();

    expect(html).toContain('>Advanced Options<');
    expect(html).not.toMatch(/>\s*Options\s*</);
  });

  it('hides the bridge name in auto bridge mode', async () => {
    const html = await render({ options: { 'bridge-mode': 'auto' } });

    expect(html).not.toContain('Default Bridge Name');
  });

  it('focuses the name input once mounted', () => {
    const { mounted } = CreateExperimentCard.directives.focus;
    const input = { tagName: 'INPUT', focus: vi.fn() };
    mounted(input);
    expect(input.focus).toHaveBeenCalled();

    // on a Buefy field, the input inside it
    const inner = { focus: vi.fn() };
    mounted({ tagName: 'DIV', querySelector: () => inner });
    expect(inner.focus).toHaveBeenCalled();
  });

  it('says why the scenario field is not ready', () => {
    const placeholder = (ctx) =>
      computed.scenarioPlaceholder.call({ scenarioNames: [], ...ctx });

    expect(placeholder({ topology: null })).toBe('Select a topology first');
    expect(placeholder({ topology: 't', scenariosLoading: true })).toBe(
      'Loading scenarios…',
    );
    expect(placeholder({ topology: 't' })).toBe(
      'No scenarios for this topology',
    );
    expect(placeholder({ topology: 't', scenarioNames: ['s'] })).toBe('None');
  });

  it("loads the chosen topology's scenarios and their apps", async () => {
    axios.get.mockResolvedValue({ data: { scenarios: { s1: ['a', 'b'] } } });
    const ctx = { topology: 'topo', scenario: 'old', scenarios: {} };

    const loading = methods.loadScenarios.call(ctx, 'topo');
    expect(ctx.scenariosLoading).toBe(true);
    expect(ctx.scenario).toBe(null);
    await loading;

    expect(axios.get).toHaveBeenCalledWith('topologies/topo/scenarios');
    expect(ctx.scenarios).toEqual({
      s1: [
        { name: 'a', disabled: false },
        { name: 'b', disabled: false },
      ],
    });
    expect(ctx.scenariosLoading).toBe(false);
  });

  it('ignores scenarios for a topology no longer chosen', async () => {
    const answer = Promise.withResolvers();
    axios.get.mockReturnValue(answer.promise);
    const ctx = { topology: 'first', scenarios: {} };

    const loading = methods.loadScenarios.call(ctx, 'first');
    ctx.topology = 'second';
    answer.resolve({ data: { scenarios: { stale: ['a'] } } });
    await loading;

    expect(ctx.scenarios).toEqual({});
  });

  it('checks names as the server does', () => {
    const nameError = (name, ctx = {}) =>
      computed.nameError.call({
        name,
        experimentNames: ['taken'],
        bridgeMode: 'manual',
        ...ctx,
      });

    expect(nameError('')).toBe(null);
    expect(nameError('ok-name')).toBe(null);
    expect(nameError('has space')).toMatch(/spaces/);
    expect(nameError('taken')).toMatch(/already exists/);
    for (const reserved of ['create', 'all', 'MiniMega', '__phenix__']) {
      expect(nameError(reserved)).toMatch(/reserved/);
    }
    expect(nameError('sixteen-chars-xx', { bridgeMode: 'auto' })).toMatch(
      /15 characters/,
    );
  });

  it('needs both ends of a VLAN range, in order', () => {
    const vlanError = (vlanMin, vlanMax) =>
      computed.vlanError.call({ vlanMin, vlanMax });

    expect(vlanError(null, null)).toBe(null);
    expect(vlanError(100, 200)).toBe(null);
    expect(vlanError(100, null)).toMatch(/both/);
    expect(vlanError(200, 100)).toMatch(/below/);
  });

  it('sends the annotations with the request', () => {
    const $emit = vi.fn();
    const tunnels = {
      ...annotationRow(NODE_ANNOTATIONS[1]),
      value: '8080:80, 2222:22',
    };
    const ctx = {
      $emit,
      valid: true,
      name: 'exp',
      topology: 'topo',
      scenario: 's1',
      scenarioApps: [
        { name: 'a', disabled: true },
        { name: 'b', disabled: false },
      ],
      vlanMin: null,
      vlanMax: null,
      branch: ' main ',
      deployMode: '',
      bridge: '',
      nodeAnnotations: [annotationRow(NODE_ANNOTATIONS[0]), tunnels],
      annotations: [
        { ...annotationRow({ type: 'text' }), key: 'team', value: 'red' },
      ],
    };

    methods.create.call(ctx);

    expect($emit).toHaveBeenCalledWith('create', {
      name: 'exp',
      topology: 'topo',
      scenario: 's1',
      vlan_min: 0,
      vlan_max: 0,
      workflow_branch: 'main',
      deploy_mode: '',
      disabled_apps: ['a'],
      default_bridge: '',
      node_annotations: {
        'phenix/default-apps': false,
        'phenix/startup-autotunnel': ['8080:80', '2222:22'],
      },
      annotations: { team: 'red' },
    });
  });

  it('leaves annotations out of the request when there are none', () => {
    const $emit = vi.fn();
    methods.create.call({
      $emit,
      valid: true,
      name: 'exp',
      topology: 'topo',
      scenario: null,
      scenarioApps: [],
      branch: '',
      bridge: '',
      nodeAnnotations: [],
      annotations: [],
    });

    const request = $emit.mock.calls[0][1];
    expect(request).not.toHaveProperty('node_annotations');
    expect(request).not.toHaveProperty('annotations');
  });
});

describe('experiment annotations', () => {
  it('reads custom node values as the CLI does', () => {
    expect(customNodeValue('true')).toBe(true);
    expect(customNodeValue(' false ')).toBe(false);
    expect(customNodeValue('["a", 1]')).toEqual(['a', 1]);
    expect(customNodeValue('{"k": "v"}')).toEqual({ k: 'v' });
    expect(customNodeValue('[not json')).toBe('[not json');
    expect(customNodeValue('123456')).toBe('123456');
  });

  it('builds both request maps from rows', () => {
    const row = (key, type, value) => ({
      ...annotationRow({ key, type }),
      value,
    });

    expect(
      nodeAnnotationsPayload([
        row('phenix/startup-via-cc', 'boolean', true),
        row('phenix/startup-autotunnel', 'list', ' 8080 ,, 9090:80 '),
        row('vrouter/enable-ssh', 'string', 'IF0'),
        { ...row('', 'text', '[1]'), key: ' custom ' },
      ]),
    ).toEqual({
      'phenix/startup-via-cc': true,
      'phenix/startup-autotunnel': ['8080', '9090:80'],
      'vrouter/enable-ssh': 'IF0',
      custom: [1],
    });

    expect(
      experimentAnnotationsPayload([
        row('phenix.workflow/tags', 'string', 'a=1'),
      ]),
    ).toEqual({ 'phenix.workflow/tags': 'a=1' });
  });

  it('flags rows the server would refuse or that say nothing', () => {
    const row = (key, value = 'v', type = 'text') => ({
      ...annotationRow({ type }),
      key,
      value,
    });

    expect(
      annotationErrors(
        [
          row(''),
          row('a'),
          row('a'),
          row('topology'),
          row('phenix.workflow/branch'),
          row('empty', ' '),
          row('flag', false, 'boolean'),
        ],
        MANAGED_EXPERIMENT_ANNOTATIONS,
      ),
    ).toEqual([
      'Enter a key',
      null,
      'a is already in the list',
      'Set by phēnix from the topology',
      'Set by the Git Workflow Branch Name field',
      'Enter a value',
      null,
    ]);
  });
});
