// The running experiment page's header: its facts, its Apps box and the VM
// table's label filter, and the rules they lay out and list by.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import ExperimentAppsBox from '@/components/experiment/ExperimentAppsBox.vue';
import ExperimentSummary from '@/components/experiment/ExperimentSummary.vue';
import VmLabelFilter from '@/components/experiment/VmLabelFilter.vue';
import {
  appStateAfter,
  appTileState,
  balancedColumns,
  experimentScenarios,
  labelOptions,
} from '@/components/experiment/experimentHeader.js';
import { vm } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import { mountComponent, textOf, tooltipButtons } from './helpers/mount.js';

beforeEach(() => resetMocks());

describe('the scenarios', () => {
  it.each([
    ['none', {}, []],
    ['its own alone', { scenario: 'scn' }, ['scn']],
    [
      'its own, from a cached copy',
      { scenario: 'scn', scenarios: [] },
      ['scn'],
    ],
    [
      'every scenario the server lists',
      { scenario: 'scn', scenarios: ['scn', 'base'] },
      ['scn', 'base'],
    ],
  ])('names %s', (_, experiment, want) => {
    expect(experimentScenarios(experiment)).toEqual(want);
  });
});

describe('the app grid', () => {
  it.each([
    [0, 4, 1],
    [1, 4, 1],
    [4, 4, 4],
    [5, 4, 3],
    [6, 4, 3],
    [7, 4, 4],
    [9, 4, 3],
  ])('lays %i tiles in rows of up to %i in %i columns', (n, max, cols) => {
    expect(balancedColumns(n, max)).toBe(cols);
  });

  it('fills as few rows as fit, leaving fewer gaps than rows', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 60 }),
        fc.integer({ min: 1, max: 8 }),
        (n, max) => {
          const cols = balancedColumns(n, max);
          const rows = Math.ceil(n / cols);
          expect(cols).toBeLessThanOrEqual(max);
          expect(rows).toBe(Math.ceil(n / max));
          expect(rows * cols - n).toBeLessThan(rows);
        },
      ),
    );
  });

  it.each([
    ['start', 'running'],
    ['success', 'succeeded'],
    ['error', 'failed'],
    ['canceled', 'canceled'],
    ['delete', null],
  ])('reads an app %s message as %s', (action, state) => {
    expect(appStateAfter(action)).toBe(state);
  });

  it('shows an app as last heard of, or as built in or not', () => {
    expect(appTileState('ntp', {})).toBe('builtin');
    expect(appTileState('mine', {})).toBe('user');
    expect(appTileState('ntp', { ntp: 'failed' })).toBe('failed');
  });
});

describe('the label options', () => {
  it('lists the server and row labels and those checked, once each, in order', () => {
    const rows = [
      vm({ tags: { role: 'web', __notes_1: 'a note', __internal: 'x' } }),
      vm({ name: 'db', tags: { tier: 'db' } }),
    ];
    expect(
      labelOptions(
        [
          { key: 'role', value: 'web' },
          { key: 'role', value: 'app' },
        ],
        rows,
        [{ key: 'zone', value: 'gone' }],
      ),
    ).toEqual([
      { key: 'role', value: 'app' },
      { key: 'role', value: 'web' },
      { key: 'tier', value: 'db' },
      { key: 'zone', value: 'gone' },
    ]);
  });

  it('lists each label it is given once, sorted, and none of phenix’s own', () => {
    const key = fc.constantFrom('a', 'b', '__notes_1', '__x', 'a=b');
    const label = fc.record({ key, value: fc.constantFrom('', 'x', 'y') });
    const userLabel = label.filter((l) => !l.key.startsWith('__'));
    fc.assert(
      fc.property(
        fc.array(userLabel, { maxLength: 6 }),
        fc.array(fc.dictionary(key, fc.constantFrom('x', 'y')), {
          maxLength: 4,
        }),
        fc.array(userLabel, { maxLength: 3 }),
        (listed, tagSets, picked) => {
          const got = labelOptions(
            listed,
            tagSets.map((tags) => vm({ tags })),
            picked,
          );
          const ids = got.map((l) => JSON.stringify([l.key, l.value]));
          expect(new Set(ids).size).toBe(ids.length);
          expect(ids).toEqual(
            [...ids].sort((a, b) => {
              const [ka, va] = JSON.parse(a);
              const [kb, vb] = JSON.parse(b);
              return ka < kb
                ? -1
                : ka > kb
                  ? 1
                  : va < vb
                    ? -1
                    : va > vb
                      ? 1
                      : 0;
            }),
          );
          expect(got.every((l) => !l.key.startsWith('__'))).toBe(true);
          for (const l of [...listed, ...picked]) {
            expect(got).toContainEqual(l);
          }
          for (const tags of tagSets) {
            for (const [k, v] of Object.entries(tags)) {
              if (!k.startsWith('__'))
                expect(got).toContainEqual({ key: k, value: v });
            }
          }
        },
      ),
    );
  });
});

describe('the experiment summary', () => {
  // a fact as its term and its description
  const fact = (f) => `${textOf(f.find('dt'))}: ${textOf(f.find('dd'))}`;
  const render = (experiment) =>
    mountComponent(ExperimentSummary, { props: { name: 'exp1', experiment } });

  it.each([
    ['one scenario', { scenario: 'scn' }, 'Scenario: scn'],
    [
      'every scenario',
      { scenario: 'scn', scenarios: ['scn', 'base'] },
      'Scenarios: scn, base',
    ],
    ['no scenario', {}, 'Scenario: None'],
  ])('names %s', async (_, experiment, text) => {
    const wrapper = await render(experiment);
    expect(fact(wrapper.find('.exp-fact-scenarios'))).toBe(text);
  });

  it("shows the experiment's name, topology, counts and start", async () => {
    const wrapper = await render({
      topology: 'topo',
      vm_count: 3,
      vlans: [{ vlan: 101, alias: 'EXP' }],
      start_time: '2026-10-28T10:00:00Z',
    });
    expect(textOf(wrapper.find('h1'))).toBe('exp1');
    const facts = wrapper.findAll('.exp-fact').map(fact);
    expect(facts.slice(1, 4)).toEqual(['Topology: topo', 'VMs: 3', 'VLANs: 1']);
    expect(facts[4]).toContain('2026-10-28T10:00:00Z');
    expect(wrapper.find('time').attributes('datetime')).toBe(
      '2026-10-28T10:00:00Z',
    );
  });
  it.each([
    ['links them to the Configs viewer', undefined, true],
    [
      'names them as text for a role that may not read configs',
      denyOnly(['configs', 'get']),
      false,
    ],
  ])('%s', async (_, role, linked) => {
    if (role) rbac.allowed = role;
    const wrapper = await render({
      topology: 'topo',
      scenarios: ['scn', 'base'],
    });
    const hrefs = wrapper
      .findAll('.exp-fact a')
      .map((a) => a.attributes('href'));
    expect(hrefs).toEqual(
      linked
        ? [
            '/configs/?view=Scenario/scn',
            '/configs/?view=Scenario/base',
            '/configs/?view=Topology/topo',
          ]
        : [],
    );
    expect(fact(wrapper.find('.exp-fact-scenarios'))).toBe(
      'Scenarios: scn, base',
    );
  });

  it('shows the start as the Experiments page does, the stamp as sent beside it', async () => {
    const wrapper = await render({ start_time: '2026-10-28T10:00:00Z' });
    const started = wrapper.find('.exp-fact-started');
    expect(textOf(started.find('time'))).toMatch(
      /^\d{2}:\d{2}:\d{2} \d{2}-\d{2}-2026 \(.+\)$/,
    );
    expect(textOf(started.find('.is-sr-only'))).toBe('(2026-10-28T10:00:00Z)');
  });
});

describe('the Apps box', () => {
  const render = (props) =>
    mountComponent(ExperimentAppsBox, {
      props: { experiment: 'exp1', apps: ['ntp', 'mine', 'other'], ...props },
    });

  const tiles = (wrapper) =>
    wrapper.findAll('.app-tile').map((tile) => ({
      text: textOf(tile),
      state: tile.classes().find((c) => c.startsWith('is-')),
      button: tile.find('button').exists(),
    }));

  it('gives each app a tile saying its state in words, user apps as buttons', async () => {
    const wrapper = await render({
      states: { mine: 'running', other: 'failed' },
    });
    expect(tiles(wrapper)).toEqual([
      { text: 'ntp, Built-in', state: 'is-builtin', button: false },
      { text: 'Trigger mine, Running', state: 'is-running', button: true },
      {
        text: 'Trigger other, Last run failed',
        state: 'is-failed',
        button: true,
      },
    ]);
    expect(textOf(wrapper.find('h2'))).toBe('Apps 3');
    expect(wrapper.find('.apps-grid').attributes('style')).toContain(
      '--apps-columns: 3',
    );
  });

  it('asks to trigger an app from its tile, or any from Trigger', async () => {
    const wrapper = await render();
    await wrapper.findAll('.app-tile button')[0].trigger('click');
    await wrapper.find('.apps-box-trigger').trigger('click');
    expect(wrapper.emitted('trigger')).toEqual([['mine'], []]);
  });

  it.each([
    [
      'a role that may not trigger them',
      { role: denyOnly(['experiments/trigger', 'create']) },
      'Cannot trigger apps: you do not have permission',
    ],
    [
      'only built-in apps',
      { apps: ['ntp', 'scorch'] },
      'Cannot trigger apps: this experiment has only built-in apps',
    ],
  ])(
    'greys out Trigger, and turns no tile into a button, for %s',
    async (_, { role, apps }, why) => {
      if (role) rbac.allowed = role;
      const wrapper = await render(apps ? { apps } : {});
      const trigger = tooltipButtons(wrapper).find(
        (b) => b.label === 'Trigger apps',
      );
      expect(trigger).toMatchObject({ tooltip: why, disabled: true });
      expect(wrapper.find('.app-tile button').exists()).toBe(false);

      // and the allowed case
      rbac.allowed = () => true;
      const allowed = await render();
      expect(
        tooltipButtons(allowed).find((b) => b.label === 'Trigger apps'),
      ).toMatchObject({ tooltip: 'pick apps to trigger', disabled: false });
    },
  );
});

describe('the label filter', () => {
  const web = { key: 'role', value: 'web' };
  const db = { key: 'tier', value: 'db' };
  const render = (props) =>
    mountComponent(VmLabelFilter, { props: { options: [web, db], ...props } });
  const box = (wrapper, text) =>
    wrapper
      .findAll('label.b-checkbox')
      .find((b) => textOf(b) === text)
      .find('input');

  it('checks and unchecks labels, and clears them', async () => {
    const wrapper = await render({ picked: [db] });
    expect(box(wrapper, 'tier=db').element.checked).toBe(true);
    await box(wrapper, 'role=web').setValue(true);
    await box(wrapper, 'tier=db').setValue(false);
    const clear = wrapper
      .findAll('button')
      .find((b) => textOf(b) === 'Clear labels');
    await clear.trigger('click');
    expect(wrapper.emitted('update:picked')).toEqual([[[db, web]], [[]], [[]]]);
  });

  it('names what it filters by, and how many are checked', async () => {
    const trigger = (w) => w.find('.vm-label-filter-trigger');
    expect(trigger(await render()).attributes('aria-label')).toBe(
      'Filter VMs by label',
    );
    const two = await render({ picked: [web, db] });
    expect(trigger(two).attributes('aria-label')).toBe(
      'Filter VMs by label, 2 checked',
    );
    expect(textOf(trigger(two))).toBe('Labels (2)');
  });

  it('is greyed out, saying why, when no VM has a label', async () => {
    const wrapper = await render({ options: [] });
    expect(
      tooltipButtons(wrapper).find((b) => b.label === 'Filter VMs by label'),
    ).toMatchObject({
      tooltip: 'Cannot filter by label: no VM has a label',
      disabled: true,
    });
  });
});
