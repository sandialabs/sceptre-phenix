// The VLANs page's model (utils/vlans.js): cards, ID states, counts, the
// filters and the query string, from a cluster with every issue kind.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  FILTERS,
  ID_SPACE,
  STATE,
  cardIds,
  cardMatches,
  cellText,
  conflictIssues,
  conflictSuggestions,
  filterCards,
  idStates,
  idsText,
  issueSummary,
  issueText,
  normalizeVlans,
  rangesOverlapping,
  shownData,
  suggestId,
  validVlan,
  vlanCards,
  vlanCounts,
  vlanInputError,
  vlansCsv,
  vlansQuery,
  vlansQueryOf,
  viewCards,
  worstKind,
  worstSeverity,
} from '@/utils/vlans.js';
import { vlansFixture } from './helpers/vlansFixture.js';

const data = () => normalizeVlans(vlansFixture());
const cardOf = (cards, name) => cards.find((card) => card.name === name);

describe('the cards', () => {
  it('puts the cards with the worst issues first, and the blacklist last', () => {
    expect(vlanCards(data()).map((c) => [c.name, c.severity])).toEqual([
      ['lab', 'conflict'],
      ['old-demo', 'conflict'],
      ['scratch', 'conflict'],
      ['substation-a', 'conflict'],
      ['red-team-2', 'difference'],
      ['ics-training', 'warning'],
      ['Blacklist', null],
    ]);
  });

  it('gives each alias its IDs, issues and one-line messages', () => {
    const cards = vlanCards(data());
    const sub = cardOf(cards, 'substation-a');
    expect(
      sub.aliases.map((a) => [a.alias, a.pinned, a.live, worstKind(a.issues)]),
    ).toEqual([
      ['CORP', 104, 110, 'id-differs'],
      ['FIELD', 103, 103, undefined],
      ['MGMT', 101, 101, undefined],
      ['SCADA', 102, 102, 'duplicate-pin'],
    ]);
    // each card shows only the issues from its own point of view
    expect(sub.aliases[3].messages).toEqual(['Also pinned by scratch (EXP).']);
    expect(sub.aliases[0].messages).toEqual([
      'phēnix pins 104. minimega allocated 110.',
    ]);
    expect(sub.messages).toEqual(['150–199 overlaps ics-training.']);
    expect(sub.range).toEqual([100, 199]);
    expect(sub.vms).toBe(47);
    expect(cardOf(cards, 'scratch').aliases[0].messages).toEqual([
      'Also pinned by substation-a (SCADA).',
    ]);

    const ics = cardOf(cards, 'ics-training');
    expect(ics.messages).toEqual(['150–199 overlaps substation-a.']);
    expect(ics.aliases.find((a) => a.alias === 'LOG').messages).toEqual([
      'Outside the range 150–249.',
    ]);
    expect(ics.issues).toEqual(
      expect.arrayContaining(['range-overlap', 'out-of-range']),
    );

    const red = cardOf(cards, 'red-team-2');
    expect(red.aliases.at(-1)).toMatchObject({
      alias: 'TAP_TEMP',
      pinned: 0,
      live: 330,
      issues: ['minimega-only'],
    });
    expect(cardOf(cards, 'lab').aliases[0].issues).toEqual(['pin-blacklisted']);
  });

  it('makes a card of the namespace no experiment owns', () => {
    const card = cardOf(vlanCards(data()), 'old-demo');
    expect(card).toMatchObject({
      kind: 'unowned',
      severity: 'conflict',
      messages: ['No experiment has the namespace old-demo.'],
      ids: [400, 401, 402, 403],
    });
    expect(card.aliases.map((a) => a.alias)).toEqual([
      'LAN',
      'WAN',
      'DMZ',
      'OOB',
    ]);
  });

  it('shows the blacklist only to a role that sees it', () => {
    const body = vlansFixture();
    delete body.blacklist;
    const cards = vlanCards(normalizeVlans(body));
    expect(cards.some((c) => c.kind === 'blacklist')).toBe(false);
    expect(cardOf(vlanCards(data()), 'Blacklist').ids).toEqual([100, 999]);
  });
});

describe('the ID states', () => {
  it('marks each ID by the most important thing that holds it', () => {
    const states = idStates(data());
    expect(states).toHaveLength(ID_SPACE);
    expect([states[0], states[4095]]).toEqual([STATE.INVALID, STATE.INVALID]);
    expect(states[1]).toBe(STATE.FREE);
    expect(states[105]).toBe(STATE.IN_RANGE);
    expect(states[101]).toBe(STATE.ALLOCATED);
    expect(states[150]).toBe(STATE.PINNED);
    expect(states[999]).toBe(STATE.BLACKLISTED);
    // a blacklisted pin, a duplicate pin and an unowned ID
    expect([states[100], states[102], states[400]]).toEqual([
      STATE.CONFLICT,
      STATE.CONFLICT,
      STATE.CONFLICT,
    ]);
  });

  it('names a cell with its state, owners and ranges', () => {
    const d = data();
    const states = idStates(d);
    expect(cellText(d, states, 102)).toBe(
      'ID 102: Conflict or unowned. EXP of scratch and SCADA of substation-a. ' +
        'In the range of substation-a.',
    );
    expect(cellText(d, states, 160)).toBe(
      'ID 160: Free, in a range. In the ranges of ics-training and substation-a.',
    );
    expect(cellText(d, states, 0)).toBe('ID 0: not a valid VLAN ID.');
  });
});

describe('the counts and issues', () => {
  it('counts the IDs in each state and the free ones', () => {
    expect(vlanCounts(data())).toEqual({
      allocated: 11,
      pinned: 11,
      blacklisted: 2,
      free: 4094 - 18,
      issues: 7,
    });
  });

  it('sums up the issues by severity', () => {
    expect(issueSummary(data().issues)).toMatchObject({
      total: 7,
      conflict: 3,
      difference: 2,
      warning: 2,
      text: '7 issues: 3 conflicts, 2 differences, 2 warnings.',
    });
    expect(issueSummary([]).text).toBe('');
    expect(worstSeverity(['out-of-range', 'id-differs'])).toBe('difference');
    expect(worstSeverity([])).toBe(null);
  });

  it('counts once a problem that each of two experiments reports', () => {
    const issues = data().issues;
    // a duplicate pin and a range overlap come once from each side
    expect(issues).toHaveLength(9);
    const dup = issues.filter((i) => i.kind === 'duplicate-pin');
    expect(dup.map((i) => i.experiments[0])).toEqual([
      'scratch',
      'substation-a',
    ]);
    expect(issueSummary(dup).total).toBe(1);
    // a viewer who sees only one side gets only that side's issue
    expect(issueSummary(dup.slice(0, 1)).total).toBe(1);
    expect(dup.map(issueText)).toEqual([
      'scratch EXP: Also pinned by substation-a (SCADA).',
      'substation-a SCADA: Also pinned by scratch (EXP).',
    ]);
    const unowned = issues.find((i) => i.kind === 'unowned');
    expect(issueText(unowned)).toBe(
      'old-demo: No experiment has the namespace old-demo.',
    );
  });

  it('reads the issues of a 409 conflict only', () => {
    const issues = [{ kind: 'duplicate-pin' }];
    const err = (status, data) => ({ response: { status, data } });
    expect(conflictIssues(err(409, { issues }))).toBe(issues);
    expect(conflictIssues(err(409, { metadata: { issues } }))).toBe(issues);
    expect(conflictIssues(err(409, { message: 'running' }))).toBe(null);
    expect(conflictIssues(err(400, { issues }))).toBe(null);
  });
});

describe('the filters', () => {
  it.each([
    [
      'all',
      [
        'lab',
        'old-demo',
        'scratch',
        'substation-a',
        'red-team-2',
        'ics-training',
        'Blacklist',
      ],
    ],
    [
      'issues',
      [
        'lab',
        'old-demo',
        'scratch',
        'substation-a',
        'red-team-2',
        'ics-training',
      ],
    ],
    ['running', ['substation-a', 'red-team-2']],
    ['stopped', ['lab', 'scratch', 'ics-training']],
    ['unowned', ['old-demo']],
  ])('the %s chip shows %j', (filter, names) => {
    const cards = filterCards(vlanCards(data()), { filter });
    expect(cards.map((c) => c.name)).toEqual(names);
  });

  it.each([
    ['302', ['red-team-2']],
    ['160', ['substation-a', 'ics-training']],
    ['scada', ['substation-a']],
    ['DEMO', ['old-demo']],
    [
      '  ',
      [
        'lab',
        'old-demo',
        'scratch',
        'substation-a',
        'red-team-2',
        'ics-training',
        'Blacklist',
      ],
    ],
  ])('a search for %j finds %j', (search, names) => {
    const cards = filterCards(vlanCards(data()), { search });
    expect(cards.map((c) => c.name)).toEqual(names);
  });

  it('shows only the experiment a link names', () => {
    const cards = filterCards(vlanCards(data()), { experiment: 'scratch' });
    expect(cards.map((c) => c.name)).toEqual(['scratch']);
  });

  it('gives every ID of the shown cards to the ID space view', () => {
    const ids = cardIds(filterCards(vlanCards(data()), { filter: 'unowned' }));
    expect([...ids]).toEqual([400, 401, 402, 403]);
  });

  it('matches every card to the All chip and some chip besides', () => {
    for (const card of vlanCards(data())) {
      // the blacklist card has its own view, so only the All chip matches it
      const chips = FILTERS.filter((f) => f.matches(card)).map((f) => f.key);
      if (card.kind === 'blacklist') expect(chips).toEqual(['all']);
      else expect(chips.length).toBeGreaterThan(1);
      expect(cardMatches(card, card.name.toUpperCase())).toBe(true);
    }
  });
});

describe('the views', () => {
  const names = (query) =>
    viewCards(vlanCards(data()), query).map((c) => c.name);
  const owners = [
    'lab',
    'old-demo',
    'scratch',
    'substation-a',
    'red-team-2',
    'ics-training',
  ];

  it('shows only the blacklist card in the Blacklist view', () => {
    expect(names({ view: 'blacklist' })).toEqual(['Blacklist']);
    expect(
      names({ view: 'blacklist', filter: 'running', search: 'lab' }),
    ).toEqual(['Blacklist']);
  });

  it('leaves the blacklist card out of the By experiment view', () => {
    expect(names({ view: 'cards' })).toEqual(owners);
    expect(names({ view: 'cards', filter: 'running' })).toEqual([
      'substation-a',
      'red-team-2',
    ]);
  });

  it('keeps the blacklisted IDs in the ID space view', () => {
    expect(names({ view: 'grid' })).toEqual([...owners, 'Blacklist']);
    const ids = cardIds(viewCards(vlanCards(data()), { view: 'grid' }));
    expect(ids.has(999)).toBe(true);
  });

  it('shows no card in the Blacklist view to a role that cannot see it', () => {
    const body = vlansFixture();
    delete body.blacklist;
    expect(
      viewCards(vlanCards(normalizeVlans(body)), { view: 'blacklist' }),
    ).toEqual([]);
  });
});

describe('free IDs', () => {
  it('suggests a free ID in the own range, else outside the other ranges', () => {
    const d = data();
    expect(suggestId(d, 'substation-a')).toBe(105);
    expect(suggestId(d, 'scratch')).toBe(1);
    expect(suggestId(d, 'scratch', new Set([1, 2]))).toBe(3);
    const scratch = cardOf(vlanCards(d), 'scratch');
    expect(conflictSuggestions(d, scratch)).toEqual({ EXP: 1 });
  });

  it('keeps a range past 4094 within 1–4094', () => {
    const d = data();
    const sub = d.experiments.find((e) => e.name === 'substation-a');
    Object.assign(sub, { min: 4000, max: 2 ** 31 });
    expect(cardOf(vlanCards(d), 'substation-a').range).toEqual([4000, 4094]);
    expect(Math.max(...cardIds(vlanCards(d)))).toBe(4094);
    expect(idStates(d)).toHaveLength(ID_SPACE);
    for (let id = 4000; id <= 4094; id++) d.blacklist.push(id);
    expect(suggestId(d, 'substation-a')).toBeLessThanOrEqual(4094);
    Object.assign(sub, { min: 5000, max: 6000 });
    expect(cardOf(vlanCards(d), 'substation-a').range).toBe(null);
  });

  it('names the ranges a block overlaps', () => {
    expect(rangesOverlapping(data(), 190, 205).map((e) => e.name)).toEqual([
      'ics-training',
      'substation-a',
    ]);
    expect(rangesOverlapping(data(), 500, 520)).toEqual([]);
  });

  it('writes IDs as runs', () => {
    expect(idsText([403, 400, 401, 402, 999, 0])).toBe('400–403, 999');
  });

  it('writes any IDs as runs that give the same IDs back', () => {
    fc.assert(
      fc.property(fc.array(fc.integer({ min: 1, max: 4094 })), (ids) => {
        const back = idsText(ids)
          .split(', ')
          .filter(Boolean)
          .flatMap((run) => {
            const [lo, hi = lo] = run.split('–').map(Number);
            return Array.from({ length: hi - lo + 1 }, (_, i) => lo + i);
          });
        expect(back).toEqual([...new Set(ids)].sort((a, b) => a - b));
      }),
      { numRuns: 200 },
    );
  });
});

describe('typed IDs', () => {
  it.each([
    ['', false, 'Type an ID.'],
    ['0', false, 'Type an ID from 1 to 4094.'],
    ['0', true, ''],
    ['4094', false, ''],
    ['4095', true, 'Type an ID from 1 to 4094, or 0 for none.'],
    ['1.5', false, 'Type an ID from 1 to 4094.'],
  ])('%j (zero allowed: %s) gives %j', (value, allowZero, error) => {
    expect(vlanInputError(value, { allowZero })).toBe(error);
  });

  it('takes only 1 to 4094 as VLAN IDs', () => {
    fc.assert(
      fc.property(fc.integer({ min: -10, max: 5000 }), (id) => {
        expect(validVlan(id)).toBe(id >= 1 && id <= 4094);
      }),
      { numRuns: 200 },
    );
  });
});

describe('the query string', () => {
  it('reads the defaults from an empty query', () => {
    expect(vlansQuery({})).toEqual({
      view: 'cards',
      filter: 'all',
      search: '',
      experiment: '',
      vlan: null,
    });
    expect(vlansQuery({ view: 'x', filter: 'y', vlan: '5000' })).toMatchObject({
      view: 'cards',
      filter: 'all',
      vlan: null,
    });
  });

  it('opens the Blacklist view from its own key and from the old filter link', () => {
    expect(vlansQuery({ view: 'blacklist' })).toMatchObject({
      view: 'blacklist',
      filter: 'all',
    });
    // the link of the old Blacklist chip
    expect(vlansQuery({ filter: 'blacklist' })).toMatchObject({
      view: 'blacklist',
      filter: 'all',
    });
    expect(vlansQuery({ view: 'grid', filter: 'blacklist' }).view).toBe('grid');
    expect(vlansQueryOf(vlansQuery({ filter: 'blacklist' }))).toEqual({
      view: 'blacklist',
    });
    expect(vlansQueryOf({ view: 'cards', filter: 'all' })).toEqual({});
  });

  it('gives back the state it writes', () => {
    const state = fc.record({
      view: fc.constantFrom('cards', 'grid', 'blacklist'),
      filter: fc.constantFrom(...FILTERS.map((f) => f.key)),
      search: fc.string({ maxLength: 8 }),
      experiment: fc.string({ maxLength: 8 }),
      vlan: fc.option(fc.integer({ min: 1, max: 4094 }), { nil: null }),
    });
    fc.assert(
      fc.property(state, (s) => {
        expect(vlansQuery(vlansQueryOf(s))).toEqual(s);
      }),
      { numRuns: 200 },
    );
  });
});

describe('the export', () => {
  it('gives only what the shown cards show', () => {
    const d = data();
    const shown = filterCards(vlanCards(d), { filter: 'unowned' });
    const out = shownData(d, shown);
    expect(new Set(out.vlans.map((row) => row.namespace))).toEqual(
      new Set(['old-demo']),
    );
    expect(out.experiments).toEqual([]);
    expect(out.issues.map((i) => i.kind)).toEqual(['unowned']);
    expect(out.blacklist).toBeUndefined();
    const blacklist = viewCards(vlanCards(d), { view: 'blacklist' });
    expect(shownData(d, blacklist).blacklist).toEqual(d.blacklist);
  });

  it('writes CSV cells that a spreadsheet does not run as formulas', () => {
    const row = {
      id: 102,
      alias: '=HYPERLINK("x")',
      experiment: '+a',
      namespace: '-b',
      running: true,
      states: ['pinned', 'allocated'],
      sources: { phenix: 102, minimega: null },
      vms: 2,
      hosts: ['@h1', 'h2'],
      issues: [],
    };
    expect(vlansCsv({ vlans: [row] }).split('\n')).toEqual([
      'id,alias,experiment,namespace,running,states,phenix,minimega,vms,hosts,issues',
      `102,"'=HYPERLINK(""x"")",'+a,'-b,true,pinned;allocated,102,,2,'@h1;h2,`,
      '',
    ]);
  });

  it('writes a row with no ID for each alias with no pin and no live ID', () => {
    const experiments = [
      {
        name: 'lab',
        running: false,
        aliases: [
          { alias: 'MGMT', pinned: 0, live: 0, vms: 3 },
          { alias: 'EXP', pinned: 101, live: 0, vms: 1 },
          { alias: 'OT', pinned: 0, live: 205, vms: 2 },
        ],
      },
    ];
    expect(vlansCsv({ vlans: [], experiments }).split('\n')).toEqual([
      'id,alias,experiment,namespace,running,states,phenix,minimega,vms,hosts,issues',
      ',MGMT,lab,,false,,,,3,,',
      '',
    ]);
  });
});
