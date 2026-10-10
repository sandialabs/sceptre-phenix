import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import {
  GROUPING_STRATEGIES,
  GROUP_PATTERN_STORAGE_KEY,
  GROUP_PATTERN_TIMEOUT_MS,
  PatternError,
  applyGroups,
  groupingPhrase,
  nothingToGroup,
  nothingToGroupByPattern,
  planGroups,
  planPatternGroups,
  readGroupPattern,
  rememberGroupPattern,
} from '@/builder/grouping.js';
import {
  GROUP_NAME_MAX,
  GROUP_PATTERN_MAX,
  matchTexts,
  patternProblem,
} from '@/builder/groupingPattern.js';
import {
  DEFAULT_NETWORK_COLORS,
  addNetwork,
  addNode,
  connect,
  createDocument,
  findNode,
  groupNodes,
  sizeOf,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

import { memoryStorage } from './fixtures.js';

// A small plant: every device on MGMT, the turbines and a router on ot, the
// router and a workstation on site, pwds on MGMT alone, and a note.
function plant() {
  let doc = createDocument({ name: 'Plant' });
  const networks = {};
  const switches = {};
  const devices = {};

  for (const [name, color] of [
    ['MGMT', '#2f6fbf'],
    ['ot', '#b05c17'],
    ['site', ''],
  ]) {
    const made = addNetwork(doc, { name, color });

    doc = made.doc;
    networks[name] = made.network;

    const sw = addNode(doc, {
      kind: 'switch',
      networkId: made.network.id,
      position: { x: 0, y: 0 },
    });

    doc = sw.doc;
    switches[name] = sw.node;
  }

  const hosts = {
    'wtg-01': ['ot', 'MGMT'],
    'wtg-02': ['ot', 'MGMT'],
    'WTG-10': ['ot', 'MGMT'],
    'rtr-site': ['ot', 'site', 'MGMT'],
    'ws-01': ['site'],
    pwds: ['MGMT'],
  };

  Object.entries(hosts).forEach(([hostname, joined], index) => {
    const made = addNode(doc, {
      kind: 'device',
      hostname,
      position: { x: index * 40, y: index * 30 },
    });

    doc = made.doc;
    devices[hostname] = made.node;

    for (const network of joined) {
      doc = connect(doc, {
        sourceNodeId: made.node.id,
        targetNodeId: switches[network].id,
      }).doc;
    }
  });

  doc = addNode(doc, { kind: 'note', text: 'hello' }).doc;

  return { doc, networks, switches, devices };
}

const ids = (nodes) => nodes.map((node) => node.id);

describe('Auto-group plans', () => {
  test('offers grouping by network first, then by name, then by name pattern', () => {
    expect(GROUPING_STRATEGIES.map((strategy) => strategy.id)).toEqual([
      'network',
      'name',
      'pattern',
    ]);
    // Only the pattern rule asks for something first.
    expect(
      GROUPING_STRATEGIES.filter((strategy) => strategy.asks).map(
        (strategy) => strategy.id,
      ),
    ).toEqual(['pattern']);
    expect(GROUPING_STRATEGIES.map((strategy) => strategy.phrase)).toEqual([
      'by network',
      'by name',
      'by name pattern',
    ]);
    expect(groupingPhrase('name')).toBe('by name');
    expect(groupingPhrase('unknown')).toBe('by network');
  });

  test('the pattern rule plans nothing without its pattern', () => {
    expect(planGroups(plant().doc, 'pattern')).toEqual([]);
  });

  test('by network: each switch with its devices, each device in its smallest network', () => {
    const { doc, networks, switches, devices } = plant();

    expect(planGroups(doc, 'network')).toEqual([
      {
        title: 'MGMT',
        color: networks.MGMT.color,
        members: [switches.MGMT.id, devices.pwds.id],
      },
      {
        title: 'ot',
        color: networks.ot.color,
        members: ids([
          switches.ot,
          devices['wtg-01'],
          devices['wtg-02'],
          devices['WTG-10'],
        ]),
      },
      {
        title: 'site',
        color: networks.site.color,
        members: ids([switches.site, devices['rtr-site'], devices['ws-01']]),
      },
    ]);

    // A network with no color of its own (as imported) gives its group the
    // palette color its connections are drawn in.
    const uncolored = structuredClone(doc);
    uncolored.networks[2].color = '';

    expect(planGroups(uncolored, 'network')[2].color).toBe(
      DEFAULT_NETWORK_COLORS[2],
    );
  });

  test('by name: devices of a family, switches and one-offs left out', () => {
    const { doc, devices } = plant();
    const planned = planGroups(doc, 'name');

    // The family as the first name writes it; WTG-10 is of it too.
    expect(planned.map((group) => [group.title, group.members])).toEqual([
      ['wtg', ids([devices['wtg-01'], devices['wtg-02'], devices['WTG-10']])],
    ]);
    expect(planned[0].color).toMatch(/^#[0-9a-f]{6}$/);
  });

  test('leaves grouped nodes and existing groups alone, and follows a selection', () => {
    const { doc, switches, devices } = plant();
    const grouped = groupNodes(doc, [devices['wtg-01'].id, switches.ot.id]).doc;

    expect(
      planGroups(grouped, 'network').find((group) => group.title === 'ot'),
    ).toEqual(
      expect.objectContaining({
        members: ids([devices['wtg-02'], devices['WTG-10']]),
      }),
    );

    // Only the selection; the note and one selected node make no group.
    const selection = ids([switches.site, devices['ws-01'], devices.pwds]);

    expect(
      planGroups(doc, 'network', { selection }).map((group) => group.members),
    ).toEqual([ids([switches.site, devices['ws-01']])]);
    expect(
      planGroups(doc, 'name', { selection: [devices['wtg-01'].id] }),
    ).toEqual([]);
  });

  test('makes the groups around their members, named apart from existing ones', () => {
    const { doc, devices } = plant();
    const titled = addNode(doc, { kind: 'group', title: 'ot' }).doc;
    const { doc: next, groups } = applyGroups(
      titled,
      planGroups(titled, 'network'),
    );

    expect(groups.map((group) => group.group.title)).toEqual([
      'MGMT',
      'ot 2',
      'site',
    ]);
    expect(groups[1].group.color).toBe('#b05c17');

    for (const group of groups) {
      const members = next.nodes.filter((node) => node.parentId === group.id);
      const box = findNode(next, group.id);

      expect(members.length).toBeGreaterThan(1);
      for (const member of members) {
        expect(member.position.x).toBeGreaterThanOrEqual(box.position.x);
        expect(member.position.x + sizeOf(member).width).toBeLessThanOrEqual(
          box.position.x + box.size.width,
        );
      }
    }

    expect(findNode(next, devices['wtg-01'].id).parentId).toBe(groups[1].id);
    expect(
      validateDocument(next).filter((entry) => entry.level === 'error'),
    ).toEqual([]);
  });

  test('says why there is nothing to group', () => {
    expect(nothingToGroup('network')).toBe(
      'Nothing to group: no ungrouped nodes share a network.',
    );
    expect(nothingToGroup('name', { selected: true })).toBe(
      'Nothing to group: no ungrouped selected devices have similar names.',
    );
  });
});

describe('Auto-group name patterns', () => {
  test('a pattern is 1 to 200 characters that compile', () => {
    expect(GROUP_PATTERN_MAX).toBe(200);
    expect(patternProblem('')).toBe('Enter a pattern.');
    expect(patternProblem(undefined)).toBe('Enter a pattern.');
    expect(patternProblem('a'.repeat(200))).toBe('');
    expect(patternProblem('a'.repeat(201))).toBe(
      'The pattern can have at most 200 characters.',
    );
    expect(patternProblem('(')).toMatch(
      /^That is not a valid regular expression\. \S/,
    );
    // The engine's own words follow, whatever they are.
    const unclosed = '(';
    let words = '';

    try {
      new RegExp(unclosed, 'i');
    } catch (error) {
      words = error.message;
    }
    expect(words).not.toBe('');
    expect(patternProblem(unclosed)).toBe(
      `That is not a valid regular expression. ${words}`,
    );
    expect(patternProblem('^[a-z]+')).toBe('');
  });

  test('the text of a name is the first pair of parentheses, or the whole match', () => {
    const names = ['web-01', 'WEB-02', 'db-east-1', 'core'];

    // The whole match, as written in each name.
    expect(matchTexts('^[a-z]+', names)).toEqual(['web', 'WEB', 'db', 'core']);
    // The first parenthesized part, when it took part in the match.
    expect(matchTexts('^(\\w+)-(east|west)', names)).toEqual([
      null,
      null,
      'db',
      null,
    ]);
    expect(matchTexts('-(\\d)\\d$', names)).toEqual(['0', '0', null, null]);
    // One that did not take part leaves the whole match.
    expect(matchTexts('(db)?-\\d+$', names)).toEqual([
      '-01',
      '-02',
      'db-east-1'.slice(-2),
      null,
    ]);
    // A match of no text, or of white space alone, is no text.
    expect(matchTexts('^', names)).toEqual([null, null, null, null]);
    expect(matchTexts('\\s+', ['a b', 'ab'])).toEqual([null, null]);
    // The text is trimmed.
    expect(matchTexts('^\\w+\\s', ['rack 1'])).toEqual(['rack']);
    // Case is ignored when matching.
    expect(matchTexts('^web', names)).toEqual(['web', 'WEB', null, null]);
  });

  test('only the first 255 characters of a name are tested', () => {
    const long = `${'a'.repeat(GROUP_NAME_MAX)}-tail`;

    expect(GROUP_NAME_MAX).toBe(255);
    expect(matchTexts('tail$', [long, 'short-tail'])).toEqual([null, 'tail']);
    expect(matchTexts('^a+$', [long])).toEqual(['a'.repeat(255)]);
    // A name that is no string is tested as text.
    expect(matchTexts('^\\d+', [42, null, undefined])).toEqual([
      '42',
      null,
      null,
    ]);
  });

  test('groups devices and switches whose names give the same text', async () => {
    const { doc, switches, devices } = plant();
    // The switches are named after their networks: MGMT, ot and site.
    const plan = await planPatternGroups(doc, '^[a-z]+');

    expect(plan.matched).toBe(9);
    expect(plan.groups.map((group) => group.title)).toEqual(['wtg']);
    // In name order; the title is the text as its first member has it.
    expect(plan.groups[0].members).toEqual(
      ids([devices['wtg-01'], devices['wtg-02'], devices['WTG-10']]),
    );
    expect(DEFAULT_NETWORK_COLORS).toContain(plan.groups[0].color);

    // A switch goes with the devices its name matches like.
    const sites = await planPatternGroups(doc, 'site');

    expect(sites).toEqual({
      groups: [
        {
          title: 'site',
          color: expect.any(String),
          members: ids([devices['rtr-site'], switches.site]),
        },
      ],
      matched: 2,
    });
  });

  test('groups are made in name order, each in the color fewest groups use', async () => {
    const { doc, switches, devices } = plant();
    const plan = await planPatternGroups(doc, 'wtg|site');

    expect(plan).toEqual({
      groups: [
        {
          title: 'site',
          color: DEFAULT_NETWORK_COLORS[0],
          members: ids([devices['rtr-site'], switches.site]),
        },
        {
          title: 'wtg',
          color: DEFAULT_NETWORK_COLORS[1],
          members: ids([
            devices['wtg-01'],
            devices['wtg-02'],
            devices['WTG-10'],
          ]),
        },
      ],
      matched: 5,
    });

    // A group that has the first color already leaves it for last.
    const colored = addNode(doc, {
      kind: 'group',
      title: 'Existing',
      color: DEFAULT_NETWORK_COLORS[0],
    }).doc;

    expect(
      (await planPatternGroups(colored, 'wtg|site')).groups.map(
        (group) => group.color,
      ),
    ).toEqual([DEFAULT_NETWORK_COLORS[1], DEFAULT_NETWORK_COLORS[2]]);

    // Texts that differ only in case are one group; a text one name alone
    // has makes none. "0" is of wtg-01, wtg-02 and ws-01, "1" of WTG-10.
    const tens = await planPatternGroups(doc, '-(\\d)\\d$');

    expect(tens.matched).toBe(4);
    expect(tens.groups).toEqual([
      {
        title: '0',
        color: DEFAULT_NETWORK_COLORS[0],
        members: ids([devices['ws-01'], devices['wtg-01'], devices['wtg-02']]),
      },
    ]);
  });

  test('looks at the selection only, and never at grouped nodes, notes or groups', async () => {
    const { doc, devices } = plant();
    const selection = ids([devices['wtg-01'], devices['wtg-02']]);

    expect(
      (await planPatternGroups(doc, '^wtg', { selection })).groups[0].members,
    ).toEqual(selection);
    expect(
      await planPatternGroups(doc, '^wtg', {
        selection: [devices['wtg-01'].id],
      }),
    ).toEqual({ groups: [], matched: 1 });

    // wtg-01 in a group of its own is no candidate.
    const grouped = groupNodes(doc, [devices['wtg-01'].id]).doc;
    const plan = await planPatternGroups(grouped, '^wtg');

    expect(plan.groups[0].members).toEqual(
      ids([devices['wtg-02'], devices['WTG-10']]),
    );
    // Neither is the note ("hello") or the group ("Group").
    expect(await planPatternGroups(grouped, '^(hello|group)')).toEqual({
      groups: [],
      matched: 0,
    });
  });

  test('a group’s title is cut to 80 characters, and numbered past a group of that name', async () => {
    let doc = createDocument({ name: 'Long' });
    const stem = 'x'.repeat(100);

    for (const hostname of [`${stem}-1`, `${stem}-2`, 'wtg-01', 'wtg-02']) {
      doc = addNode(doc, { kind: 'device', hostname }).doc;
    }

    doc = addNode(doc, { kind: 'group', title: 'wtg' }).doc;

    const plan = await planPatternGroups(doc, '^[a-z]+');

    expect(plan.groups.map((group) => group.title)).toEqual([
      'wtg',
      'x'.repeat(80),
    ]);
    expect(
      applyGroups(doc, plan.groups).groups.map((group) => group.group.title),
    ).toEqual(['wtg 2', 'x'.repeat(80)]);
  });

  test('a pattern that does not compile, or takes too long, makes no plan', async () => {
    const { doc } = plant();

    await expect(planPatternGroups(doc, '(')).rejects.toMatchObject({
      name: 'PatternError',
      code: 'invalid',
      message: expect.stringMatching(/^That is not a valid regular expression/),
    });
    await expect(planPatternGroups(doc, '')).rejects.toMatchObject({
      code: 'invalid',
      message: 'Enter a pattern.',
    });

    // What runs the pattern is given the pattern, the names in name order,
    // and a signal that aborts once the run is over.
    const match = vi.fn((_, names) => names.map(() => null));

    await planPatternGroups(doc, '^wtg', { match });
    expect(match).toHaveBeenCalledExactlyOnceWith(
      '^wtg',
      [
        'MGMT',
        'ot',
        'pwds',
        'rtr-site',
        'site',
        'ws-01',
        'wtg-01',
        'wtg-02',
        'WTG-10',
      ],
      { signal: expect.any(AbortSignal) },
    );
    expect(match.mock.calls[0][2].signal.aborted).toBe(true);

    expect(new PatternError('slow').message).toBe(
      'This pattern takes too long to match. Use a simpler one.',
    );
    expect(new PatternError('worker').message).toBe(
      'The pattern could not be checked. Reload the page to try again.',
    );
  });

  describe('with a clock', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    test('a match that never settles is given up as slow after 2000 ms, and its run ended', async () => {
      const { doc } = plant();
      let signal = null;
      const match = vi.fn((...given) => {
        ({ signal } = given[2]);

        return new Promise(() => {});
      });
      const plan = planPatternGroups(doc, '(a+)+$', { match });
      const failed = expect(plan).rejects.toMatchObject({
        name: 'PatternError',
        code: 'slow',
      });

      expect(GROUP_PATTERN_TIMEOUT_MS).toBe(2000);
      await vi.advanceTimersByTimeAsync(1999);
      expect(signal.aborted).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      await failed;
      // The signal is what ends the worker.
      expect(signal.aborted).toBe(true);
    });

    test('a signal that aborts ends the run at once', async () => {
      const { doc } = plant();
      const closing = new AbortController();
      let signal = null;
      const plan = planPatternGroups(doc, '^wtg', {
        signal: closing.signal,
        match: (...given) => {
          ({ signal } = given[2]);

          return new Promise(() => {});
        },
      });
      const stopped = expect(plan).rejects.toMatchObject({
        name: 'AbortError',
      });

      await vi.advanceTimersByTimeAsync(10);
      closing.abort();
      await stopped;
      expect(signal.aborted).toBe(true);
      expect(vi.getTimerCount()).toBe(0);
    });
  });

  test('says why a pattern has nothing to group', () => {
    expect(nothingToGroupByPattern(0)).toBe(
      'Nothing to group: the pattern matches no ungrouped device or switch.',
    );
    expect(nothingToGroupByPattern(0, { selected: true })).toBe(
      'Nothing to group: the pattern matches no ungrouped selected device or switch.',
    );
    expect(nothingToGroupByPattern(5)).toBe(
      'Nothing to group: 5 names match, but no two with the same text.',
    );
    expect(nothingToGroupByPattern(1, { selected: true })).toBe(
      'Nothing to group: 1 name matches, but no two with the same text.',
    );
  });

  test('the last pattern is kept in this browser, as plain text', () => {
    const storage = memoryStorage();

    expect(GROUP_PATTERN_STORAGE_KEY).toBe('phenix.builder.groupPattern');
    expect(readGroupPattern(storage)).toBe('');
    expect(readGroupPattern(null)).toBe('');

    rememberGroupPattern('^[a-z]+', storage);
    expect(storage.entries.get(GROUP_PATTERN_STORAGE_KEY)).toBe('^[a-z]+');
    expect(readGroupPattern(storage)).toBe('^[a-z]+');

    // A stored value that is no pattern of 1 to 200 characters is ignored.
    for (const bad of ['', 'a'.repeat(201)]) {
      storage.setItem(GROUP_PATTERN_STORAGE_KEY, bad);
      expect(readGroupPattern(storage), bad.length).toBe('');
    }
    expect(readGroupPattern({ getItem: () => ({ pattern: '^a' }) })).toBe('');

    // Blocked storage keeps nothing, and breaks nothing.
    const blocked = {
      getItem() {
        throw new Error('blocked');
      },
      setItem() {
        throw new Error('blocked');
      },
    };

    expect(readGroupPattern(blocked)).toBe('');
    expect(() => rememberGroupPattern('^a', blocked)).not.toThrow();
  });
});

describe('Auto-group in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    store.setDocument(plant().doc);
  });

  // Whether any two of the boxes overlap.
  function overlapping(boxes) {
    return boxes.some((a, i) =>
      boxes
        .slice(i + 1)
        .some(
          (b) =>
            a.position.x < b.position.x + b.size.width &&
            b.position.x < a.position.x + a.size.width &&
            a.position.y < b.position.y + b.size.height &&
            b.position.y < a.position.y + a.size.height,
        ),
    );
  }

  test('groups, then lays out, as one commit that one undo takes back', async () => {
    const before = store.doc;
    const entries = store.history.size;

    const groups = await store.autoGroup('network');

    expect(groups).toHaveLength(3);
    expect(store.history.size).toBe(entries + 1);
    expect(store.announcement).toBe('Created 3 groups by network');

    const boxes = store.doc.nodes.filter((node) => node.kind === 'group');

    expect(boxes).toHaveLength(3);
    expect(overlapping(boxes)).toBe(false);
    // The draft had no layout of its own: it keeps the Settings default
    // that laid it out, and one undo takes it back to Default.
    expect(store.doc.layout).toBe('elk');
    expect(store.currentLayout).toBe('elk');

    store.undo();
    expect(store.doc).toEqual(before);
    expect(store.currentLayout).toBe('');
  });

  test('lays out with the draft’s own layout, and keeps it', async () => {
    store.setDocument({ ...plant().doc, layout: 'cards' });

    await store.autoGroup('network');

    expect(store.doc.layout).toBe('cards');
  });

  test('nothing to group is no edit', async () => {
    await store.autoGroup('network');
    const entries = store.history.size;

    expect(await store.autoGroup('network')).toBeNull();
    expect(store.announcement).toBe(
      'Nothing to group: no ungrouped nodes share a network.',
    );
    expect(store.history.size).toBe(entries);

    // With nodes selected, only they are grouped.
    store.select({ nodes: [store.doc.nodes[0].id], edges: [] });
    expect(await store.autoGroup('name')).toBeNull();
    expect(store.announcement).toBe(
      'Nothing to group: no ungrouped selected devices have similar names.',
    );
  });

  test('a read-only draft is not grouped', async () => {
    const doc = store.doc;

    store.readOnly = true;

    expect(await store.autoGroup('name')).toBeNull();
    expect(store.error).toBe('This draft is read only.');
    expect(store.doc).toBe(doc);
  });

  test('a layout under way turns it away, and it turns a layout away', async () => {
    const running = store.layout();

    expect(await store.autoGroup('network')).toBeNull();
    expect(store.announcement).toBe('Auto layout is still running.');
    await running;

    const grouping = store.autoGroup('name');

    expect(store.autoGrouping).toBe(true);
    expect(await store.layout()).toBeNull();
    expect(store.announcement).toBe('Auto-group is still running.');
    expect(await grouping).toHaveLength(1);
    expect(store.autoGrouping).toBe(false);
  });
  describe('by name pattern', () => {
    let storage;

    beforeEach(() => {
      storage = memoryStorage();
      vi.stubGlobal('localStorage', storage);
    });

    afterEach(() => {
      vi.unstubAllGlobals();
      vi.useRealTimers();
    });

    test('groups, then lays out, as one commit that one undo takes back and redo makes again', async () => {
      const before = store.doc;
      const entries = store.history.size;

      const result = await store.autoGroupByPattern('^(wtg|site|rtr)');

      expect(result.problem).toBe('');
      expect(result.invalid).toBe(false);
      expect(result.groups.map((group) => group.group.title)).toEqual(['wtg']);
      expect(store.history.size).toBe(entries + 1);
      expect(store.announcement).toBe('Created 1 group by name pattern');
      expect(store.autoGrouping).toBe(false);

      const [box] = store.doc.nodes.filter((node) => node.kind === 'group');
      const members = store.doc.nodes.filter(
        (node) => node.parentId === box.id,
      );

      expect(members.map((node) => node.device.hostname).sort()).toEqual([
        'WTG-10',
        'wtg-01',
        'wtg-02',
      ]);
      // The layout that ran is kept, as for the other rules.
      expect(store.doc.layout).toBe('elk');

      store.undo();
      expect(store.doc).toEqual(before);
      expect(store.announcement).toBe('Undid Created 1 group by name pattern.');

      store.redo();
      expect(
        store.doc.nodes.filter((node) => node.kind === 'group'),
      ).toHaveLength(1);
    });

    test('several groups are one commit', async () => {
      const entries = store.history.size;
      const { groups } = await store.autoGroupByPattern('^(wtg)|(site)');

      expect(groups.map((group) => group.group.title)).toEqual(['site', 'wtg']);
      expect(store.history.size).toBe(entries + 1);
      expect(store.announcement).toBe('Created 2 groups by name pattern');
    });

    test('with nodes selected, only they are grouped', async () => {
      const wtg = store.doc.nodes.filter((node) =>
        /^wtg-0/.test(node.device?.hostname || ''),
      );

      store.select({ nodes: wtg.map((node) => node.id), edges: [] });

      const { groups } = await store.autoGroupByPattern('^wtg');

      expect(groups).toHaveLength(1);
      expect(
        store.doc.nodes
          .filter((node) => node.parentId === groups[0].id)
          .map((node) => node.device.hostname)
          .sort(),
      ).toEqual(['wtg-01', 'wtg-02']);
      // The selection stays as it was.
      expect(store.selection.nodes).toEqual(wtg.map((node) => node.id));
    });

    test('what keeps it from grouping is returned, not announced, and nothing changes', async () => {
      const doc = store.doc;
      const entries = store.history.size;
      const said = store.announcementSeq;
      const refused = (problem, invalid) => ({
        groups: null,
        problem,
        invalid,
      });

      expect(await store.autoGroupByPattern('')).toEqual(
        refused('Enter a pattern.', true),
      );
      expect(await store.autoGroupByPattern('a'.repeat(201))).toEqual(
        refused('The pattern can have at most 200 characters.', true),
      );
      expect(await store.autoGroupByPattern('(')).toEqual(
        refused(
          expect.stringMatching(/^That is not a valid regular expression\. /),
          true,
        ),
      );
      expect(await store.autoGroupByPattern('^zzz')).toEqual(
        refused(
          'Nothing to group: the pattern matches no ungrouped device or switch.',
          true,
        ),
      );
      expect(await store.autoGroupByPattern('^(ws|pwds|rtr)')).toEqual(
        refused(
          'Nothing to group: 3 names match, but no two with the same text.',
          true,
        ),
      );

      store.select({ nodes: [store.doc.nodes[0].id], edges: [] });
      expect(await store.autoGroupByPattern('^zzz')).toEqual(
        refused(
          'Nothing to group: the pattern matches no ungrouped selected device or switch.',
          true,
        ),
      );
      store.clearSelection();

      // The worker could not start: that is not about the pattern.
      expect(
        await store.autoGroupByPattern('^wtg', {
          match: () => Promise.reject(new PatternError('worker')),
        }),
      ).toEqual(
        refused(
          'The pattern could not be checked. Reload the page to try again.',
          false,
        ),
      );
      // Neither is a failure of any other kind, which goes to the console.
      const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

      expect(
        await store.autoGroupByPattern('^wtg', {
          match: () => Promise.reject(new Error('boom')),
        }),
      ).toEqual(
        refused(
          'The pattern could not be checked. Reload the page to try again.',
          false,
        ),
      );
      expect(logged).toHaveBeenCalledOnce();
      logged.mockRestore();

      expect(store.doc).toBe(doc);
      expect(store.history.size).toBe(entries);
      expect(store.announcementSeq).toBe(said);
      expect(store.error).toBe('');
    });

    test('a pattern that takes too long is given up after 2 seconds', async () => {
      vi.useFakeTimers();

      const doc = store.doc;
      const running = store.autoGroupByPattern('(a+)+$', {
        match: () => new Promise(() => {}),
      });

      await vi.advanceTimersByTimeAsync(2000);
      expect(await running).toEqual({
        groups: null,
        problem: 'This pattern takes too long to match. Use a simpler one.',
        invalid: true,
      });
      expect(store.doc).toBe(doc);
    });

    test('a read-only draft is not grouped, and a layout under way turns it away', async () => {
      const doc = store.doc;
      const running = store.layout();

      expect(await store.autoGroupByPattern('^wtg')).toEqual({
        groups: null,
        problem: 'Auto layout is still running.',
        invalid: false,
      });
      await running;

      const grouping = store.autoGroup('name');

      expect(await store.autoGroupByPattern('^wtg')).toEqual({
        groups: null,
        problem: 'Auto-group is still running.',
        invalid: false,
      });
      await grouping;
      store.undo();
      store.undo();
      expect(store.doc).toEqual(doc);

      store.readOnly = true;
      expect(await store.autoGroupByPattern('^wtg')).toEqual({
        groups: null,
        problem: 'This draft is read-only.',
        invalid: false,
      });
      expect(store.doc).toEqual(doc);
      // The dialog shows it: the page alert stays out of it.
      expect(store.error).toBe('');
    });

    test('the dialog closing, or the diagram changing, while the pattern runs makes no groups', async () => {
      const closing = new AbortController();
      const entries = store.history.size;
      const stopped = store.autoGroupByPattern('^wtg', {
        signal: closing.signal,
        match: () => new Promise(() => {}),
      });

      closing.abort();
      expect(await stopped).toEqual({
        groups: null,
        problem: '',
        invalid: false,
      });
      expect(store.history.size).toBe(entries);

      // An edit while the pattern ran: the plan was for the diagram before.
      let answer = null;
      const late = store.autoGroupByPattern('^wtg', {
        match: (pattern, names) =>
          new Promise((resolve) => {
            answer = () => resolve(matchTexts(pattern, names));
          }),
      });

      await Promise.resolve();
      store.setInfo({ name: 'Renamed' });
      answer();
      expect(await late).toEqual({ groups: null, problem: '', invalid: false });
      expect(store.announcement).toBe(
        'The diagram changed during Auto-group, so no groups were made.',
      );
      expect(
        store.doc.nodes.filter((node) => node.kind === 'group'),
      ).toHaveLength(0);
    });

    test('a pattern that compiles is kept for the next time, whatever it matches', async () => {
      await store.autoGroupByPattern('(');
      await store.autoGroupByPattern('');
      expect(readGroupPattern(storage)).toBe('');

      await store.autoGroupByPattern('^zzz');
      expect(readGroupPattern(storage)).toBe('^zzz');

      await store.autoGroupByPattern('^wtg');
      expect(readGroupPattern(storage)).toBe('^wtg');
    });

    test('the rule that needs a pattern does nothing without one', async () => {
      const said = store.announcementSeq;

      expect(await store.autoGroup('pattern')).toBeNull();
      expect(store.announcementSeq).toBe(said);
      expect(store.layoutRunning).toBe(false);
    });
  });
});
