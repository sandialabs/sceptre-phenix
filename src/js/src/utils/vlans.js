// The VLANs page's model of the cluster's VLAN IDs, built from GET /vlans:
// the cards of the By experiment view, the state of each ID in the ID space
// view, the filters both share and the counts of the header.
import { listText, plural, pluralWord } from '@/utils/plural.js';

export const MIN_VLAN = 1;
export const MAX_VLAN = 4094;
// IDs 0 to 4095, of which 0 and 4095 are not valid
export const ID_SPACE = 4096;
export const GRID_SIZE = 64;

// why a running experiment's pins and range cannot change
export const RUNNING_TIP = 'Stop the experiment to change its VLAN IDs';

// Whether id is a VLAN ID minimega can use.
export const validVlan = (id) =>
  Number.isInteger(id) && id >= MIN_VLAN && id <= MAX_VLAN;

// Why a typed VLAN ID is not valid, or '' when it is.
export function vlanInputError(value, { allowZero = false } = {}) {
  const text = String(value ?? '').trim();
  if (text === '') return 'Type an ID.';
  const id = Number(text);
  if (allowZero && id === 0) return '';
  if (!/^\d+$/.test(text) || !validVlan(id)) {
    return allowZero
      ? 'Type an ID from 1 to 4094, or 0 for none.'
      : 'Type an ID from 1 to 4094.';
  }
  return '';
}

// Each issue kind: its severity and the short tag a row shows.
export const ISSUE_KINDS = {
  'duplicate-pin': { severity: 'conflict', tag: 'Conflict' },
  'pin-blacklisted': { severity: 'conflict', tag: 'Blacklisted' },
  unowned: { severity: 'conflict', tag: 'Unowned' },
  'id-differs': { severity: 'difference', tag: 'Differs' },
  'minimega-only': { severity: 'difference', tag: 'minimega only' },
  'range-overlap': { severity: 'warning', tag: 'Overlap' },
  'out-of-range': { severity: 'warning', tag: 'Out of range' },
};

// most severe first
const SEVERITIES = ['conflict', 'difference', 'warning'];

// the Bulma tag color of each severity
export const SEVERITY_TAG = {
  conflict: 'is-danger',
  difference: 'is-warning',
  warning: 'is-warning is-light',
};

const severityOf = (kind) => ISSUE_KINDS[kind]?.severity ?? 'warning';

const rank = (severity) => {
  const i = SEVERITIES.indexOf(severity);
  return i < 0 ? SEVERITIES.length : i;
};

// The most severe of these issue kinds, or null for none.
export function worstSeverity(kinds) {
  let worst = null;
  for (const kind of kinds ?? []) {
    const severity = severityOf(kind);
    if (worst === null || rank(severity) < rank(worst)) worst = severity;
  }
  return worst;
}

// The most severe issue kind of these, for a row's one tag.
export function worstKind(kinds) {
  return [...(kinds ?? [])].sort(
    (a, b) => rank(severityOf(a)) - rank(severityOf(b)),
  )[0];
}

// The subject of an issue: the server gives each issue from the point of
// view of its first experiment, or of a namespace that no experiment owns.
const subjectOf = (issue) => issue.experiments?.[0] || issue.namespace || '';

// An issue's message with its subject and alias: "scratch EXP: Also pinned
// by substation-a (SCADA)."
export function issueText(issue) {
  const who = [subjectOf(issue), issue.alias].filter(Boolean).join(' ');
  return who ? `${who}: ${issue.message}` : issue.message;
}

// The issues with one for each problem: a conflict or overlap between two
// experiments comes once from each of them, and counts once.
function distinctIssues(issues) {
  const seen = new Set();
  return (issues ?? []).filter((issue) => {
    const key = JSON.stringify([
      issue.kind,
      issue.vlan ?? null,
      issue.range ?? null,
      issue.namespace ?? '',
      [...(issue.experiments ?? [])].sort(),
    ]);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

// The issues counted by severity, each problem once, with the banner's
// sentence: "5 issues: 2 conflicts, 2 differences, 1 warning."
export function issueSummary(all) {
  const issues = distinctIssues(all);
  const counts = { conflict: 0, difference: 0, warning: 0 };
  for (const issue of issues) {
    const severity = issue.severity ?? severityOf(issue.kind);
    counts[severity in counts ? severity : 'warning'] += 1;
  }
  const total = issues.length;
  const parts = SEVERITIES.filter((s) => counts[s]).map((s) =>
    plural(counts[s], s),
  );
  const text = total ? `${plural(total, 'issue')}: ${parts.join(', ')}.` : '';
  return { total, ...counts, text };
}

// The GET /vlans body with every list present; minimega is taken as read
// unless the server says it failed.
export function normalizeVlans(body) {
  const minimega = body?.minimega ?? {};
  return {
    vlans: body?.vlans ?? [],
    experiments: body?.experiments ?? [],
    issues: body?.issues ?? [],
    blacklist: Array.isArray(body?.blacklist) ? body.blacklist : null,
    minimega: {
      ok: minimega.ok !== false,
      read: minimega.read ?? '',
      error: minimega.error ?? '',
      blacklistRemoval: !!minimega.blacklistRemoval,
    },
  };
}

// The IDs of a row: its own, and the other source's when they differ.
const rowIds = (row) =>
  [row.id, row.sources?.phenix, row.sources?.minimega].filter(validVlan);

// Whether a row is held by a minimega namespace no experiment owns.
export const unownedRow = (row) =>
  !row.owned && !!row.namespace && !row.states?.includes('blacklisted');

// The header's counts: IDs allocated, pinned, blacklisted, free and issues.
export function vlanCounts(data) {
  const allocated = new Set();
  const pinned = new Set();
  const used = new Set();
  const blacklisted = new Set(data.blacklist ?? []);
  for (const row of data.vlans) {
    if (!validVlan(row.id)) continue;
    used.add(row.id);
    if (row.states?.includes('allocated')) allocated.add(row.id);
    if (row.states?.includes('pinned')) pinned.add(row.id);
    if (row.states?.includes('blacklisted')) blacklisted.add(row.id);
  }
  for (const id of blacklisted) if (validVlan(id)) used.add(id);
  return {
    allocated: allocated.size,
    pinned: pinned.size,
    blacklisted: blacklisted.size,
    free: MAX_VLAN - MIN_VLAN + 1 - used.size,
    issues: distinctIssues(data.issues).length,
  };
}

// The state of each ID in the ID space view, most important last, so a cell
// shows the highest that applies.
export const STATE = Object.freeze({
  INVALID: 0,
  FREE: 1,
  IN_RANGE: 2,
  PINNED: 3,
  ALLOCATED: 4,
  BLACKLISTED: 5,
  CONFLICT: 6,
});

// Each state's legend text and color; the colors are told apart by the
// legend and the cell names, not by color alone.
export const STATE_INFO = [
  { label: 'Not a valid ID', color: '#141414' },
  { label: 'Free', color: '#5e5e5e' },
  { label: 'Free, in a range', color: '#2d6a8f' },
  { label: 'Pinned only', color: '#d9a400' },
  { label: 'Allocated', color: '#3fae5a' },
  { label: 'Blacklisted', color: '#a77bd6' },
  { label: 'Conflict or unowned', color: '#ff5c4d' },
];

// The outline of the IDs a range selection holds in the ID space view: 7.5:1
// on the grid's ground.
export const SELECTION_COLOR = '#ff9ecf';

// An experiment's range as [min, max] within 1–4094, or null without one.
function rangeOf(exp) {
  if (!exp || !(exp.min > 0) || !(exp.max >= exp.min)) return null;
  const lo = Math.max(exp.min, MIN_VLAN);
  const hi = Math.min(exp.max, MAX_VLAN);
  return lo <= hi ? [lo, hi] : null;
}

// the Bulma tag color of each state that holds an ID; the others are is-light
export const STATE_TAG = {
  [STATE.CONFLICT]: 'is-danger',
  [STATE.ALLOCATED]: 'is-success',
  [STATE.PINNED]: 'is-warning',
  [STATE.BLACKLISTED]: 'is-light',
};

// The state of every ID, 0 to 4095, as a Uint8Array of STATE values.
export function idStates(data) {
  const states = new Uint8Array(ID_SPACE).fill(STATE.FREE);
  states[0] = STATE.INVALID;
  states[ID_SPACE - 1] = STATE.INVALID;
  const raise = (id, state) => {
    if (validVlan(id) && states[id] < state) states[id] = state;
  };
  for (const exp of data.experiments) {
    const range = rangeOf(exp);
    if (!range) continue;
    for (let id = range[0]; id <= range[1]; id++) {
      raise(id, STATE.IN_RANGE);
    }
  }
  for (const id of data.blacklist ?? []) raise(id, STATE.BLACKLISTED);
  for (const row of data.vlans) {
    const conflict =
      unownedRow(row) ||
      (row.issues ?? []).some((kind) => severityOf(kind) === 'conflict');
    const states_ = row.states ?? [];
    let state = STATE.PINNED;
    if (states_.includes('allocated')) state = STATE.ALLOCATED;
    if (states_.includes('blacklisted')) state = STATE.BLACKLISTED;
    if (conflict) state = STATE.CONFLICT;
    raise(row.id, state);
  }
  return states;
}

// "100–199"
export const rangeText = (range) =>
  range[0] === range[1] ? `${range[0]}` : `${range[0]}–${range[1]}`;

// IDs as short text, runs joined: "400–403, 999".
export function idsText(ids) {
  const sorted = [...new Set(ids)].filter(validVlan).sort((a, b) => a - b);
  const runs = [];
  for (const id of sorted) {
    const last = runs.at(-1);
    if (last && id === last[1] + 1) last[1] = id;
    else runs.push([id, id]);
  }
  return runs.map(rangeText).join(', ');
}

// the issues of an experiment, or of a namespace no experiment owns
const issuesOfExperiment = (issues, name) =>
  issues.filter((issue) => issue.experiments?.[0] === name);
const issuesOfNamespace = (issues, namespace) =>
  issues.filter(
    (issue) => !issue.experiments?.length && issue.namespace === namespace,
  );

// the one-line messages of these issues, each once
const messages = (issues) => [
  ...new Set(issues.map((issue) => issue.message).filter(Boolean)),
];

// An experiment's card: its aliases with the IDs of each source and the
// issues of each, its range, and the messages of its issues.
function experimentCard(data, exp) {
  const rows = data.vlans.filter((row) => row.experiment === exp.name);
  const issues = issuesOfExperiment(data.issues, exp.name);
  // an alias's issues name it, or one of its IDs; a range overlap is the card's
  const aboutAlias = (issue, a) =>
    issue.kind !== 'range-overlap' &&
    (issue.alias === a.alias ||
      (validVlan(issue.vlan) && [a.pinned, a.live].includes(issue.vlan)));
  const aliases = (exp.aliases ?? []).map((a) => {
    const kinds = new Set();
    for (const row of rows) {
      if (row.alias === a.alias) for (const k of row.issues ?? []) kinds.add(k);
    }
    const own = issues.filter((issue) => aboutAlias(issue, a));
    for (const issue of own) kinds.add(issue.kind);
    return {
      alias: a.alias,
      pinned: a.pinned || 0,
      live: a.live || 0,
      vms: a.vms ?? 0,
      issues: [...kinds],
      messages: messages(own),
    };
  });
  const cardIssues = issues.filter(
    (issue) => !(exp.aliases ?? []).some((a) => aboutAlias(issue, a)),
  );
  const kinds = [
    ...issues.map((issue) => issue.kind),
    ...aliases.flatMap((a) => a.issues),
  ];
  return {
    key: `experiment:${exp.name}`,
    kind: 'experiment',
    name: exp.name,
    running: !!exp.running,
    range: rangeOf(exp),
    aliases,
    vms: aliases.reduce((sum, a) => sum + a.vms, 0),
    issues: [...new Set(kinds)],
    messages: messages(cardIssues),
    severity: worstSeverity(kinds),
    ids: [
      ...new Set(aliases.flatMap((a) => [a.pinned, a.live]).filter(validVlan)),
    ],
  };
}

// A card for each minimega namespace that holds IDs but has no experiment.
function unownedCards(data) {
  const byNamespace = new Map();
  for (const row of data.vlans.filter(unownedRow)) {
    if (!byNamespace.has(row.namespace)) byNamespace.set(row.namespace, []);
    byNamespace.get(row.namespace).push(row);
  }
  return [...byNamespace].map(([namespace, rows]) => {
    const issues = issuesOfNamespace(data.issues, namespace);
    return {
      key: `namespace:${namespace}`,
      kind: 'unowned',
      name: namespace,
      running: true,
      range: null,
      aliases: rows
        .map((row) => ({
          alias: row.alias,
          pinned: 0,
          live: row.id,
          vms: row.vms ?? 0,
          issues: row.issues?.length ? row.issues : ['unowned'],
          messages: [],
        }))
        .sort((a, b) => a.live - b.live),
      vms: rows.reduce((sum, row) => sum + (row.vms ?? 0), 0),
      issues: ['unowned'],
      messages: messages(issues).length
        ? messages(issues)
        : [`No experiment has the namespace ${namespace}.`],
      severity: 'conflict',
      ids: [...new Set(rows.flatMap(rowIds))],
    };
  });
}

// The cards of the page: each experiment, each unowned namespace, then the
// blacklist when the role may see it. The Blacklist view shows the last. Cards with the
// worst issues come first, then by name.
export function vlanCards(data) {
  const cards = [
    ...data.experiments.map((exp) => experimentCard(data, exp)),
    ...unownedCards(data),
  ].sort(
    (a, b) =>
      rank(a.severity) - rank(b.severity) || a.name.localeCompare(b.name),
  );
  if (data.blacklist) {
    // every ID minimega lists, so the card agrees with the header count
    const ids = [...data.blacklist].sort((a, b) => a - b);
    cards.push({
      key: 'blacklist',
      kind: 'blacklist',
      name: 'Blacklist',
      running: false,
      range: null,
      aliases: [],
      vms: 0,
      issues: [],
      messages: [],
      severity: null,
      ids,
    });
  }
  return cards;
}

// The filter chips, in order, and whether a card matches each.
export const FILTERS = [
  { key: 'all', label: 'All', matches: () => true },
  { key: 'issues', label: 'Issues', matches: (card) => !!card.severity },
  {
    key: 'running',
    label: 'Running',
    matches: (card) => card.kind === 'experiment' && card.running,
  },
  {
    key: 'stopped',
    label: 'Stopped',
    matches: (card) => card.kind === 'experiment' && !card.running,
  },
  {
    key: 'unowned',
    label: 'Unowned',
    matches: (card) => card.kind === 'unowned',
  },
];

const filterKey = (value) =>
  FILTERS.some((f) => f.key === value) ? value : 'all';

// Whether a card matches a search: an ID it holds, or part of its name or
// of an alias, in any case.
export function cardMatches(card, search) {
  const text = String(search ?? '')
    .trim()
    .toLowerCase();
  if (!text) return true;
  if (/^\d+$/.test(text)) {
    const id = Number(text);
    return (
      card.ids.includes(id) ||
      (!!card.range && id >= card.range[0] && id <= card.range[1])
    );
  }
  return (
    card.name.toLowerCase().includes(text) ||
    card.aliases.some((a) => a.alias.toLowerCase().includes(text))
  );
}

// The cards a view shows: the blacklist card in the Blacklist view, else the
// cards that the filter chip, the experiment of a link and the search keep. The
// By experiment view leaves out the blacklist card.
export function viewCards(cards, query) {
  if (query.view === 'blacklist')
    return cards.filter((card) => card.kind === 'blacklist');
  const kept = filterCards(cards, query);
  return query.view === 'cards'
    ? kept.filter((card) => card.kind !== 'blacklist')
    : kept;
}

// The cards the filter chip, the experiment of a link and the search show.
export function filterCards(cards, { filter = 'all', experiment, search }) {
  const chip = FILTERS.find((f) => f.key === filter) ?? FILTERS[0];
  return cards.filter(
    (card) =>
      chip.matches(card) &&
      (!experiment ||
        (card.kind === 'experiment' && card.name === experiment)) &&
      cardMatches(card, search),
  );
}

// The IDs the shown cards hold or range over, for the ID space view to show
// the other IDs dimmed.
export function cardIds(cards) {
  const ids = new Set();
  for (const card of cards) {
    for (const id of card.ids) ids.add(id);
    if (card.range) {
      for (let id = card.range[0]; id <= card.range[1]; id++) ids.add(id);
    }
  }
  return ids;
}

// The card of a row: its experiment's, its unowned namespace's or the
// blacklist's.
function cardKeyOf(row) {
  if (row.owned) return `experiment:${row.experiment}`;
  if (unownedRow(row)) return `namespace:${row.namespace}`;
  return 'blacklist';
}

// What the shown cards show, as the GET /vlans body: their rows, experiments
// and issues, for an export that matches the page.
export function shownData(data, cards) {
  const keys = new Set(cards.map((card) => card.key));
  const shown = (key) => keys.has(key);
  const issueKey = (issue) =>
    issue.experiments?.length
      ? `experiment:${issue.experiments[0]}`
      : `namespace:${issue.namespace}`;
  return {
    vlans: data.vlans.filter((row) => shown(cardKeyOf(row))),
    experiments: data.experiments.filter((exp) =>
      shown(`experiment:${exp.name}`),
    ),
    issues: data.issues.filter((issue) => shown(issueKey(issue))),
    ...(data.blacklist && shown('blacklist') && { blacklist: data.blacklist }),
    minimega: data.minimega,
  };
}

// the columns of GET /vlans?format=csv
const CSV_COLUMNS = [
  'id',
  'alias',
  'experiment',
  'namespace',
  'running',
  'states',
  'phenix',
  'minimega',
  'vms',
  'hosts',
  'issues',
];

// A CSV cell: quoted when it must be, and with an apostrophe first when a
// spreadsheet would read it as a formula.
function csvCell(value) {
  let text = String(value ?? '');
  if (/^[=+\-@\t\r]/.test(text)) text = `'${text}`;
  return /[",\r\n]|^\s/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

// The rows as GET /vlans?format=csv writes them, then a row with no ID for
// each experiment alias that has no pin and no live ID.
export function vlansCsv({ vlans: rows, experiments = [] }) {
  // a row with no ID has only these fields
  const unset = experiments.flatMap((exp) =>
    (exp.aliases ?? [])
      .filter((a) => !a.pinned && !a.live)
      .map((a) => ({
        alias: a.alias,
        experiment: exp.name,
        running: exp.running,
        vms: a.vms,
      })),
  );
  const lines = [...rows, ...unset].map((row) =>
    [
      row.id,
      row.alias,
      row.experiment,
      row.namespace,
      !!row.running,
      (row.states ?? []).join(';'),
      row.sources?.phenix,
      row.sources?.minimega,
      row.vms ?? 0,
      (row.hosts ?? []).join(';'),
      (row.issues ?? []).join(';'),
    ]
      .map(csvCell)
      .join(','),
  );
  return [CSV_COLUMNS.join(','), ...lines].map((line) => `${line}\n`).join('');
}

// The rows of one ID.
export const rowsOf = (data, id) => data.vlans.filter((row) => row.id === id);

// The experiments whose range holds id.
export const rangesHolding = (data, id) =>
  data.experiments.filter((exp) => {
    const range = rangeOf(exp);
    return range && id >= range[0] && id <= range[1];
  });

// The IDs that any row or the blacklist uses.
function usedIds(data) {
  const used = new Set(data.blacklist ?? []);
  for (const row of data.vlans) for (const id of rowIds(row)) used.add(id);
  return used;
}

// A free ID to suggest for an alias of the experiment `name`: the first one
// in its own range, else the first outside every other range, else the
// first free one; 0 when there is none. IDs in taken are skipped too.
export function suggestId(data, name, taken = new Set()) {
  const used = usedIds(data);
  for (const id of taken) used.add(id);
  const own = rangeOf(data.experiments.find((exp) => exp.name === name));
  const others = data.experiments
    .filter((exp) => exp.name !== name)
    .map(rangeOf)
    .filter(Boolean);
  const free = (id) => !used.has(id);
  const outside = (id) => others.every(([lo, hi]) => id < lo || id > hi);
  if (own) {
    for (let id = own[0]; id <= own[1]; id++) if (free(id)) return id;
  }
  for (let id = MIN_VLAN; id <= MAX_VLAN; id++) {
    if (free(id) && outside(id)) return id;
  }
  for (let id = MIN_VLAN; id <= MAX_VLAN; id++) if (free(id)) return id;
  return 0;
}

// The experiments whose range overlaps the block [start, end].
export const rangesOverlapping = (data, start, end) =>
  data.experiments.filter((exp) => {
    const range = rangeOf(exp);
    return range && range[0] <= end && range[1] >= start;
  });

// What a grid cell is, in words: "ID 102: Conflict or unowned. SCADA of
// substation-a, EXP of scratch."
export function cellText(data, states, id) {
  if (!validVlan(id)) return `ID ${id}: not a valid VLAN ID.`;
  const owners = rowsOf(data, id).map((row) => {
    if (row.states?.includes('blacklisted') && !row.namespace) {
      return 'the minimega blacklist';
    }
    const owner = row.experiment || row.namespace;
    return row.alias ? `${row.alias} of ${owner}` : owner;
  });
  if (!owners.length && (data.blacklist ?? []).includes(id)) {
    owners.push('the minimega blacklist');
  }
  const ranges = rangesHolding(data, id).map((exp) => exp.name);
  const parts = [`ID ${id}: ${STATE_INFO[states[id]].label}.`];
  if (owners.length) parts.push(`${listText([...new Set(owners)])}.`);
  if (ranges.length) {
    parts.push(
      `In the ${pluralWord(ranges.length, 'range')} of ${listText(ranges)}.`,
    );
  }
  return parts.join(' ');
}

// The views of the page: By experiment, ID space and Blacklist.
const VIEWS = ['cards', 'grid', 'blacklist'];

// A range of IDs from the query string, "100-163", as [100, 163], or null
// when it is not two valid IDs.
function rangeQuery(value) {
  const match = /^(\d+)-(\d+)$/.exec(typeof value === 'string' ? value : '');
  if (!match) return null;
  const ids = [Number(match[1]), Number(match[2])];
  if (!ids.every(validVlan)) return null;
  return ids.sort((a, b) => a - b);
}

// The page's query string keys, read into the page's state.
export function vlansQuery(query) {
  const id = Number(query?.vlan);
  // filter=blacklist was the Blacklist view before it had its own button
  const view = VIEWS.includes(query?.view)
    ? query.view
    : query?.filter === 'blacklist'
      ? 'blacklist'
      : 'cards';
  return {
    view,
    filter: filterKey(query?.filter),
    search: typeof query?.q === 'string' ? query.q : '',
    experiment: typeof query?.experiment === 'string' ? query.experiment : '',
    vlan: validVlan(id) ? id : null,
    range: rangeQuery(query?.vlans),
  };
}

// The query string for the page's state; defaults are left out.
export function vlansQueryOf({
  view,
  filter,
  search,
  experiment,
  vlan,
  range,
}) {
  return {
    ...(VIEWS.includes(view) && view !== 'cards' && { view }),
    ...(filter && filter !== 'all' && { filter }),
    ...(search && { q: search }),
    ...(experiment && { experiment }),
    ...(validVlan(vlan) && { vlan: String(vlan) }),
    ...(range && { vlans: `${range[0]}-${range[1]}` }),
  };
}

// The issues with a 409 from a pin or range change, or null for another error.
export function conflictIssues(err) {
  if (err?.response?.status !== 409) return null;
  const data = err.response.data;
  const issues = data?.issues ?? data?.metadata?.issues;
  return Array.isArray(issues) && issues.length ? issues : null;
}

// A free ID for each alias of an experiment card that has a conflict, by
// alias, each a different ID.
export function conflictSuggestions(data, card) {
  const taken = new Set();
  const out = {};
  for (const a of card.aliases) {
    if (worstSeverity(a.issues) !== 'conflict') continue;
    const id = suggestId(data, card.name, taken);
    if (!id) break;
    taken.add(id);
    out[a.alias] = id;
  }
  return out;
}
