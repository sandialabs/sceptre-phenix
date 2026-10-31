// The Experiments page's create card: its fields, help and checks, the
// scenarios it loads for a topology, and the request it emits.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
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
  annotationRowsFrom,
  CUSTOM_NODE_ANNOTATION,
  customNodeValue,
  experimentAnnotationsPayload,
  MANAGED_EXPERIMENT_ANNOTATIONS,
  NODE_ANNOTATIONS,
  nodeAnnotationsPayload,
} from '@/utils/experimentAnnotations.js';
import { never } from './helpers/async.js';
import {
  buttons,
  byLabel,
  mountComponent,
  mountPage,
  textOf,
} from './helpers/mount.js';

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

  it('leaves an app out and back in from anywhere on its tag, or its keys', async () => {
    answer({
      topologies: { topologies: ['topo'] },
      'topologies/topo/scenarios': { scenarios: { s1: ['a'] } },
    });
    const wrapper = await card();
    await choose(wrapper, 'topology', 'topo');
    await choose(wrapper, 'scenario', 's1');
    const tag = () => wrapper.find('.create-exp-apps .tag');
    const pressed = () => tag().attributes('aria-pressed');

    await tag().trigger('click');
    expect(pressed()).toBe('false');
    await tag().trigger('keydown', { key: 'Enter' });
    expect(pressed()).toBe('true');
    await tag().trigger('keydown', { key: ' ' });
    expect(pressed()).toBe('false');
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
    ).toMatch(/15 or fewer characters/);
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

  it('orders the ends of a VLAN range as numbers', async () => {
    const wrapper = await card();
    const vlan = fc.integer({ min: 0, max: 4094 });
    // a minimum, often where one more digit begins, and a maximum often
    // next to it
    const range = fc
      .oneof(vlan, fc.constantFrom(9, 10, 99, 100, 999, 1000))
      .chain((min) =>
        fc.tuple(
          fc.constant(min),
          fc.oneof(
            vlan,
            fc
              .constantFrom(min - 1, min, min + 1)
              .filter((max) => max >= 0 && max <= 4094),
          ),
        ),
      );
    await fc.assert(
      fc.asyncProperty(range, async ([min, max]) => {
        await byLabel(wrapper, 'VLAN range minimum').setValue(String(min));
        await byLabel(wrapper, 'VLAN range maximum').setValue(String(max));
        const error = message(
          byLabel(wrapper, 'VLAN range minimum'),
          '.create-exp-vlans',
        );
        expect(error).toEqual(
          max < min ? expect.stringMatching(/below/) : null,
        );
      }),
      { numRuns: 30 },
    );
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
    await wrapper.find('.create-exp-apps .tag').trigger('click');
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
        { start: false },
      ],
    ]);
  });

  // the footer's buttons, as the buttons() reader writes them
  const footer = (wrapper) => buttons(wrapper.find('footer'));

  it('offers Create and Start to a role that may start the experiment', async () => {
    const wrapper = await filled();
    expect(footer(wrapper)).toEqual(['Create Experiment']);

    await wrapper.setProps({ startAllowed: (name) => name === 'exp' });
    expect(footer(wrapper)).toEqual(['Create and Start', 'Create Experiment']);
    await wrapper
      .findAll('footer button')
      .find((b) => textOf(b) === 'Create and Start')
      .trigger('click');
    expect(wrapper.emitted('create')[0][1]).toEqual({ start: true });
  });

  it('spins the button clicked and creates nothing more while pending', async () => {
    const wrapper = await filled();
    await wrapper.setProps({ startAllowed: () => true });
    await create(wrapper);
    await wrapper.setProps({ pending: true });

    expect(footer(wrapper)).toEqual([
      'Create and Start (disabled)',
      'Create Experiment (spinning) (disabled)',
    ]);
    await create(wrapper);
    expect(wrapper.emitted('create')).toHaveLength(1);
  });

  it('shows why the create failed, keeping what was typed', async () => {
    const wrapper = await filled();
    await create(wrapper);
    await wrapper.setProps({
      error: 'experiment exp exists: try another name',
    });

    const error = wrapper.find('.create-exp-error');
    expect(textOf(error)).toBe('experiment exp exists: try another name');
    expect(error.attributes('role')).toBe('alert');
    expect(nameInput(wrapper).element.value).toBe('exp');
    // hidden while the next try is under way
    await wrapper.setProps({ pending: true });
    expect(wrapper.find('.create-exp-error').exists()).toBe(false);
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

  // JSON lists and objects, with space around them and inside them
  const json = fc
    .tuple(
      fc.oneof(
        fc.array(fc.jsonValue()),
        fc.dictionary(fc.string(), fc.jsonValue()),
      ),
      fc.constantFrom(undefined, 2, '\t'),
      fc.stringMatching(/^\s*$/),
    )
    .map(([value, indent, pad]) => ({
      value: JSON.parse(JSON.stringify(value)),
      text: pad + JSON.stringify(value, null, indent) + pad,
    }));

  it('reads any JSON list or object back as itself', () => {
    fc.assert(
      fc.property(json, ({ value, text }) => {
        expect(customNodeValue(text)).toEqual(value);
      }),
    );
  });

  it('keeps any other text as it was typed', () => {
    const text = fc.string({ unit: 'binary' }).filter((s) => {
      const t = s.trim();
      return t != 'true' && t != 'false' && !/^[[{]/.test(t);
    });
    fc.assert(
      fc.property(text, (s) => {
        expect(customNodeValue(s)).toBe(s);
      }),
    );
  });

  it('sends back the annotations a node has when its rows are left as they were', () => {
    const key = fc.oneof(
      fc.constantFrom(...NODE_ANNOTATIONS.map((e) => e.key)),
      fc.string({ minLength: 1 }).filter((s) => s.trim() === s),
    );
    const value = fc.oneof(
      fc.boolean(),
      fc.string(),
      fc.array(fc.string()),
      fc.jsonValue(),
    );
    fc.assert(
      fc.property(fc.dictionary(key, value), (annotations) => {
        const rows = annotationRowsFrom(
          annotations,
          NODE_ANNOTATIONS,
          CUSTOM_NODE_ANNOTATION,
        );
        expect(nodeAnnotationsPayload(rows)).toEqual(annotations);
      }),
    );
  });

  it('reads back, from the text of its row, any value a row can show', () => {
    const typeOf = (key) => NODE_ANNOTATIONS.find((e) => e.key == key)?.type;
    const listItem = fc.stringMatching(/^[^,\s](?:[^,]*[^,\s])?$/);
    // values the row's text keeps the type of: no number, null, or text that
    // reads as true, false or JSON
    const valueFor = {
      boolean: fc.boolean(),
      list: fc.array(listItem),
      password: fc.string(),
      string: fc.string(),
      text: fc.oneof(
        fc.boolean(),
        fc.string().filter((s) => {
          const t = s.trim();
          return t != 'true' && t != 'false' && !/^[[{]/.test(t);
        }),
        fc
          .oneof(
            fc.array(fc.jsonValue()),
            fc.dictionary(fc.string(), fc.jsonValue()),
          )
          .map((v) => JSON.parse(JSON.stringify(v))),
      ),
    };
    const key = fc.oneof(
      fc.constantFrom(...NODE_ANNOTATIONS.map((e) => e.key)),
      fc.string({ minLength: 1 }).filter((s) => s.trim() === s && !typeOf(s)),
    );
    const entries = fc
      .uniqueArray(key, { maxLength: 6 })
      .chain((keys) =>
        fc.tuple(
          ...keys.map((k) =>
            fc.tuple(fc.constant(k), valueFor[typeOf(k) ?? 'text']),
          ),
        ),
      );
    fc.assert(
      fc.property(entries, (pairs) => {
        const annotations = Object.fromEntries(pairs);
        const rows = annotationRowsFrom(
          annotations,
          NODE_ANNOTATIONS,
          CUSTOM_NODE_ANNOTATION,
        ).map((row) => ({ ...row, original: undefined }));
        expect(nodeAnnotationsPayload(rows)).toEqual(annotations);
      }),
    );
  });

  it('keeps the type of a value its row shows as other text, while the row is left as it was', () => {
    const rows = annotationRowsFrom(
      { n: 5, none: null, word: 'true', 'phenix/default-apps': 'false' },
      NODE_ANNOTATIONS,
      CUSTOM_NODE_ANNOTATION,
    );
    expect(nodeAnnotationsPayload(rows)).toEqual({
      n: 5,
      none: null,
      word: 'true',
      'phenix/default-apps': 'false',
    });
    // once the text is typed again, it is read as any typed text
    const typed = rows.map((row) => ({ ...row, original: undefined }));
    expect(nodeAnnotationsPayload(typed)).toEqual({
      n: '5',
      none: 'null',
      word: true,
      'phenix/default-apps': false,
    });
  });

  it('sends a typed list as its items, whatever the spacing and empty entries', () => {
    const item = fc.stringMatching(/^[^,\s](?:[^,]*[^,\s])?$/);
    const space = fc.stringMatching(/^ *$/);
    const typed = fc
      .array(fc.tuple(space, fc.option(item), space))
      .map((parts) => ({
        items: parts.flatMap(([, i]) => (i === null ? [] : [i])),
        text: parts.map(([a, i, b]) => a + (i ?? '') + b).join(','),
      }));
    fc.assert(
      fc.property(typed, ({ items, text }) => {
        const entry = NODE_ANNOTATIONS.find((e) => e.type == 'list');
        const row = { ...annotationRow(entry), value: text };
        expect(nodeAnnotationsPayload([row])).toEqual({
          'phenix/startup-autotunnel': items,
        });
      }),
    );
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

  describe('for any rows', () => {
    const rowArb = fc.record({
      key: fc.constantFrom(
        '',
        ' ',
        'a',
        ' a',
        'a ',
        'b',
        'topology',
        'scenario',
      ),
      type: fc.constantFrom('text', 'string', 'list', 'boolean'),
      value: fc.oneof(fc.boolean(), fc.constantFrom('', ' ', 'v')),
    });
    const rowsArb = fc.array(rowArb, { maxLength: 8 });
    const errors = (rows) =>
      annotationErrors(rows, MANAGED_EXPERIMENT_ANNOTATIONS);

    it('judges each row by the rows above it alone', () => {
      fc.assert(
        fc.property(rowsArb, fc.nat(), (rows, n) => {
          const k = n % (rows.length + 1);
          expect(errors(rows.slice(0, k))).toEqual(errors(rows).slice(0, k));
        }),
      );
    });

    it('calls every row a repeat but the first with its key', () => {
      fc.assert(
        fc.property(rowsArb, (rows) => {
          const keys = rows.map((row) => row.key.trim()).filter(Boolean);
          const repeats = errors(rows).filter((e) =>
            e?.endsWith(' is already in the list'),
          );
          expect(repeats).toHaveLength(keys.length - new Set(keys).size);
        }),
      );
    });
  });
});
