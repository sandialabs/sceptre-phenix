// What the command palette lists for what was typed.
//
// With nothing typed: the commands for the selection, the recent commands,
// then every command that can run now, by group. With a query: every
// command of the open view that matches, those that cannot run too (with
// the reason), grouped and ranked by fuzzy.js, and a few nodes (in the
// editor) or drafts (on the landing) whose names match. A prefix searches
// something else: @ the nodes by name, hostname, image, VLAN and addresses,
// # the networks by name or VLAN alias, and ? lists the prefixes. During a
// command that asks for choices (Add device, Connect), the choices of its
// next step.
//
// A result is
//   key       stable and unique in the list
//   kind      'command', 'choice' (of a step, a node or a draft), 'recent'
//             (a command run before, with its choices) or 'prefix' (typing
//             it: `query`)
//   command   the command it runs; choice, picked: the choice and every
//             choice before it, for a step's choices and recent commands
//   title, ranges  its name and the matched parts of it
//   field     {label, value, ranges}: the node field that matched, if not
//             the name
//   detail    a second line; disabled: why it cannot run, or ''
//   icon, keys (the command's shortcuts), next ('step' when it asks for a
//             choice next), hint (the prefix that finds it)
//
// Typing must stay quick in a diagram of thousands of nodes. The palette
// keeps Go to node's choices for the document it was built from
// (createChoiceCache), and a command search matches node names only, making
// choices of just the few it shows.

import { toRaw } from 'vue';

import { count, listOf } from './announce.js';
import {
  GROUPS,
  availability,
  commandKeys,
  commandTitle,
  getCommand,
  nodeChoices,
  paletteCommands,
  viewOf,
} from './commands.js';
import { highlightParts, matchItem } from './fuzzy.js';
import { findNode, nodeLabel } from './model.js';

// How many choices a list shows before asking for a narrower search.
export const CHOICE_LIMIT = 50;
// How many nodes or drafts a command search adds under the commands.
const NAMED_LIMIT = 3;

const GROUP_ICONS = {
  General: 'keyboard',
  Edit: 'copy',
  Selection: 'check',
  Structure: 'layout',
  Add: 'plus',
  'Go to': 'search',
  View: 'image',
  Draft: 'document',
  Drafts: 'document',
};

const ICONS = {
  'help.open': 'help',
  'edit.undo': 'undo',
  'edit.redo': 'redo',
  'edit.paste': 'paste',
  'edit.delete': 'trash',
  'structure.group': 'group',
  'structure.ungroup': 'ungroup',
  'structure.autoGroup.network': 'auto-group',
  'structure.autoGroup.name': 'auto-group',
  'structure.restoreLayout': 'undo',
  'structure.connect': 'link',
  'structure.disconnect': 'close',
  'add.device': 'server',
  'add.switch': 'switch',
  'add.note': 'sticky-note',
  'add.group': 'object-group',
  'goto.network': 'network-wired',
  'goto.drafts': 'arrow-left',
  'view.zoomIn': 'plus',
  'view.focusMode': 'focus',
  'view.theme.system': 'system',
  'view.theme.light': 'sun',
  'view.theme.dark': 'moon',
  'draft.save': 'save',
  'draft.history': 'refresh',
  'draft.export': 'download',
  'draft.upload': 'upload',
  'draft.publish': 'publish',
  'drafts.blank': 'plus',
  'drafts.import': 'layout',
  'drafts.upload': 'upload',
};

// The commands listed for the selection, first, when nothing is typed.
const FOR_SELECTION = [
  'edit.rename',
  'structure.connect',
  'edit.duplicate',
  'edit.copy',
  'structure.group',
  'structure.ungroup',
  'edit.delete',
];

export const NODE_SEARCH =
  'Nodes are found by name, hostname, label, image, network, VLAN, IP address and MAC address.';
const NETWORK_SEARCH = 'Networks are found by name or VLAN alias.';

/**
 * Splits off the prefix. '>' is accepted and ignored, for VS Code habits:
 * the palette starts in commands anyway.
 *
 * @param {string} query as typed
 * @returns {{prefix: ''|'@'|'#'|'?', text: string}}
 */
export function parseQuery(query) {
  const text = String(query || '');
  const first = text[0];

  if (first === '@' || first === '#' || first === '?') {
    return { prefix: first, text: text.slice(1) };
  }

  return { prefix: '', text: first === '>' ? text.slice(1) : text };
}

/**
 * @param {object} command
 * @returns {string} a BuilderIcon name
 */
function commandIcon(command) {
  return ICONS[command.id] || GROUP_ICONS[command.group] || 'keyboard';
}

/**
 * @param {object} command
 * @returns {number} how many choices it asks for
 */
export function stepCount(command) {
  return command.choices ? command.steps?.length || 1 : 0;
}

/**
 * Go to node's choices, kept for one document. The store replaces its
 * document on every edit and never changes one in place, so the document
 * is its own revision: a new one lists the nodes again, and typing does
 * not.
 *
 * @returns {{nodes: function(object): object[]}} nodes(doc), commands.js
 *   nodeChoices
 */
export function createChoiceCache() {
  let doc = null;
  let choices = [];

  return {
    nodes(current) {
      const plain = toRaw(current);

      if (plain !== doc) {
        doc = plain;
        choices = nodeChoices(plain);
      }

      return choices;
    },
  };
}

function commandItem(command, ctx, match = null) {
  const state = availability(command, ctx);

  return {
    key: `command:${command.id}`,
    kind: 'command',
    command,
    choice: undefined,
    picked: [],
    title: commandTitle(command, ctx),
    ranges: match?.ranges || [],
    field: null,
    detail: state === true ? command.detail?.(ctx) || '' : '',
    disabled: state === true ? '' : state,
    icon: commandIcon(command),
    keys: commandKeys(command),
    next: command.choices ? 'step' : '',
    hint: '',
  };
}

function choiceItem(command, choice, picked, match = null) {
  return {
    key: `choice:${command.id}:${[...picked, choice].map((c) => c.id).join('/')}`,
    kind: 'choice',
    command,
    choice,
    picked,
    title: choice.title,
    ranges: match?.ranges || [],
    field: match?.field || null,
    detail: choice.detail || '',
    disabled: choice.disabled || '',
    icon: choice.icon || commandIcon(command),
    keys: [],
    next: picked.length + 1 < stepCount(command) ? 'step' : '',
    hint: '',
  };
}

function prefixItem(query, title, detail) {
  return {
    key: `prefix:${query}`,
    kind: 'prefix',
    query,
    title,
    ranges: [],
    field: null,
    detail,
    disabled: '',
    icon: 'search',
    keys: [],
    next: '',
    hint: '',
  };
}

// The choices that match, best first when something is typed, and how many
// matched in all. `fields: false` matches the names only.
function matchChoices(command, choices, picked, text, limit, fields = true) {
  const matched = [];

  choices.forEach((choice, index) => {
    const match = matchItem(fields ? choice : { title: choice.title }, text);

    if (match) {
      matched.push({ choice, match, index });
    }
  });

  if (text.trim()) {
    matched.sort((a, b) => b.match.score - a.match.score || a.index - b.index);
  }

  return {
    items: matched
      .slice(0, limit)
      .map(({ choice, match }) => choiceItem(command, choice, picked, match)),
    total: matched.length,
  };
}

function result(mode, groups, { total, more = 0, empty = null, noun } = {}) {
  const shown = groups.reduce((sum, group) => sum + group.items.length, 0);

  return {
    mode,
    groups,
    total: total ?? shown,
    more,
    empty,
    noun: noun || 'result',
  };
}

function nothingMatches(text) {
  return `Nothing matches “${text.trim()}”.`;
}

// --- a step's choices ------------------------------------------------------------

function stepResults(ctx, command, picked, text) {
  const step = command.steps?.[picked.length] || 'Choice';
  const { items, total } = matchChoices(
    command,
    command.choices(ctx, picked),
    picked,
    text,
    CHOICE_LIMIT,
  );

  return result(
    'step',
    items.length
      ? [{ id: 'step', label: `Choose a ${step.toLowerCase()}`, items }]
      : [],
    {
      total,
      more: total - items.length,
      empty: items.length
        ? null
        : {
            title: text.trim()
              ? nothingMatches(text)
              : 'There is nothing to choose from.',
            tip: '',
          },
    },
  );
}

// --- @ nodes and # networks --------------------------------------------------------

function prefixResults(ctx, prefix, text, cache) {
  const nodes = prefix === '@';
  const command = getCommand(nodes ? 'goto.node' : 'goto.network');
  const noun = nodes ? 'node' : 'network';
  const state = availability(command, ctx);

  if (state !== true) {
    return result(prefix, [], { empty: { title: state, tip: '' }, noun });
  }

  const { items, total } = matchChoices(
    command,
    nodes && cache ? cache.nodes(ctx.store.doc) : command.choices(ctx, []),
    [],
    text,
    CHOICE_LIMIT,
  );

  return result(
    prefix,
    items.length
      ? [{ id: prefix, label: nodes ? 'Nodes' : 'Networks', items }]
      : [],
    {
      total,
      more: total - items.length,
      noun,
      empty: items.length
        ? null
        : {
            title: nothingMatches(text),
            tip: nodes ? NODE_SEARCH : NETWORK_SEARCH,
          },
    },
  );
}

// --- ? help ------------------------------------------------------------------------

function helpResults(ctx) {
  const editing = viewOf(ctx) === 'editor';
  const search = [
    editing &&
      prefixItem(
        '@',
        '@ Go to a node',
        'Type @ and part of a name, hostname, image, VLAN, IP or MAC address',
      ),
    editing &&
      prefixItem('#', '# Go to a network', 'Type # and a name or VLAN alias'),
    prefixItem(
      '',
      '> Commands',
      'The palette starts in commands, so > can be left out',
    ),
  ].filter(Boolean);
  const help = ['shortcuts.open', 'help.open'].map((id) =>
    commandItem(getCommand(id), ctx),
  );

  return result('?', [
    { id: 'search', label: 'Search', items: search },
    { id: 'help', label: 'Help', items: help },
  ]);
}

// --- nothing typed -----------------------------------------------------------------

function selectionName(store) {
  const { nodes, edges } = store.selection;

  if (nodes.length === 1 && edges.length === 0) {
    return nodeLabel(findNode(store.doc, nodes[0])) || 'node';
  }

  return listOf(
    [
      nodes.length && count(nodes.length, 'node'),
      edges.length && count(edges.length, 'connection'),
    ].filter(Boolean),
  );
}

/**
 * What is selected, in a sentence: what the palette says as it closes when
 * Shift+Enter changed the selection. Each change was said on the palette's
 * own status line, while the live region waited for it to close.
 *
 * @param {object} store
 * @returns {string} "Selected plc-sub-3 and plc-sub-4.", "Selected 5 nodes
 *   and 1 connection." or "Nothing is selected."
 */
export function selectionSummary(store) {
  const { nodes, edges } = store.selection;

  if (nodes.length + edges.length === 0) {
    return 'Nothing is selected.';
  }

  const parts = [
    ...(nodes.length <= 3
      ? nodes.map((id) => nodeLabel(findNode(store.doc, id)) || 'node')
      : [count(nodes.length, 'node')]),
    edges.length && count(edges.length, 'connection'),
  ].filter(Boolean);

  return `Selected ${listOf(parts)}.`;
}

// Connect, for one selected device: its first step is already chosen.
function connectItem(ctx, command) {
  const { nodes, edges } = ctx.store.selection;
  const device =
    nodes.length === 1 &&
    edges.length === 0 &&
    command
      .choices(ctx, [])
      .find((choice) => choice.value === nodes[0] && !choice.disabled);

  return device
    ? {
        ...commandItem(command, ctx),
        key: 'selected:structure.connect',
        title: `Connect ${device.title} to a switch`,
        detail: 'Adds a new interface',
        picked: [device],
      }
    : null;
}

function selectionItems(ctx, listed) {
  const { nodes, edges } = ctx.store.selection;

  if (viewOf(ctx) !== 'editor' || nodes.length + edges.length === 0) {
    return [];
  }

  return FOR_SELECTION.map(getCommand)
    .filter(
      (command) =>
        listed.includes(command) && availability(command, ctx) === true,
    )
    .map((command) =>
      command.id === 'structure.connect'
        ? connectItem(ctx, command)
        : commandItem(command, ctx),
    )
    .filter(Boolean);
}

// A recent command as it can run now, or null when it cannot: it is not a
// command of this view, cannot run, or one of its choices is gone.
function recentItem(entry, ctx, listed, index) {
  const command = getCommand(entry.id);

  if (
    !command ||
    command.prefix ||
    !listed.includes(command) ||
    availability(command, ctx) !== true
  ) {
    return null;
  }

  const picked = [];

  for (const id of entry.choices) {
    const choice = (command.choices?.(ctx, picked) || []).find(
      (option) => option.id === id,
    );

    if (!choice || choice.disabled) {
      return null;
    }

    picked.push(choice);
  }

  if (picked.length !== stepCount(command)) {
    return null;
  }

  const last = picked[picked.length - 1];

  return {
    ...commandItem(command, ctx),
    key: `recent:${index}`,
    kind: 'recent',
    choice: last,
    picked,
    title: [commandTitle(command, ctx), ...picked.map((c) => c.title)].join(
      ' › ',
    ),
    detail: last ? last.detail || '' : command.detail?.(ctx) || '',
    icon: 'clock',
    keys: last ? [] : commandKeys(command),
    next: '',
  };
}

// The view's own group first: the drafts on the landing.
function groupOrder(ctx) {
  return viewOf(ctx) === 'landing'
    ? ['Drafts', ...GROUPS.filter((group) => group !== 'Drafts')]
    : GROUPS;
}

function startResults(ctx, recent) {
  const listed = paletteCommands(ctx);
  const groups = [];
  const selected = selectionItems(ctx, listed);
  const shown = new Set(
    selected.filter((item) => !item.picked.length).map((item) => item.command),
  );

  if (selected.length) {
    groups.push({
      id: 'selected',
      label: `Selected: ${selectionName(ctx.store)}`,
      items: selected,
    });
  }

  const recentItems = recent
    .map((entry, index) => recentItem(entry, ctx, listed, index))
    .filter(Boolean);

  if (recentItems.length) {
    groups.push({ id: 'recent', label: 'Recent', items: recentItems });
  }

  for (const group of groupOrder(ctx)) {
    const items = listed
      .filter(
        (command) =>
          command.group === group &&
          !shown.has(command) &&
          availability(command, ctx) === true,
      )
      .map((command) => commandItem(command, ctx));

    if (items.length) {
      groups.push({ id: group, label: group, items });
    }
  }

  return result('commands', groups);
}

// --- a command search --------------------------------------------------------------

// The nodes whose names match, as the command search lists them. Only the
// names are matched, so only the few nodes shown are made into choices.
function namedNodes(ctx, command, text) {
  const doc = toRaw(ctx.store.doc);
  const { items } = matchChoices(
    command,
    (doc.nodes || []).map((node) => ({ id: node.id, title: nodeLabel(node) })),
    [],
    text,
    NAMED_LIMIT,
    false,
  );
  const choices = new Map(
    nodeChoices(
      doc,
      items.map((item) => item.choice.id),
    ).map((choice) => [choice.id, choice]),
  );

  return items.map((item) =>
    choiceItem(command, choices.get(item.choice.id), [], {
      ranges: item.ranges,
    }),
  );
}

// Up to three nodes (in the editor) or drafts (on the landing) whose names
// match, found the way @ or Open draft finds them.
function namedResults(ctx, text) {
  const editing = viewOf(ctx) === 'editor';
  const command = getCommand(editing ? 'goto.node' : 'drafts.open');

  if (availability(command, ctx) !== true) {
    return null;
  }

  const items = editing
    ? namedNodes(ctx, command, text)
    : matchChoices(
        command,
        command.choices(ctx, []),
        [],
        text,
        NAMED_LIMIT,
        false,
      ).items;

  return items.length
    ? {
        id: 'named',
        label: editing ? 'Go to node' : 'Open draft',
        items: items.map((item) => ({ ...item, hint: editing ? '@' : '' })),
      }
    : null;
}

function searchResults(ctx, text, recent) {
  const listed = paletteCommands(ctx);
  const order = groupOrder(ctx);
  const recentRank = new Map();

  recent.forEach((entry, index) => {
    if (!recentRank.has(entry.id)) {
      recentRank.set(entry.id, index);
    }
  });

  const byGroup = new Map();

  listed
    .map((command, index) => ({
      command,
      index,
      rank: recentRank.get(command.id) ?? recent.length,
      match: matchItem(
        { title: commandTitle(command, ctx), keywords: command.keywords },
        text,
      ),
    }))
    .filter((entry) => entry.match)
    // Ties go to recent use, then to the registry's order.
    .sort(
      (a, b) =>
        b.match.score - a.match.score || a.rank - b.rank || a.index - b.index,
    )
    .forEach((entry) => {
      const group = entry.command.group;

      if (!byGroup.has(group)) {
        byGroup.set(group, { best: entry.match.score, entries: [] });
      }

      byGroup.get(group).entries.push(entry);
    });

  // Groups by their best match.
  const groups = [...byGroup]
    .sort(
      ([a, left], [b, right]) =>
        right.best - left.best || order.indexOf(a) - order.indexOf(b),
    )
    .map(([group, { entries }]) => ({
      id: group,
      label: group,
      items: entries.map(({ command, match }) =>
        commandItem(command, ctx, match),
      ),
    }));

  const named = namedResults(ctx, text);

  if (named) {
    groups.push(named);
  }

  if (groups.length) {
    return result('commands', groups);
  }

  const editing = viewOf(ctx) === 'editor';
  const typed = text.trim();

  return result(
    'commands',
    editing
      ? [
          {
            id: 'instead',
            label: 'Try instead',
            items: [
              prefixItem(
                `@${typed}`,
                `Search nodes for “${typed}”`,
                'By hostname, label, image, VLAN, IP or MAC address',
              ),
              prefixItem(
                `#${typed}`,
                `Search networks for “${typed}”`,
                'By name or VLAN alias',
              ),
            ],
          },
        ]
      : [],
    {
      total: 0,
      empty: {
        title: nothingMatches(text),
        tip: editing
          ? 'Commands and node names were searched.'
          : 'Commands and draft names were searched.',
      },
    },
  );
}

/**
 * A result's second line, in runs for display (highlightParts). When a
 * field matched rather than the name, the line shows it with the match
 * marked: in place, when the line already holds it (a node's image or
 * network), otherwise first and named ("IP address 10.0.1.5 · Device ·
 * …"). A field whose value names itself ("VLAN 101") is not named twice.
 *
 * @param {object} item a result
 * @returns {{text: string, match: boolean}[]}
 */
export function detailParts(item) {
  const { detail = '', field } = item;

  if (!field) {
    return detail ? [{ text: detail, match: false }] : [];
  }

  const at = detail.indexOf(field.value);

  if (at >= 0) {
    return highlightParts(
      detail,
      field.ranges.map(([start, end]) => [start + at, end + at]),
    );
  }

  const named = field.value.toLowerCase().startsWith(field.label.toLowerCase());

  return [
    ...(named ? [] : [{ text: `${field.label} `, match: false }]),
    ...highlightParts(field.value, field.ranges),
    ...(detail ? [{ text: ` · ${detail}`, match: false }] : []),
  ];
}

/**
 * What the palette lists.
 *
 * @param {object} ctx the command context (commands.js)
 * @param {object} [options]
 * @param {string} [options.query] as typed
 * @param {object} [options.command] a command whose choices are being made
 * @param {object[]} [options.picked] the choices made so far
 * @param {{id: string, choices: string[]}[]} [options.recent] recent.js's
 *   list
 * @param {object} [options.cache] createChoiceCache's, kept while the
 *   palette is open, for the @ search
 * @returns {{mode: string, groups: {id: string, label: string, items:
 *   object[]}[], total: number, more: number, empty: ({title: string, tip:
 *   string}|null), noun: string}} `total` counts every match (suggestions
 *   are not matches), `more` those left out of a long list, and `noun`
 *   names them
 */
export function paletteResults(
  ctx,
  { query = '', command = null, picked = [], recent = [], cache = null } = {},
) {
  if (command) {
    return stepResults(ctx, command, picked, query);
  }

  const { prefix, text } = parseQuery(query);

  if (prefix === '@' || prefix === '#') {
    return prefixResults(ctx, prefix, text, cache);
  }

  if (prefix === '?') {
    return helpResults(ctx);
  }

  return text.trim()
    ? searchResults(ctx, text, recent)
    : startResults(ctx, recent);
}
