// The running experiment page's header: its facts, its Apps box and the VM
// table's label and host filters, and the rules they lay out and list by.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import ExperimentAppsBox from '@/components/experiment/ExperimentAppsBox.vue';
import ExperimentHeader from '@/components/experiment/ExperimentHeader.vue';
import ExperimentSummary from '@/components/experiment/ExperimentSummary.vue';
import VmHostFilter from '@/components/experiment/VmHostFilter.vue';
import VmLabelFilter from '@/components/experiment/VmLabelFilter.vue';
import {
  appStateAfter,
  appTileState,
  balancedColumns,
  builderRoute,
  experimentScenarios,
  HEADER_EXPANDED_PREF,
  hostOptions,
  hostsShown,
  labelOptions,
  triggerableApps,
  TRIGGER_STAGES,
  triggerBlocked,
} from '@/components/experiment/experimentHeader.js';
import { componentDeclarations } from './helpers/css.js';
import { vm } from './helpers/fixtures.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';
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

describe('the apps to trigger', () => {
  const apps = ['ntp', 'mine', 'scorch', 'other'];

  it.each(TRIGGER_STAGES.map((stage) => [stage]))(
    'offers at the %s stage the apps it can run there',
    (stage) => {
      expect(triggerableApps(apps, stage)).toEqual(
        stage === 'running' ? ['mine', 'other'] : apps,
      );
    },
  );

  it('offers nothing for an experiment without apps', () => {
    expect(triggerableApps(undefined, 'configure')).toEqual([]);
    expect(triggerableApps(null, 'running')).toEqual([]);
  });
});

describe('the Builder link', () => {
  it.each([
    ['no Builder fields', { topology: 't' }, null],
    ['an empty builder', { topology: 't', builder: {} }, null],
    ['no Builder annotation', { topology: 't', builder: null }, null],
    [
      'a draft',
      { topology: 't', builder: { draft: 'd1', document: 'doc' } },
      { name: 'builder', query: { draft: 'd1' } },
    ],
    [
      'a document alone',
      { topology: 't', builder: { document: 'doc' } },
      { name: 'builder', query: { topology: 't' } },
    ],
  ])('for an experiment with %s', (_, experiment, route) => {
    expect(builderRoute(experiment)).toEqual(route);
  });
});

describe('the host names', () => {
  it('shows the first few and counts the rest', () => {
    fc.assert(
      fc.property(
        fc.array(fc.string(), { maxLength: 8 }),
        fc.nat(5),
        (hosts, max) => {
          const { shown, more } = hostsShown(hosts, max);
          expect([...shown, ...hosts.slice(shown.length)]).toEqual(hosts);
          expect(shown.length).toBe(Math.min(hosts.length, max));
          expect(more).toBe(hosts.length - shown.length);
        },
      ),
      { numRuns: 100 },
    );
    expect(hostsShown(null, 3)).toEqual({ shown: [], more: 0 });
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
            '/vlans?experiment=exp1',
          ]
        : ['/vlans?experiment=exp1'],
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

  describe('the VMs that wait for a delayed start', () => {
    const summary = (delayed) =>
      mountComponent(ExperimentSummary, {
        props: {
          name: 'exp1',
          experiment: { vm_count: 5 },
          running: 3,
          delayed,
        },
      });
    const tip =
      'These VMs have a delayed start and did not start yet. Each VM ' +
      'starts after its timer, when its C2 hosts check in, or when a user ' +
      'starts it.';

    it.each([
      ['one', 1, '1 delayed VM waiting', '1 delayed'],
      ['some', 2, '2 delayed VMs waiting', '2 delayed'],
    ])(
      'shows %s as a blue tag after the status, and in the VMs tile',
      async (_, n, text, count) => {
        const wrapper = await summary(n);
        const tag = wrapper.find('.exp-summary-delayed');
        expect(tag.classes()).toEqual(
          expect.arrayContaining(['tag', 'is-info']),
        );
        expect(textOf(tag)).toBe(text);
        expect(tag.attributes('role')).toBe('note');
        expect(tag.attributes('aria-label')).toBe(text);
        const described = wrapper.find(
          `#${tag.attributes('aria-describedby')}`,
        );
        expect(textOf(described)).toBe(tip);

        const tooltip = wrapper
          .findAllComponents({ name: 'BTooltip' })
          .find((t) => t.element.contains(tag.element));
        expect(textOf(tooltip.vm.$refs.content)).toBe(tip);

        const head = wrapper.find('.exp-summary-actions');
        const tags = head.findAll('.tag').map((t) => textOf(t));
        expect(tags.slice(-2)).toEqual(['Running', text]);

        const dds = wrapper.findAll('.exp-fact-vms dd').map((dd) => textOf(dd));
        expect(dds).toEqual(['3 running of 5', count]);
      },
    );

    it.each([
      ['until the VM list says', null],
      ['when none waits', 0],
    ])('shows no tag or count %s', async (_, n) => {
      const wrapper = await summary(n);
      expect(wrapper.find('.exp-summary-delayed').exists()).toBe(false);
      expect(wrapper.find('#exp-summary-delayed-tip').exists()).toBe(false);
      expect(wrapper.findAll('.exp-fact-vms dd')).toHaveLength(1);
    });

    it('counts down to the next start of a VM that waits for a timer', async () => {
      const start = Date.parse('2026-01-01T10:00:00Z');
      vi.useFakeTimers({
        now: start,
        toFake: ['setInterval', 'clearInterval', 'Date'],
      });
      try {
        const wrapper = await mountComponent(ExperimentSummary, {
          props: {
            name: 'exp1',
            experiment: { vm_count: 5 },
            running: 3,
            delayed: 2,
            nextStart: start + 252_000,
          },
        });
        const tag = () => wrapper.find('.exp-summary-delayed');
        const text = '2 delayed VMs waiting, next starts in 4m 12s';
        expect(textOf(tag())).toBe(text);
        expect(tag().attributes('aria-label')).toBe(text);

        vi.advanceTimersByTime(1000);
        await nextTick();
        expect(textOf(tag())).toBe(
          '2 delayed VMs waiting, next starts in 4m 11s',
        );

        // the VMs that are left wait for a user or C2
        await wrapper.setProps({ delayed: 1, nextStart: null });
        expect(textOf(tag())).toBe('1 delayed VM waiting');
        expect(vi.getTimerCount()).toBe(0);

        await wrapper.setProps({ nextStart: start + 60_000 });
        expect(vi.getTimerCount()).toBe(1);
        wrapper.unmount();
        expect(vi.getTimerCount()).toBe(0);
      } finally {
        vi.useRealTimers();
      }
    });

    it('passes the next start from the header to its summary', async () => {
      const wrapper = await mountComponent(ExperimentHeader, {
        props: { name: 'exp1', delayed: 1, nextStart: 1234 },
      });
      expect(wrapper.findComponent(ExperimentSummary).props('nextStart')).toBe(
        1234,
      );
    });

    it('takes the tag and count away when the last VM starts', async () => {
      const wrapper = await summary(1);
      expect(wrapper.find('.exp-summary-delayed').exists()).toBe(true);
      await wrapper.setProps({ delayed: 0 });
      expect(wrapper.find('.exp-summary-delayed').exists()).toBe(false);
      expect(wrapper.find('.exp-fact-delayed').exists()).toBe(false);
    });
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

  describe('the hosts', () => {
    const summary = (hosts) =>
      mountComponent(ExperimentSummary, {
        props: { name: 'exp1', experiment: {}, hosts },
      });
    const tile = (wrapper) => wrapper.find('.exp-fact-hosts');
    // a description's text, leaving out the tooltip drawn only on hover
    const shownText = (dd) => {
      const copy = dd.element.cloneNode(true);
      copy.querySelectorAll('.tooltip-content').forEach((n) => n.remove());
      return textOf(copy);
    };

    it.each([
      ['no tile when the page gives none', undefined, null],
      ['that they load until the VM list says', null, ['Loading…']],
      ['none', [], ['0']],
      ['each of a few', ['a', 'b', 'c'], ['3', 'a, b, c']],
      [
        'the first few and how many more',
        ['a', 'b', 'c', 'd', 'e'],
        ['5', 'a, b, c, +2 more: d, e'],
      ],
    ])('shows %s', async (_, hosts, dds) => {
      const wrapper = await summary(hosts);
      expect(tile(wrapper).exists()).toBe(dds !== null);
      if (dds) {
        expect(tile(wrapper).findAll('dd').map(shownText)).toEqual(dds);
        expect(textOf(tile(wrapper).find('dt'))).toBe('Hosts');
      }
    });

    it('names them all in the tooltip of the more, which the keyboard reaches', async () => {
      const wrapper = await summary(['a', 'b', 'c', 'd']);
      const more = tile(wrapper).find('.exp-fact-more');
      expect(textOf(more)).toBe('+1 more');
      expect(more.attributes('tabindex')).toBe('0');
      const tooltip = tile(wrapper).findComponent({ name: 'BTooltip' });
      expect(textOf(tooltip.vm.$refs.content)).toBe('a, b, c, d');
    });
  });

  it('wraps the scenarios over up to three lines, naming them all in a title', async () => {
    const scenarios = ['first', 'second', 'third'];
    const wrapper = await render({ scenario: 'first', scenarios });
    const dd = wrapper.find('.exp-fact-scenarios dd');
    expect(dd.attributes('title')).toBe('first, second, third');
    expect(
      componentDeclarations(
        'src/components/experiment/ExperimentSummary.vue',
        '.content .exp-fact dd.exp-fact-scenario-list',
      ),
    ).toMatchObject({
      'white-space': 'normal',
      'overflow-wrap': 'anywhere',
      '-webkit-line-clamp': '3',
      'line-clamp': '3',
    });
  });

  it.each([
    ['a role that may use the Builder', undefined, true],
    ['a role that may not', denyOnly(['builder', 'use']), false],
    [
      'a role that may only use the Builder',
      allowOnly(['builder', 'use']),
      true,
    ],
  ])(
    'links a Builder experiment to its draft for %s',
    async (_, role, shown) => {
      if (role) rbac.allowed = role;
      const wrapper = await render({ topology: 't', builder: { draft: 'd1' } });
      const link = wrapper.find('.exp-summary-builder');
      expect(link.exists()).toBe(shown);
      if (shown) {
        expect(link.element.tagName).toBe('A');
        expect(link.attributes('href')).toBe('/builder?draft=d1');
        expect(textOf(link)).toBe('Open in Builder');
      }
    },
  );

  it('offers no Builder link for an experiment the Builder did not make', async () => {
    const wrapper = await render({ topology: 't' });
    expect(wrapper.find('.exp-summary-builder').exists()).toBe(false);
  });

  // DOM order: scenarios, topology, hosts, VMs, VLANs, start
  it('fills every row of tiles, with or without a start or hosts, in a narrow or wide card', () => {
    const file = 'src/components/experiment/ExperimentSummary.vue';
    const span = (selectors, wide) => {
      let found;
      for (const media of wide ? [null, '(min-width: 44rem)'] : [null]) {
        for (const selector of selectors.filter(Boolean)) {
          const value = componentDeclarations(file, selector, media)[
            'grid-column'
          ];
          if (value) found = Number(value.replace('span ', ''));
        }
      }
      return found;
    };
    for (const start of [false, true]) {
      for (const hosts of [false, true]) {
        for (const wide of [false, true]) {
          const tiles = [
            [
              '.exp-fact-scenarios',
              start && hosts && '.has-start.has-hosts .exp-fact-scenarios',
            ],
            ['.exp-fact-topology'],
            hosts && ['.exp-fact-hosts', start && '.has-start .exp-fact-hosts'],
            [
              '.exp-fact-vms',
              !start &&
                !hosts &&
                '.exp-summary-facts:not(.has-hosts):not(.has-start) .exp-fact-vms',
              start && '.has-start .exp-fact-vms',
            ],
            ['.exp-fact-vlans'],
            start && ['.exp-fact-started'],
          ].filter(Boolean);
          const rows = [0];
          for (const tile of tiles) {
            const n = span(tile, wide);
            if (rows.at(-1) + n > 12) rows.push(0);
            rows[rows.length - 1] += n;
          }
          expect({ start, hosts, wide, rows }).toEqual({
            start,
            hosts,
            wide,
            rows: [12, 12],
          });
        }
      }
    }
    expect(componentDeclarations(file, '.exp-fact-vlans')).toEqual({
      'grid-column': 'span 2',
    });
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

describe("a stopped experiment's summary", () => {
  const render = (props) =>
    mountComponent(ExperimentSummary, {
      props: {
        name: 'exp1',
        stopped: true,
        experiment: {
          vm_count: 4,
          vlans: [],
          start_time: '2026-10-28T10:00:00Z',
        },
        ...props,
      },
    });

  it('says it is stopped, counts its VMs and leaves out the start of its last run', async () => {
    const wrapper = await render();
    expect(textOf(wrapper.find('.exp-summary-status'))).toBe('Stopped');
    expect(
      wrapper.find('.exp-summary-status [data-icon="stop"]').exists(),
    ).toBe(true);
    expect(textOf(wrapper.find('.exp-fact-vms dd'))).toBe('4');
    expect(wrapper.find('.exp-fact-started').exists()).toBe(false);
    expect(wrapper.find('.exp-summary-facts').classes('has-start')).toBe(false);
  });

  it.each([
    [undefined, null],
    [null, 'Loading…'],
    [['n1', 'n2'], '2'],
  ])('shows the scheduled hosts %j', async (hosts, text) => {
    const wrapper = await render({ hosts });
    const tile = wrapper.find('.exp-fact-hosts');
    expect(tile.exists()).toBe(text !== null);
    if (text) {
      expect(textOf(tile.find('dt'))).toBe('Scheduled hosts');
      expect(textOf(tile.find('dd'))).toBe(text);
    }
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

  it('of a stopped experiment says only whether each app is built in, with no trigger', async () => {
    const wrapper = await render({
      stopped: true,
      states: { mine: 'running', other: 'failed' },
    });
    expect(tiles(wrapper)).toEqual([
      { text: 'ntp, Built-in', state: 'is-builtin', button: false },
      { text: 'mine, User app', state: 'is-user', button: false },
      { text: 'other, User app', state: 'is-user', button: false },
    ]);
    expect(wrapper.find('.apps-box-trigger').exists()).toBe(false);
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

describe('why apps cannot be triggered', () => {
  it.each([
    ['no permission', false, ['mine'], 'you do not have permission'],
    ['only built-in apps', true, ['ntp', 'scorch'], 'only built-in apps'],
    ['no apps', true, undefined, 'only built-in apps'],
  ])('says so for %s', (_, allowed, apps, why) => {
    expect(triggerBlocked(allowed, apps)).toContain(why);
    // and the allowed case
    expect(triggerBlocked(true, ['ntp', 'mine'])).toBeNull();
  });
});

describe('the collapsible header', () => {
  const experiment = {
    name: 'exp1',
    vm_count: 7,
    vlans: [{ vlan: 101 }, { vlan: 102 }, { vlan: 103 }],
    apps: ['ntp', 'mine', 'other', 'scorch'],
    start_time: '2026-10-28T10:00:00Z',
    builder: { draft: 'd1' },
  };
  const render = (props, opts) =>
    mountComponent(ExperimentHeader, {
      ...opts,
      props: {
        name: 'exp1',
        experiment,
        running: 5,
        delayed: 2,
        hosts: ['n1', 'n2'],
        ...props,
      },
    });
  const toggle = (wrapper) => wrapper.find('.experiment-header-toggle');
  const shown = (node) => node.element.style.display !== 'none';
  const brief = (wrapper) => textOf(wrapper.find('.exp-summary-brief'));

  it('starts collapsed to one line: name, status, delayed tag, links, counts and Trigger', async () => {
    const wrapper = await render();
    expect(wrapper.classes()).toContain('is-collapsed');
    expect(shown(wrapper.find('#exp-summary-facts'))).toBe(false);
    expect(shown(wrapper.find('#experiment-header-apps'))).toBe(false);

    const head = wrapper.find('.exp-summary-head');
    expect(textOf(head.find('h1'))).toBe('exp1');
    expect(textOf(head.find('.exp-summary-status'))).toBe('Running');
    expect(head.find('.exp-summary-status [data-icon]').exists()).toBe(false);
    expect(textOf(head.find('.exp-summary-delayed'))).toBe(
      '2 delayed VMs waiting',
    );
    expect(head.findAll('a').map((a) => a.attributes('href'))).toEqual([
      '/soh/exp1',
      '/scorch/exp1',
      '/builder?draft=d1',
    ]);
    expect(brief(wrapper)).toBe(
      '5 of 7 VMs running · 3 VLANs · 2 hosts · 4 apps',
    );
    // the separators are not read out
    expect(
      wrapper.findAll('.exp-summary-brief [aria-hidden="true"]'),
    ).toHaveLength(3);

    const trigger = head.find('.exp-summary-end .apps-box-trigger');
    expect(textOf(trigger)).toBe('Trigger');
    await trigger.trigger('click');
    expect(wrapper.emitted('trigger')).toEqual([[]]);
  });

  it('names what its toggle shows and hides, and whether they show', async () => {
    const wrapper = await render();
    const button = toggle(wrapper);
    expect(button.element.tagName).toBe('BUTTON');
    expect(button.attributes('type')).toBe('button');
    expect(textOf(button)).toBe('Details');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(button.attributes('aria-controls')).toBe(
      'exp-summary-facts experiment-header-apps',
    );
    for (const id of button.attributes('aria-controls').split(' ')) {
      expect(wrapper.find(`#${id}`).exists()).toBe(true);
    }
    expect(button.find('[data-icon="chevron-down"]').exists()).toBe(true);

    // without apps, it controls the tiles alone
    const bare = await render({ experiment: { ...experiment, apps: [] } });
    expect(toggle(bare).attributes('aria-controls')).toBe('exp-summary-facts');
    expect(bare.find('#experiment-header-apps').exists()).toBe(false);
  });

  it('keeps Details at the top right: after Trigger in the Apps box beside the summary', async () => {
    const wrapper = await render({}, { attachTo: document.body });
    expect(
      wrapper.find('.exp-summary-end .experiment-header-toggle').exists(),
    ).toBe(true);

    await toggle(wrapper).trigger('click');
    expect(wrapper.findAll('.experiment-header-toggle')).toHaveLength(1);
    const end = wrapper.find('.apps-box-head .apps-box-end');
    expect(end.findAll('button').map(textOf)).toEqual(['Trigger', 'Details']);
    // focus moves with it
    expect(document.activeElement).toBe(toggle(wrapper).element);

    await toggle(wrapper).trigger('click');
    expect(
      wrapper.find('.exp-summary-end .experiment-header-toggle').exists(),
    ).toBe(true);
  });

  it('keeps Details in the summary when the Apps box wraps below it, or is absent', async () => {
    localStorage.setItem(HEADER_EXPANDED_PREF, 'true');
    const style = window.getComputedStyle;
    const bases = {
      'experiment-header-summary': '512px',
      'experiment-header-apps': '576px',
    };
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el) => {
      const key = Object.keys(bases).find((c) => el.classList.contains(c));
      return key
        ? { flexBasis: bases[key] }
        : el.classList.contains('experiment-header')
          ? { columnGap: '16px' }
          : style(el);
    });
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
    const wrapper = await render();
    expect(
      wrapper.find('.exp-summary-end .experiment-header-toggle').exists(),
    ).toBe(true);
    expect(
      wrapper.find('.apps-box-end .experiment-header-toggle').exists(),
    ).toBe(false);

    const bare = await render({ experiment: { ...experiment, apps: [] } });
    expect(
      bare.find('.exp-summary-end .experiment-header-toggle').exists(),
    ).toBe(true);
    vi.restoreAllMocks();
  });

  it('expands to the tiles and Apps box, and back, remembering each choice', async () => {
    const wrapper = await render();
    await toggle(wrapper).trigger('click');
    expect(localStorage.getItem(HEADER_EXPANDED_PREF)).toBe('true');
    expect(toggle(wrapper).attributes('aria-expanded')).toBe('true');
    expect(toggle(wrapper).find('[data-icon="chevron-up"]').exists()).toBe(
      true,
    );
    expect(wrapper.classes()).not.toContain('is-collapsed');
    expect(shown(wrapper.find('#exp-summary-facts'))).toBe(true);
    expect(shown(wrapper.find('#experiment-header-apps'))).toBe(true);
    expect(wrapper.find('.exp-summary-brief').exists()).toBe(false);
    // Trigger shows once, in the Apps box
    expect(wrapper.findAll('.apps-box-trigger')).toHaveLength(1);
    expect(wrapper.find('.exp-summary-end .apps-box-trigger').exists()).toBe(
      false,
    );
    // the delayed tag stays in the head
    expect(
      wrapper.find('.exp-summary-head .exp-summary-delayed').exists(),
    ).toBe(true);
    await wrapper.find('.app-tile button').trigger('click');
    await wrapper.find('.apps-box .apps-box-trigger').trigger('click');
    expect(wrapper.emitted('trigger')).toEqual([['mine'], []]);

    await toggle(wrapper).trigger('click');
    expect(localStorage.getItem(HEADER_EXPANDED_PREF)).toBe('false');
    expect(toggle(wrapper).attributes('aria-expanded')).toBe('false');
    expect(shown(wrapper.find('#exp-summary-facts'))).toBe(false);
  });

  it.each([
    ['expanded', 'true', true],
    ['collapsed', 'false', false],
    ['collapsed when it has nothing', null, false],
  ])('opens %s as this browser last left it', async (_, pref, expanded) => {
    if (pref !== null) localStorage.setItem(HEADER_EXPANDED_PREF, pref);
    const wrapper = await render();
    expect(toggle(wrapper).attributes('aria-expanded')).toBe(String(expanded));
    expect(shown(wrapper.find('#exp-summary-facts'))).toBe(expanded);
  });

  it('collapses when the browser cannot store the choice', async () => {
    const getItem = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const wrapper = await render();
    expect(toggle(wrapper).attributes('aria-expanded')).toBe('false');
    getItem.mockRestore();
  });

  it.each([
    [
      'the VMs alone until the running count is known',
      { running: null },
      '7 VMs · 3 VLANs · 2 hosts · 4 apps',
    ],
    [
      'no hosts while they load',
      { hosts: null },
      '5 of 7 VMs running · 3 VLANs · 4 apps',
    ],
    [
      'no apps for an experiment without them',
      { experiment: { ...experiment, apps: undefined, vlans: [{ vlan: 1 }] } },
      '5 of 7 VMs running · 1 VLAN · 2 hosts',
    ],
    [
      'the scheduled hosts of a stopped experiment, and no unknown count',
      {
        stopped: true,
        running: null,
        delayed: null,
        vmCount: null,
        hosts: ['n1'],
      },
      '3 VLANs · 1 scheduled host · 4 apps',
    ],
  ])('counts %s', async (_, props, text) => {
    expect(brief(await render(props))).toBe(text);
  });

  it('of a stopped experiment says so and offers no Trigger', async () => {
    const wrapper = await render({
      stopped: true,
      running: null,
      delayed: null,
    });
    expect(textOf(wrapper.find('.exp-summary-status'))).toBe('Stopped');
    expect(wrapper.find('.apps-box-trigger').exists()).toBe(false);
    await toggle(wrapper).trigger('click');
    expect(wrapper.find('.apps-box-trigger').exists()).toBe(false);
  });
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
