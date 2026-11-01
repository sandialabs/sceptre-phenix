// The running experiment page's header: its facts, its Apps box and the VM
// table's label and host filters, and the rules they lay out and list by.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import ExperimentAppsBox from '@/components/experiment/ExperimentAppsBox.vue';
import ExperimentSummary from '@/components/experiment/ExperimentSummary.vue';
import VmHostFilter from '@/components/experiment/VmHostFilter.vue';
import VmLabelFilter from '@/components/experiment/VmLabelFilter.vue';
import {
  appStateAfter,
  appTileState,
  balancedColumns,
  experimentScenarios,
  hostOptions,
  labelOptions,
} from '@/components/experiment/experimentHeader.js';
import { componentDeclarations } from './helpers/css.js';
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

describe('the host options', () => {
  it('lists the server and row hosts and those checked, once each, in order', () => {
    const rows = [
      vm({ host: 'node3' }),
      vm({ name: 'db', host: '' }),
      vm({ name: 'ext', host: 'router', external: true }),
    ];
    expect(hostOptions(['node2', 'node1'], rows, ['gone'])).toEqual([
      'gone',
      'node1',
      'node2',
      'node3',
    ]);
  });

  it('lists each host it is given once, sorted', () => {
    const host = fc.constantFrom('', 'a', 'b', 'B', 'node10', 'node9');
    fc.assert(
      fc.property(
        fc.array(host.filter(Boolean), { maxLength: 5 }),
        fc.array(host, { maxLength: 5 }),
        fc.array(host.filter(Boolean), { maxLength: 3 }),
        (listed, rowHosts, picked) => {
          const got = hostOptions(
            listed,
            rowHosts.map((h) => vm({ host: h })),
            picked,
          );
          expect(new Set(got).size).toBe(got.length);
          expect(got).toEqual([...got].sort());
          expect(got).not.toContain('');
          for (const h of [...listed, ...picked, ...rowHosts.filter(Boolean)]) {
            expect(got).toContain(h);
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

  it.each([
    ['links to its state of health and SCORCH runs', undefined, true],
    [
      'leaves the links out for a role that may not get it',
      denyOnly(['experiments', 'get']),
      false,
    ],
  ])('%s, left of its status', async (_, role, linked) => {
    if (role) rbac.allowed = role;
    const wrapper = await render({});
    const head = wrapper.find('.exp-summary-actions');
    expect(head.findAll('a').map((a) => a.attributes('href'))).toEqual(
      linked ? ['/soh/exp1', '/scorch/exp1'] : [],
    );
    expect(head.findAll('a').map((a) => a.attributes('aria-label'))).toEqual(
      linked
        ? [
            'View state of health for experiment exp1',
            'View SCORCH runs for experiment exp1',
          ]
        : [],
    );
    // the links come first, then the status
    expect(textOf(head.element.lastElementChild)).toBe('Running');
  });

  it.each([
    ['the total alone until the running count is known', null, 'VMs: 3'],
    ['how many are running of all', 2, 'VMs: 2 running of 3'],
  ])('counts the VMs: %s', async (_, count, text) => {
    const wrapper = await mountComponent(ExperimentSummary, {
      props: { name: 'exp1', experiment: { vm_count: 3 }, running: count },
    });
    expect(fact(wrapper.find('.exp-fact-vms'))).toBe(text);
  });

  it('shows the start as the Experiments page does: as sent, then its age', async () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-28T13:00:00Z') });
    try {
      const wrapper = await render({ start_time: '2026-10-28T10:00:00Z' });
      const started = wrapper.find('.exp-fact-started dd');
      expect(textOf(started)).toBe('2026-10-28T10:00:00Z (3 hours ago)');
      expect(
        wrapper
          .findAllComponents({ name: 'BTooltip' })
          .some((t) => started.element.contains(t.element)),
      ).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows a start that is not a time as it came', async () => {
    const wrapper = await render({ start_time: 'garbled' });
    expect(textOf(wrapper.find('.exp-fact-started dd'))).toBe('garbled');
  });

  // "2026-10-28T14:03:22-06:00 (59 minutes ago)" fits on one line in 7 of the
  // 12 columns of a 44rem card, and "18 running of 25" in 3.
  it('widens the start over the counts in a wide card', () => {
    const file = 'src/components/experiment/ExperimentSummary.vue';
    expect(
      componentDeclarations(file, '.content dl.exp-summary-facts'),
    ).toMatchObject({ 'container-type': 'inline-size' });
    expect(componentDeclarations(file, '.exp-fact-started')).toEqual({
      'grid-column': 'span 6',
    });
    expect(
      componentDeclarations(file, '.exp-fact-started', '(min-width: 44rem)'),
    ).toEqual({ 'grid-column': 'span 7' });
    expect(
      componentDeclarations(
        file,
        '.has-start .exp-fact-vms',
        '(min-width: 44rem)',
      ),
    ).toEqual({ 'grid-column': 'span 3' });
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

describe('the label and host filters', () => {
  const web = { key: 'role', value: 'web' };
  const db = { key: 'tier', value: 'db' };
  // [kind, filter, its options, its title, why it may be greyed out]
  const filters = [
    ['label', VmLabelFilter, [web, db], 'Labels', 'no VM has a label'],
    [
      'host',
      VmHostFilter,
      ['node1', 'node2'],
      'Hosts',
      'no VM is running on a host',
    ],
  ];

  it.each(filters)(
    'the %s filter names what it filters by, and how many are checked',
    async (kind, Filter, options, title) => {
      const render = (props) =>
        mountComponent(Filter, { props: { options, ...props } });
      const trigger = (w) => w.find(`.vm-${kind}-filter-trigger`);
      expect(trigger(await render()).attributes('aria-label')).toBe(
        `Filter VMs by ${kind}`,
      );
      const two = await render({ picked: options });
      expect(trigger(two).attributes('aria-label')).toBe(
        `Filter VMs by ${kind}, 2 checked`,
      );
      expect(textOf(trigger(two))).toBe(`${title} (2)`);
    },
  );

  it.each(filters.map(([kind, Filter, , , why]) => [kind, Filter, why]))(
    'the %s filter is greyed out, saying why, with nothing to filter by',
    async (kind, Filter, why) => {
      const wrapper = await mountComponent(Filter, { props: { options: [] } });
      expect(
        tooltipButtons(wrapper).find(
          (b) => b.label === `Filter VMs by ${kind}`,
        ),
      ).toMatchObject({
        tooltip: `Cannot filter by ${kind}: ${why}`,
        disabled: true,
      });
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
});

describe('the host filter', () => {
  const render = (props) =>
    mountComponent(VmHostFilter, {
      props: { options: ['node1', 'node2'], ...props },
    });
  const box = (wrapper, text) =>
    wrapper
      .findAll('label.b-checkbox')
      .find((b) => textOf(b) === text)
      .find('input');

  it('hides its tooltip while its list is open', async () => {
    const wrapper = await render();
    const tooltip = () => wrapper.findComponent({ name: 'BTooltip' });
    expect(tooltip().props('active')).toBe(true);
    await wrapper.find('.vm-host-filter-trigger').trigger('click');
    // Buefy opens a dropdown on a timer
    await vi.waitFor(() => expect(tooltip().props('active')).toBe(false));
  });

  it('leaves unchecked the hosts that would make the request too long', async () => {
    const wrapper = await render({
      options: ['node1', 'node2', 'node3'],
      picked: ['node1'],
      fits: (hosts) => hosts.length <= 2,
    });
    const disabled = ['node1', 'node2', 'node3'].map(
      (h) => box(wrapper, h).element.disabled,
    );
    expect(disabled).toEqual([false, false, false]);

    await wrapper.setProps({ picked: ['node1', 'node2'] });
    expect(
      ['node1', 'node2', 'node3'].map((h) => box(wrapper, h).element.disabled),
    ).toEqual([false, false, true]);
    expect(textOf(wrapper)).toContain('Uncheck a host to check another');
  });
});

describe('the Hosts and Labels buttons', () => {
  // each button's classes and markup, with its own kind, icon and title
  // taken out
  const looks = async (Filter, options, kind, icon, title) => {
    const trigger = (await mountComponent(Filter, { props: { options } })).find(
      `.vm-${kind}-filter-trigger`,
    );
    expect(trigger.find(`[data-icon="${icon}"]`).exists()).toBe(true);
    return trigger
      .html()
      .replace(new RegExp(`(?<!aria-)${kind}`, 'g'), 'KIND')
      .replaceAll(icon, 'ICON')
      .replaceAll(title, 'TITLE');
  };

  it('look alike apart from their icon and title', async () => {
    expect(
      await looks(VmHostFilter, ['node1'], 'host', 'server', 'Hosts'),
    ).toBe(
      await looks(
        VmLabelFilter,
        [{ key: 'role', value: 'web' }],
        'label',
        'tag',
        'Labels',
      ),
    );
  });

  // a full-width glyph such as the server's fills its icon box, so Bulma's
  // 0.25em after the box left it touching the title
  it('leave room between the icon and the title', () => {
    expect(
      componentDeclarations(
        'src/components/experiment/VmPickFilter.vue',
        '.vm-pick-filter-trigger :deep(.icon:first-child:not(:last-child))',
      ),
    ).toEqual({ 'margin-inline-end': '0.5em' });
  });
});
