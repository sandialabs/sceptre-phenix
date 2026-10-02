// Auto-group: puts ungrouped nodes into groups of their own, by network, by
// name, or by a pattern the user types. The store then lays the diagram
// out, so the new groups do not overlap (see autoGroup and
// autoGroupByPattern in store.js).
//
// By network, each network's switch goes with the devices on that network,
// and a device on several networks with its smallest one: the rule the
// network layouts cluster by (see common.js), so the layout draws each new
// group where it drew that cluster. By name, devices whose hostnames share
// a family (web-01, web-02) go together; switches stay out. Community
// detection on the connection graph was tried and left out: a network most
// devices join, such as a management network, pulls unrelated networks into
// one group with no name that fits.
//
// By name pattern, the user's regular expression is run on each name, a
// device's hostname or a switch's label, and the nodes it matches the same
// text of, ignoring case, go together (see groupingPattern.js). The pattern
// runs in a Web Worker that is ended when it takes too long, and only when
// the user asks: it is kept in this browser, never in the document.
//
// Only ungrouped devices and switches are grouped (only the selected ones,
// when a selection is given), existing groups are left alone, and a group
// would need two members.

import { count } from './announce.js';
import { drawnColor } from './colors.js';
import {
  GROUP_PATTERN_MAX,
  matchTexts,
  patternProblem,
} from './groupingPattern.js';
import { uniqueName } from './ids.js';
import { collator, prefixOf } from './layouts/common.js';
import {
  DEFAULT_NETWORK_COLORS,
  addNode,
  boundsOf,
  nodeLabel,
} from './model.js';
import { pageStorage } from './storage.js';

// Each rule: `phrase` ends what its commit says ("Created 2 groups by
// network"), and `asks` marks a rule that needs something from the user
// first (the pattern), so choosing it opens a dialog.
export const GROUPING_STRATEGIES = Object.freeze([
  {
    id: 'network',
    label: 'By network',
    summary: 'Each network’s switch with its devices',
    phrase: 'by network',
  },
  {
    id: 'name',
    label: 'By name',
    summary: 'Devices with names like web-01 and web-02',
    phrase: 'by name',
  },
  {
    id: 'pattern',
    label: 'By name pattern',
    summary: 'Nodes whose names match a regular expression',
    phrase: 'by name pattern',
    asks: true,
  },
]);

const DEFAULT_GROUPING = 'network';

// How long a pattern may take over all the names before its worker is
// ended.
export const GROUP_PATTERN_TIMEOUT_MS = 2000;

// The longest title a group takes from the text a pattern matched.
const GROUP_TITLE_MAX = 80;

// Where the last pattern used is kept, in this browser. It is the user's
// own text, so logout removes it (see session.js).
export const GROUP_PATTERN_STORAGE_KEY = 'phenix.builder.groupPattern';

// Room between a new group's border and its members, as groupNodes leaves.
const GROUP_PADDING = 40;

/**
 * @param {string} id
 * @returns {{id: string, label: string, summary: string, phrase: string,
 *   asks?: boolean}|undefined}
 */
function groupingStrategy(id) {
  return GROUPING_STRATEGIES.find((strategy) => strategy.id === id);
}

/**
 * @param {string} id a GROUPING_STRATEGIES id; an unknown one is the default
 * @returns {string} how the rule grouped, for its commit: "by network"
 */
export function groupingPhrase(id) {
  return (groupingStrategy(id) || groupingStrategy(DEFAULT_GROUPING)).phrase;
}

// The nodes Auto-group may put in a group: devices and switches in no group,
// of the selection when there is one.
function candidatesOf(doc, selection) {
  const selected = new Set(selection || []);

  return (doc.nodes || []).filter(
    (node) =>
      (node.kind === 'device' || node.kind === 'switch') &&
      !node.parentId &&
      (selected.size === 0 || selected.has(node.id)),
  );
}

// Each device's networks, and how many devices each network joins, over the
// whole diagram.
function networksOfDevices(doc) {
  const nodes = new Map((doc.nodes || []).map((node) => [node.id, node]));
  const joined = new Map();
  const size = new Map();

  for (const edge of doc.edges || []) {
    const ends = [nodes.get(edge.sourceNodeId), nodes.get(edge.targetNodeId)];
    const device = ends.find((node) => node?.kind === 'device');
    const network = ends.find((node) => node?.kind === 'switch')?.switch
      ?.networkId;

    if (!device || !network) {
      continue;
    }

    const networks = joined.get(device.id) || new Set();

    if (!networks.has(network)) {
      networks.add(network);
      size.set(network, (size.get(network) || 0) + 1);
    }

    joined.set(device.id, networks);
  }

  return { joined, size };
}

function byNetwork(doc, candidates) {
  const networks = new Map(
    (doc.networks || []).map((network, index) => [
      network.id,
      { ...network, index },
    ]),
  );
  const { joined, size } = networksOfDevices(doc);
  // The smallest network; ties by name, then document order.
  const primaryOf = (ids) =>
    [...ids]
      .filter((id) => networks.has(id))
      .sort(
        (a, b) =>
          size.get(a) - size.get(b) ||
          collator.compare(networks.get(a).name, networks.get(b).name) ||
          networks.get(a).index - networks.get(b).index,
      )[0];
  const members = new Map();

  for (const node of candidates) {
    const network =
      node.kind === 'switch'
        ? node.switch?.networkId
        : primaryOf(joined.get(node.id) || []);

    if (networks.has(network)) {
      members.set(network, [...(members.get(network) || []), node.id]);
    }
  }

  return [...members]
    .sort(([a], [b]) => networks.get(a).index - networks.get(b).index)
    .map(([network, ids]) => {
      const { name, color, index } = networks.get(network);

      // A network with no color is drawn in the palette color of its place
      // (see networkStyle), and its group takes that color too.
      return {
        title: name,
        color:
          drawnColor(color) ||
          DEFAULT_NETWORK_COLORS[index % DEFAULT_NETWORK_COLORS.length],
        members: ids,
      };
    });
}

// A hostname's family as it is written: "web" of "web-01", "IT" of
// "IT-WS-01" (prefixOf, keeping the case).
function familyTitle(hostname) {
  const head = String(hostname || '').split(/[-_.\s]/)[0];

  return head.replace(/\d+$/, '') || head;
}

function byName(doc, candidates) {
  const families = new Map();

  const devices = candidates
    .filter((node) => node.kind === 'device')
    .sort((a, b) =>
      collator.compare(a.device?.hostname || '', b.device?.hostname || ''),
    );

  for (const node of devices) {
    const hostname = node.device?.hostname || '';
    const key = prefixOf(hostname);
    const family = families.get(key) || {
      title: familyTitle(hostname),
      members: [],
    };

    family.members.push(node.id);
    families.set(key, family);
  }

  return inNameOrder(doc, [...families.values()]);
}

// The families of two or more, in name order, each in the palette color
// fewest groups use, earliest first.
function inNameOrder(doc, families) {
  const uses = new Map(DEFAULT_NETWORK_COLORS.map((color) => [color, 0]));

  for (const node of doc.nodes || []) {
    const color = node.kind === 'group' ? node.group?.color : '';

    if (uses.has(color)) {
      uses.set(color, uses.get(color) + 1);
    }
  }

  return families
    .filter((family) => family.members.length > 1)
    .sort((a, b) => collator.compare(a.title, b.title))
    .map((family) => {
      const color = [...uses].sort((a, b) => a[1] - b[1])[0][0];

      uses.set(color, uses.get(color) + 1);

      return { ...family, color };
    });
}

const STRATEGIES = { network: byNetwork, name: byName };

/**
 * The groups Auto-group would make, in the diagram's order of networks or
 * in name order: each {title, color, members}, members being node ids.
 * Groups of one are left out. A rule that asks for something first plans
 * none here (see planPatternGroups).
 *
 * @param {object} doc builder document
 * @param {string} strategy a GROUPING_STRATEGIES id; an unknown one is the
 *   default
 * @param {object} [options] selection: node ids to group; empty or left out
 *   for every ungrouped node
 * @returns {{title: string, color: string, members: string[]}[]}
 */
export function planGroups(doc, strategy, { selection = [] } = {}) {
  if (groupingStrategy(strategy)?.asks) {
    return [];
  }

  const run =
    STRATEGIES[groupingStrategy(strategy) ? strategy : DEFAULT_GROUPING];

  return run(doc, candidatesOf(doc, selection)).filter(
    (group) => group.members.length > 1,
  );
}

/**
 * Makes the planned groups: a group node for each, around its members,
 * named after it (numbered past a group of the same name), and its members
 * put in it. Where the groups go is left to a layout.
 *
 * @param {object} doc
 * @param {{title: string, color: string, members: string[]}[]} planned
 *   from planGroups
 * @returns {{doc: object, groups: object[]}} the document, and the group
 *   nodes made
 */
export function applyGroups(doc, planned) {
  let next = doc;
  const groups = [];
  const titles = (doc.nodes || [])
    .filter((node) => node.kind === 'group')
    .map((node) => node.group?.title || node.label || '');

  for (const plan of planned) {
    const ids = new Set(plan.members);
    const members = next.nodes.filter((node) => ids.has(node.id));
    const bounds = boundsOf(members, GROUP_PADDING);
    const title = uniqueName(plan.title || 'Group', titles, (v) => v, ' ');
    const created = addNode(next, {
      kind: 'group',
      title,
      color: plan.color,
      position: { x: bounds.x, y: bounds.y },
      size: { width: bounds.width, height: bounds.height },
    });

    titles.push(title);
    groups.push(created.node);
    next = {
      ...created.doc,
      nodes: created.doc.nodes.map((node) =>
        ids.has(node.id) ? { ...node, parentId: created.node.id } : node,
      ),
    };
  }

  return { doc: next, groups };
}

/**
 * Why Auto-group has nothing to do, in words.
 *
 * @param {string} strategy
 * @param {object} [options] selected: whether it looked at a selection
 * @returns {string}
 */
export function nothingToGroup(strategy, { selected = false } = {}) {
  const which = selected ? 'selected ' : '';

  return strategy === 'name'
    ? `Nothing to group: no ungrouped ${which}devices have similar names.`
    : `Nothing to group: no ungrouped ${which}nodes share a network.`;
}

// --- by name pattern ---------------------------------------------------------

const PATTERN_ERRORS = {
  slow: 'This pattern takes too long to match. Use a simpler one.',
  worker: 'The pattern could not be checked. Reload the page to try again.',
};

/**
 * Why a pattern made no plan: `code` is 'invalid' (it does not compile),
 * 'slow' (its worker was ended) or 'worker' (the worker could not start).
 * The message says it to the user.
 */
export class PatternError extends Error {
  constructor(code, message = PATTERN_ERRORS[code], options) {
    super(message, options);
    this.name = 'PatternError';
    this.code = code;
  }
}

// The name a pattern is run on: a device's hostname, a switch's label as
// the canvas shows it.
function patternName(node) {
  return node.kind === 'device' ? node.device?.hostname || '' : nodeLabel(node);
}

// Matches in a worker made for this run and ended after it: on its answer,
// on its failure, or when `signal` aborts.
async function matchInWorker(pattern, names, { signal } = {}) {
  let Matcher;

  try {
    ({ default: Matcher } = await import('./groupingWorker.js?worker'));
  } catch (error) {
    throw new PatternError('worker', undefined, { cause: error });
  }

  return new Promise((resolve, reject) => {
    let worker;

    try {
      worker = new Matcher();
    } catch (error) {
      reject(new PatternError('worker', undefined, { cause: error }));

      return;
    }

    const end = () => worker.terminate();

    if (signal?.aborted) {
      end();
    } else {
      signal?.addEventListener('abort', end, { once: true });
    }

    worker.addEventListener('message', ({ data }) => {
      end();

      if (Array.isArray(data?.texts)) {
        resolve(data.texts);
      } else {
        reject(
          new PatternError(
            'invalid',
            `That is not a valid regular expression. ${data?.error || ''}`.trim(),
          ),
        );
      }
    });
    worker.addEventListener('error', (event) => {
      event.preventDefault?.();
      end();
      reject(new PatternError('worker'));
    });
    worker.postMessage({ pattern, names });
  });
}

// Node, where the unit tests run, has no Worker: the pattern runs in-thread
// there. A browser build leaves that path out.
function matchNames(pattern, names, options) {
  return import.meta.env.MODE === 'test'
    ? matchTexts(pattern, names)
    : matchInWorker(pattern, names, options);
}

// Runs `match`, and gives up on it as too slow after the time limit, or as
// stopped (an AbortError) when `signal` aborts. The signal it hands `match`
// aborts once the run is over, however it ends, so a worker never outlives
// its run.
async function matchWithin(match, pattern, names, signal) {
  const run = new AbortController();
  let timer = null;
  let stop = null;
  const limits = new Promise((_, reject) => {
    timer = setTimeout(
      () => reject(new PatternError('slow')),
      GROUP_PATTERN_TIMEOUT_MS,
    );
    stop = () =>
      reject(new DOMException('The matching was stopped.', 'AbortError'));
  });

  if (signal?.aborted) {
    stop();
  } else {
    signal?.addEventListener('abort', stop, { once: true });
  }

  try {
    return await Promise.race([
      Promise.resolve().then(() =>
        match(pattern, names, { signal: run.signal }),
      ),
      limits,
    ]);
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', stop);
    run.abort();
  }
}

/**
 * The groups Auto-group by name pattern would make: the candidates (as for
 * the other rules) whose names the pattern matches the same text of,
 * ignoring case, in name order. A group is named after the text as its
 * first member in name order has it, and groups of one are left out.
 *
 * @param {object} doc builder document
 * @param {string} pattern a regular expression, as typed
 * @param {object} [options] selection: node ids to group, empty or left out
 *   for every ungrouped node; match(pattern, names, {signal}): what runs the
 *   pattern (see matchTexts), a Web Worker by default; signal: an
 *   AbortSignal that ends the matching
 * @returns {Promise<{groups: {title: string, color: string,
 *   members: string[]}[], matched: number}>} the plan, for applyGroups, and
 *   how many candidates the pattern matched; rejects with a PatternError,
 *   or with an AbortError when `signal` aborts
 */
export async function planPatternGroups(
  doc,
  pattern,
  { selection = [], match = matchNames, signal } = {},
) {
  const problem = patternProblem(pattern);

  if (problem) {
    throw new PatternError('invalid', problem);
  }

  const candidates = candidatesOf(doc, selection)
    .map((node) => ({ id: node.id, name: patternName(node) }))
    .sort((a, b) => collator.compare(a.name, b.name));
  const texts = await matchWithin(
    match,
    pattern,
    candidates.map((candidate) => candidate.name),
    signal,
  );
  const families = new Map();
  let matched = 0;

  candidates.forEach((candidate, index) => {
    const text = texts?.[index];

    if (typeof text !== 'string' || !text) {
      return;
    }

    const key = text.toLowerCase();
    const family = families.get(key) || {
      title: text.slice(0, GROUP_TITLE_MAX),
      members: [],
    };

    matched += 1;
    family.members.push(candidate.id);
    families.set(key, family);
  });

  return { groups: inNameOrder(doc, [...families.values()]), matched };
}

/**
 * Why Auto-group by name pattern has nothing to do, in words.
 *
 * @param {number} matched how many candidates the pattern matched
 * @param {object} [options] selected: whether it looked at a selection
 * @returns {string}
 */
export function nothingToGroupByPattern(matched, { selected = false } = {}) {
  return matched > 0
    ? `Nothing to group: ${count(matched, 'name')} ${
        matched === 1 ? 'matches' : 'match'
      }, but no two with the same text.`
    : `Nothing to group: the pattern matches no ungrouped ${
        selected ? 'selected ' : ''
      }device or switch.`;
}

/**
 * The pattern used last in this browser, for the dialog to offer again.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {string} '' when none is kept, or what is kept is no pattern
 */
export function readGroupPattern(storage = pageStorage()) {
  let stored = null;

  try {
    stored = storage?.getItem(GROUP_PATTERN_STORAGE_KEY) ?? null;
  } catch {
    // Blocked storage keeps nothing.
  }

  return typeof stored === 'string' &&
    stored.length >= 1 &&
    stored.length <= GROUP_PATTERN_MAX
    ? stored
    : '';
}

/**
 * Keeps a pattern for the next time the dialog opens.
 *
 * @param {string} pattern one that compiles
 * @param {Storage|null} [storage] localStorage by default
 */
export function rememberGroupPattern(pattern, storage = pageStorage()) {
  try {
    storage?.setItem(GROUP_PATTERN_STORAGE_KEY, pattern);
  } catch {
    // Blocked or full: the dialog then opens empty.
  }
}
