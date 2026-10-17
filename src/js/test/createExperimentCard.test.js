// The Experiments page's create card: its fields, help and checks, the
// scenarios it loads for a topology, and the request it emits.
import { flushPromises } from '@vue/test-utils';
import { h, withDirectives } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);

import CreateExperimentCard from '@/components/CreateExperimentCard.vue';
import {
  annotationErrors,
  annotationRow,
  customNodeValue,
  experimentAnnotationsPayload,
  MANAGED_EXPERIMENT_ANNOTATIONS,
  nodeAnnotationsPayload,
} from '@/utils/experimentAnnotations.js';
import { never } from './helpers/async.js';
import { byLabel, mountComponent, mountPage, textOf } from './helpers/mount.js';

// Has axios.get answer each URL from `answers`: data, or a promise of it;
// a URL not there never answers.
function answer(answers) {
  axios.get.mockImplementation(async (url) =>
    url in answers ? { data: await answers[url] } : never(),
  );
}

// the card, once its topology list has loaded (or not, when it never does)
const card = (options) => mountPage(CreateExperimentCard, options);

const nameInput = (wrapper) =>
  wrapper.find('input[aria-labelledby="create-exp-name"]');

const select = (wrapper, id) =>
  wrapper.find(`select[aria-labelledby="create-exp-${id}"]`);

// the scenario field's placeholder, and its other options
const scenarioField = (wrapper) => {
  const scenario = select(wrapper, 'scenario');
  return {
    placeholder: textOf(scenario.find('option[hidden]')),
    options: scenario.findAll('option:not([hidden])').map(textOf),
  };
};

async function choose(wrapper, id, value) {
  await select(wrapper, id).setValue(value);
  await flushPromises();
}

// the message under the field holding this control, or null
const message = (control, field = '.field') => {
  const help = control.element.closest(field).querySelector('p.help');
  return help ? textOf(help) : null;
};

beforeEach(() => {
  resetMocks({ store: {} });
  axios.get.mockImplementation(never);
});

describe('CreateExperimentCard', () => {
  it('shows the scenario field before a topology is chosen', async () => {
    const wrapper = await card();

    expect(wrapper.find('label#create-exp-scenario').exists()).toBe(true);
    expect(select(wrapper, 'scenario').element.disabled).toBe(true);
    expect(scenarioField(wrapper).placeholder).toBe('Select a topology first');
  });

  it('puts a small help button right after each label', async () => {
    const wrapper = await card();
    const labels = wrapper.findAll('label.label');

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
      const tooltip = labels.find((l) => textOf(l) === label).element
        .nextElementSibling;
      expect(tooltip.classList, label).toContain('b-tooltip');
      const help = tooltip.querySelector('.tooltip-trigger').firstElementChild;
      const button = `button[type="button"].field-help-button[aria-label="About ${label}"]`;
      expect(help.matches(button), label).toBe(true);
      expect(help.firstElementChild.matches('.icon.is-small'), label).toBe(
        true,
      );
    }
  });

  it('links to the docs section on creating experiments', async () => {
    const link = (await card()).find('a.create-exp-docs');

    expect(link.attributes()).toMatchObject({
      href: 'https://phenix.sceptre.dev/latest/experiments/#create-a-new-experiment',
      target: '_blank',
      rel: 'noopener',
    });
  });

  it('names the options section Advanced Options', async () => {
    const wrapper = await card();

    expect(textOf(wrapper.find('.create-exp-advanced-toggle'))).toBe(
      'Advanced Options',
    );
    expect(wrapper.findAll('*').map(textOf)).not.toContain('Options');
  });

  it('hides the bridge name in auto bridge mode', async () => {
    const wrapper = await card({
      props: { options: { 'bridge-mode': 'auto' } },
    });

    expect(textOf(wrapper)).not.toContain('Default Bridge Name');
  });

  it('focuses the name input once mounted', async () => {
    const wrapper = await card({ attachTo: document.body });
    // on a Buefy field, the input inside it
    expect(document.activeElement).toBe(nameInput(wrapper).element);

    const { focus } = CreateExperimentCard.directives;
    const plain = await mountComponent(
      { render: () => withDirectives(h('input'), [[focus]]) },
      { attachTo: document.body },
    );
    expect(document.activeElement).toBe(plain.element);
  });

  it('says why the scenario field is not ready', async () => {
    const empty = Promise.withResolvers();
    answer({
      topologies: { topologies: ['t', 'u'] },
      'topologies/t/scenarios': empty.promise,
      'topologies/u/scenarios': { scenarios: { s: [] } },
    });
    const wrapper = await card();

    await choose(wrapper, 'topology', 't');
    expect(scenarioField(wrapper).placeholder).toBe('Loading scenarios…');
    empty.resolve({});
    await flushPromises();
    expect(scenarioField(wrapper).placeholder).toBe(
      'No scenarios for this topology',
    );

    await choose(wrapper, 'topology', 'u');
    expect(scenarioField(wrapper)).toEqual({
      placeholder: 'None',
      options: ['None', 's'],
    });
  });

  it("loads the chosen topology's scenarios and their apps", async () => {
    const scenarios = Promise.withResolvers();
    answer({
      topologies: { topologies: ['old', 'topo'] },
      'topologies/old/scenarios': { scenarios: { s0: [] } },
      'topologies/topo/scenarios': scenarios.promise,
    });
    const wrapper = await card();
    await choose(wrapper, 'topology', 'old');
    await choose(wrapper, 'scenario', 's0');

    await choose(wrapper, 'topology', 'topo');
    const field = select(wrapper, 'scenario');
    expect(field.element.parentElement.classList).toContain('is-loading');
    // the placeholder shows only while no scenario is chosen
    expect(scenarioField(wrapper)).toEqual({
      placeholder: 'Loading scenarios…',
      options: [],
    });
    scenarios.resolve({ scenarios: { s1: ['a', 'b'] } });
    await flushPromises();

    expect(axios.get).toHaveBeenCalledWith('topologies/topo/scenarios');
    expect(field.element.parentElement.classList).not.toContain('is-loading');
    await choose(wrapper, 'scenario', 's1');
    expect(
      wrapper
        .findAll('.create-exp-apps .tag')
        .map((tag) => [textOf(tag), tag.attributes('aria-pressed')]),
    ).toEqual([
      ['a', 'true'],
      ['b', 'true'],
    ]);
  });

  it('ignores scenarios for a topology no longer chosen', async () => {
    const stale = Promise.withResolvers();
    answer({
      topologies: { topologies: ['first', 'second'] },
      'topologies/first/scenarios': stale.promise,
    });
    const wrapper = await card();

    await choose(wrapper, 'topology', 'first');
    await choose(wrapper, 'topology', 'second');
    stale.resolve({ scenarios: { stale: ['a'] } });
    await flushPromises();

    expect(scenarioField(wrapper)).toEqual({
      placeholder: 'Loading scenarios…',
      options: [],
    });
  });

  it('checks names as the server does', async () => {
    const nameError = async (name, options) => {
      const wrapper = await card({
        props: { experimentNames: ['taken'], options },
      });
      await nameInput(wrapper).setValue(name);
      return message(nameInput(wrapper));
    };

    expect(await nameError('')).toBe(null);
    expect(await nameError('ok-name')).toBe(null);
    expect(await nameError('has space')).toMatch(/spaces/);
    expect(await nameError('taken')).toMatch(/already exists/);
    for (const reserved of ['create', 'all', 'MiniMega', '__phenix__']) {
      expect(await nameError(reserved)).toMatch(/reserved/);
    }
    expect(
      await nameError('sixteen-chars-xx', { 'bridge-mode': 'auto' }),
    ).toMatch(/15 characters/);
  });

  it('needs both ends of a VLAN range, in order', async () => {
    const wrapper = await card();
    const vlanError = async (min, max) => {
      if (min !== null) {
        await byLabel(wrapper, 'VLAN range minimum').setValue(min);
      }
      if (max !== null) {
        await byLabel(wrapper, 'VLAN range maximum').setValue(max);
      }
      return message(
        byLabel(wrapper, 'VLAN range minimum'),
        '.create-exp-vlans',
      );
    };

    expect(await vlanError(null, null)).toBe(null);
    expect(await vlanError('100', null)).toMatch(/both/);
    expect(await vlanError(null, '200')).toBe(null);
    expect(await vlanError('200', '100')).toMatch(/below/);
  });

  // a card with topology topo and its scenario s1, whose apps are a and b
  async function filled() {
    answer({
      topologies: { topologies: ['topo'] },
      'topologies/topo/scenarios': { scenarios: { s1: ['a', 'b'] } },
    });
    const wrapper = await card();
    await nameInput(wrapper).setValue('exp');
    await choose(wrapper, 'topology', 'topo');
    return wrapper;
  }

  // adds the annotation with this menu entry to the list at this index
  async function addAnnotation(wrapper, list, entry) {
    const item = wrapper
      .findAllComponents({ name: 'AnnotationList' })
      .at(list)
      .findAllComponents({ name: 'BDropdownItem' })
      .find((i) => textOf(i.find('b')) === entry);
    await item.trigger('click');
  }

  const create = (wrapper) =>
    wrapper
      .findAll('footer button')
      .find((b) => textOf(b) === 'Create Experiment')
      .trigger('click');

  it('sends the annotations with the request', async () => {
    const wrapper = await filled();
    await choose(wrapper, 'scenario', 's1');
    // b-tag emits clicks on its text only
    await wrapper.find('.create-exp-apps .tag span').trigger('click');
    await wrapper
      .find('input[aria-labelledby="create-exp-branch"]')
      .setValue(' main ');
    await addAnnotation(wrapper, 0, 'phenix/default-apps');
    await addAnnotation(wrapper, 0, 'phenix/startup-autotunnel');
    await byLabel(wrapper, 'phenix/startup-autotunnel value').setValue(
      '8080:80, 2222:22',
    );
    await addAnnotation(wrapper, 1, 'Custom annotation');
    await byLabel(wrapper, 'Annotation 1 key').setValue('team');
    await byLabel(wrapper, 'team value').setValue('red');

    await create(wrapper);
    expect(wrapper.emitted('create')).toEqual([
      [
        {
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
        },
      ],
    ]);
  });

  it('leaves annotations out of the request when there are none', async () => {
    const wrapper = await filled();

    await create(wrapper);
    const [[request]] = wrapper.emitted('create');
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
