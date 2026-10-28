// What the command palette lists for the typed text. With no text, it lists
// the commands for the selection, the recent commands and every command that
// can run. With text, it lists the matching commands, nodes or drafts and
// library items, or what a prefix of SEARCHES finds. createChoiceCache keeps
// the Go to node choices, so typing stays quick in a big diagram.

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
import { fieldOrder, highlightParts, matchItem, matchName } from './fuzzy.js';
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
  'settings.open': 'settings',
  'settings.layers': 'layer-group',
  'edit.undo': 'undo',
  'edit.redo': 'redo',
  'edit.paste': 'paste',
  'edit.delete': 'trash',
  'edit.rename': 'pencil',
  'node.edit': 'pencil',
  'structure.group': 'group',
  'structure.ungroup': 'ungroup',
  'structure.autoGroup.network': 'auto-group',
  'structure.autoGroup.name': 'auto-group',
  'structure.autoGroup.pattern': 'auto-group',
  'structure.restoreLayout': 'undo',
  'structure.connect': 'link',
  'structure.setLayer': 'layer-group',
  'selection.layer': 'layer-group',
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
  'draft.history': 'history',
  'draft.download': 'download',
  'draft.download.json': 'download',
  'draft.download.yaml': 'download',
  'draft.download.topology': 'download',
  'draft.download.png': 'image',
  'draft.download.svg': 'image',
  'draft.download.drawio': 'download',
  'draft.download.gexf': 'download',
  'draft.upload': 'upload',
  'draft.publish': 'publish',
  'draft.experiment': 'experiment',
  'drafts.blank': 'plus',
  'drafts.import': 'layout',
  'drafts.upload': 'upload',
  'drafts.open': 'folder-open',
  'draft.duplicate': 'copy',
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
  'Nodes are found by name, hostname, IP address, MAC address, VLAN or network, annotation, label, description and image. Matches show in that order.';
const NETWORK_SEARCH = 'Networks are found by name or VLAN alias.';
export const TEMPLATE_SEARCH =
  'Templates are found by name, description, icon, layer, type, hostname, image, collection and who created or updated them.';
const COLLECTION_SEARCH =
  'Collections are found by name, description, template names and who created or updated them.';
const ICON_SEARCH =
  'Icons are found by name, other names, size, owner and who created or updated them.';

// What each prefix searches: the command whose choices it lists, what the
// list and its items are called, and what finds an item.
const SEARCHES = {
  '@': { command: 'goto.node', label: 'Nodes', noun: 'node', tip: NODE_SEARCH },
  '#': {
    command: 'goto.network',
    label: 'Networks',
    noun: 'network',
    tip: NETWORK_SEARCH,
  },
  't:': {
    command: 'goto.template',
    label: 'Templates',
    noun: 'template',
    tip: TEMPLATE_SEARCH,
  },
  'c:': {
    command: 'goto.collection',
    label: 'Collections',
    noun: 'collection',
    tip: COLLECTION_SEARCH,
  },
  'i:': {
    command: 'goto.icon',
    label: 'Icons',
    noun: 'icon',
    tip: ICON_SEARCH,
  },
};

// The prefixes of the libraries, in the order a command search lists them.
const LIBRARY_PREFIXES = ['t:', 'c:', 'i:'];

/**
 * Splits off the prefix. '>' is accepted and ignored for VS Code habits. A
 * letter prefix (t:, c:, i:) is also found in upper case.
 */
export function parseQuery(query) {
  const text = String(query || '');
  const first = text[0];
  const letter = text.slice(0, 2).toLowerCase();

  if (first === '@' || first === '#' || first === '?') {
    return { prefix: first, text: text.slice(1) };
  }

  if (LIBRARY_PREFIXES.includes(letter)) {
    return { prefix: letter, text: text.slice(2) };
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
 * Go to node's choices, kept for one document. The store never changes a
 * document in place, so the document is its own revision.
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

function commandItem(
  command,
  ctx,
  match = null,
  state = availability(command, ctx),
) {
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
    next: command.choices && !command.direct?.(ctx) ? 'step' : '',
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
    layer: choice.layer || null,
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
// matched in all. `fields: false` matches the names only. A command with
// `nameFirst` lists every name match, also letter by letter, before the
// choices that a field finds.
function matchChoices(command, choices, picked, text, limit, fields = true) {
  const nameFirst = fields && Boolean(command.nameFirst);
  const matched = [];

  choices.forEach((choice, index) => {
    const named = nameFirst && matchName(choice, text);
    const match =
      named ||
      matchItem(fields ? choice : { title: choice.title }, text, {
        name: !nameFirst,
      });

    if (match) {
      matched.push({ choice, match, index, named: Boolean(named) });
    }
  });

  if (text.trim()) {
    matched.sort(
      (a, b) =>
        b.named - a.named ||
        fieldOrder(a.match, b.match) ||
        b.match.score - a.match.score ||
        a.index - b.index,
    );
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

// --- @ nodes, # networks, t: templates, c: collections and i: icons ---------------

function prefixResults(ctx, prefix, text, cache) {
  const { label, noun, tip, ...search } = SEARCHES[prefix];
  const command = getCommand(search.command);
  const state = availability(command, ctx);

  if (state !== true) {
    return result(prefix, [], { empty: { title: state, tip: '' }, noun });
  }

  const { items, total } = matchChoices(
    command,
    prefix === '@' && cache
      ? cache.nodes(ctx.store.doc)
      : command.choices(ctx, []),
    [],
    text,
    CHOICE_LIMIT,
  );

  return result(prefix, items.length ? [{ id: prefix, label, items }] : [], {
    total,
    more: total - items.length,
    noun,
    empty: items.length ? null : { title: nothingMatches(text), tip },
  });
}

// --- ? help ------------------------------------------------------------------------

function helpResults(ctx) {
  const editing = viewOf(ctx) === 'editor';
  const search = [
    editing &&
      prefixItem(
        '@',
        '@ Go to a node',
        'Type @ and part of a name, hostname, IP or MAC address, VLAN, network, annotation, label or description',
      ),
    editing &&
      prefixItem('#', '# Go to a network', 'Type # and a name or VLAN alias'),
    prefixItem(
      't:',
      't: Find a template',
      'Type t: and part of a name, image or collection',
    ),
    prefixItem(
      'c:',
      'c: Find a collection',
      'Type c: and part of a name or template name',
    ),
    prefixItem(
      'i:',
      'i: Find an icon',
      'Type i: and part of a name, other name or owner',
    ),
    prefixItem(
      '',
      '> Commands',
      'The palette starts in commands, so you do not have to type >',
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
 * What is selected, in a sentence, that the palette says as it closes after
 * Shift+Enter changed the selection.
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

// A recent command as it can run now, or null when it cannot. It cannot run
// when it is not a command of this view, when the command cannot run now, or
// when one of its choices is gone.
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

// Up to three templates, collections and icons each, in groups, that the
// words of the text find in their names or their fields. A match of letters
// in order is left out here: t:, c: and i: find those.
function libraryResults(ctx, text) {
  return LIBRARY_PREFIXES.flatMap((prefix) => {
    const { label } = SEARCHES[prefix];
    const command = getCommand(SEARCHES[prefix].command);

    if (availability(command, ctx) !== true) {
      return [];
    }

    const items = command
      .choices(ctx, [])
      .map((choice, index) => ({
        choice,
        index,
        match: matchItem(choice, text),
      }))
      .filter(
        ({ match }) => match && (match.grade !== undefined || match.field),
      )
      .sort((a, b) => b.match.score - a.match.score || a.index - b.index)
      .slice(0, NAMED_LIMIT)
      .map(({ choice, match }) => ({
        ...choiceItem(command, choice, [], match),
        hint: prefix,
      }));

    return items.length
      ? [{ id: `named-${prefix.slice(0, 1)}`, label, items }]
      : [];
  });
}

// Better matches first. Of two matches that differ only in the position of
// their words, the one that can run comes first. Thus Enter runs it, and does
// not say why the other cannot run.
function byMatch(a, b) {
  return (
    (b.match.grade ?? b.match.score) - (a.match.grade ?? a.match.score) ||
    Number(b.state === true) - Number(a.state === true) ||
    b.match.score - a.match.score
  );
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
        {
          title: commandTitle(command, ctx),
          aliases: command.aliases,
          keywords: command.keywords,
        },
        text,
      ),
    }))
    .filter((entry) => entry.match)
    .map((entry) => ({ ...entry, state: availability(entry.command, ctx) }))
    // Ties go to recent use, then to the registry's order.
    .sort((a, b) => byMatch(a, b) || a.rank - b.rank || a.index - b.index)
    .forEach((entry) => {
      const group = entry.command.group;

      if (!byGroup.has(group)) {
        byGroup.set(group, { best: entry, entries: [] });
      }

      byGroup.get(group).entries.push(entry);
    });

  // Groups by their best match.
  const groups = [...byGroup]
    .sort(
      ([a, left], [b, right]) =>
        byMatch(left.best, right.best) || order.indexOf(a) - order.indexOf(b),
    )
    .map(([group, { entries }]) => ({
      id: group,
      label: group,
      items: entries.map(({ command, match, state }) =>
        commandItem(command, ctx, match, state),
      ),
    }));

  const named = namedResults(ctx, text);

  if (named) {
    groups.push(named);
  }

  groups.push(...libraryResults(ctx, text));

  if (groups.length) {
    return result('commands', groups);
  }

  const editing = viewOf(ctx) === 'editor';
  const typed = text.trim();

  return result(
    'commands',
    [
      {
        id: 'instead',
        label: 'Try instead',
        items: [
          editing &&
            prefixItem(
              `@${typed}`,
              `Search nodes for “${typed}”`,
              'By name, hostname, IP or MAC address, VLAN, network, annotation, label or description',
            ),
          editing &&
            prefixItem(
              `#${typed}`,
              `Search networks for “${typed}”`,
              'By name or VLAN alias',
            ),
          prefixItem(
            `t:${typed}`,
            `Search templates for “${typed}”`,
            'By name, image or collection',
          ),
          prefixItem(
            `c:${typed}`,
            `Search collections for “${typed}”`,
            'By name or template name',
          ),
          prefixItem(
            `i:${typed}`,
            `Search icons for “${typed}”`,
            'By name, other name or owner',
          ),
        ].filter(Boolean),
      },
    ],
    {
      total: 0,
      empty: {
        title: nothingMatches(text),
        tip: editing
          ? 'The search included commands, node names, templates, collections and icons.'
          : 'The search included commands, draft names, templates, collections and icons.',
      },
    },
  );
}

// The most characters of a matched field that a second line shows. A longer
// value (a description) shows the part around its first match.
const FIELD_SHOWN = 80;
// How many characters before the first match such a part starts.
const FIELD_LEAD = 20;

// A part of a long value around its first match, with the ranges moved to
// it: {text, ranges}. An ellipsis marks each end that was cut.
function excerpt(value, ranges) {
  if (value.length <= FIELD_SHOWN) {
    return { text: value, ranges };
  }

  const first = ranges[0]?.[0] ?? 0;
  const start = Math.max(
    0,
    Math.min(first - FIELD_LEAD, value.length - FIELD_SHOWN),
  );
  const end = Math.min(value.length, start + FIELD_SHOWN);
  const lead = start > 0 ? '…' : '';
  const shift = lead.length - start;

  return {
    text: `${lead}${value.slice(start, end)}${end < value.length ? '…' : ''}`,
    ranges: ranges
      .filter(([from]) => from >= start && from < end)
      .map(([from, to]) => [from + shift, Math.min(to, end) + shift]),
  };
}

/**
 * A result's second line, in runs for display, with the matched field marked.
 * A long value shows only the part around its match.
 */
export function detailParts(item) {
  const { detail = '', field } = item;

  if (!field) {
    return detail ? [{ text: detail, match: false }] : [];
  }

  const at = field.prefix ? -1 : detail.indexOf(field.value);

  if (at >= 0) {
    return highlightParts(
      detail,
      field.ranges.map(([start, end]) => [start + at, end + at]),
    );
  }

  const named = field.value.toLowerCase().startsWith(field.label.toLowerCase());
  const lead = field.prefix
    ? `${field.prefix} `
    : named
      ? ''
      : `${field.label}: `;
  const shown = excerpt(field.value, field.ranges);

  return [
    ...(lead ? [{ text: lead, match: false }] : []),
    ...highlightParts(shown.text, shown.ranges),
    ...(detail ? [{ text: ` · ${detail}`, match: false }] : []),
  ];
}

/**
 * What the palette lists. `total` counts every match (suggestions are not
 * matches). `more` counts the matches left out of a long list, and `noun`
 * names them.
 */
export function paletteResults(
  ctx,
  { query = '', command = null, picked = [], recent = [], cache = null } = {},
) {
  if (command) {
    return stepResults(ctx, command, picked, query);
  }

  const { prefix, text } = parseQuery(query);

  if (Object.hasOwn(SEARCHES, prefix)) {
    return prefixResults(ctx, prefix, text, cache);
  }

  if (prefix === '?') {
    return helpResults(ctx);
  }

  return text.trim()
    ? searchResults(ctx, text, recent)
    : startResults(ctx, recent);
}
