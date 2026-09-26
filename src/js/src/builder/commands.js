// The Builder's commands: one registry of everything it can do by name or
// by key, in the editor and on the drafts landing. The key dispatcher below,
// the command palette, the shortcut sheet, the toolbar's tooltips and
// aria-keyshortcuts, the canvas's Keyboard help and the outline's hint all
// read it, so none of them can promise a key another does not handle.
//
// A command is
//   id        stable, 'area.action'; customized keys are stored under it
//   title     its name in the palette and the shortcut sheet; one that ends
//             in '…' opens a dialog
//   group     its heading in the palette and the sheet (GROUPS is the order)
//   keywords  more words the palette matches, without highlighting them
//   keys      default key specs (keymap.js), a list or {mac: [], other: []}
//   scope     where its keys work (SCOPES), one or a list
//   page      its keys also work on the rest of the Builder's page (the app
//             header), outside text fields and the app's dialogs
//   views     where it exists: ['editor'] when left out, ['landing'], or both
//   local     the focused control handles its keys itself (canvas items,
//             outline rows): listed for reference, never dispatched
//   fixed     its keys cannot be customized (local ones never can)
//   palette   false keeps it out of the command palette
//   when(ctx) true, or the reason it cannot run now, in words
//   run(ctx, choice, picked)
//   choices(ctx, picked)  for a command that asks for more first: the
//             options of the next step, as choices (below); `steps` names
//             each step, and run gets the last choice and every choice in
//             order
//   prefix    the palette query that searches what the command goes to
//   label(ctx)  the title as things are now ('Hide minimap')
//   detail(ctx) a second line for the palette
//   phrase    what its keys do, for the Keyboard help ('copies')
//
// A choice is {id, title, detail?, keywords?, icon?, disabled?, value?}:
// `disabled` is the reason it cannot be chosen, and `value` what run needs.
//
// The context (createCommandContext) is {store, view}: the Builder store and
// the adapter BuilderBeta.vue implements (VIEW_API). A run may add `source`
// ('key' from the dispatcher, 'palette' from the palette) and `additive`
// (Shift+Enter on a Go to node choice); a key press also adds `event`,
// `focus` (focusScope) and `item` (the focused node, connection or row).

import { nextTick, toRaw } from 'vue';

import { count, listOf } from './announce.js';
import { DEVICE_TEMPLATES, PALETTE, kindMeta, nodeIconKey } from './catalog.js';
import { GROUPING_STRATEGIES } from './grouping.js';
import { HELP_URL } from './help.js';
import { LAYOUT_ALGORITHMS, layoutAlgorithm } from './layouts/index.js';
import {
  ariaKey,
  currentPlatform,
  isCharacterKey,
  keyLabel,
  keyText,
  keymapState,
  matchesKey,
  sameKey,
  shortcutOverride,
  typesCharacter,
} from './keymap.js';
import {
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  networkRefusal,
  nodeLabel,
  specInterfaces,
} from './model.js';
import { connectionList } from './outline.js';
import { pressSelection } from './selection.js';
import { addInView, paletteNode } from '@/components/builder/paletteDnd.js';

export const GROUPS = [
  'General',
  'Edit',
  'Selection',
  'Structure',
  'Add',
  'Go to',
  'View',
  'Draft',
  'Drafts',
];

// Where a scope's keys work, by focusScope's answer: 'fields' everywhere in
// the view, text fields included (for keys that mean nothing to a field);
// 'editor' everywhere but text fields; 'canvas' on the canvas itself, a node
// or a connection; 'outline' on an outline row.
export const SCOPES = {
  fields: ['field', 'editor', 'canvas', 'outline', 'landing'],
  editor: ['editor', 'canvas', 'outline', 'landing'],
  canvas: ['canvas'],
  outline: ['outline'],
};

// What the view adapter provides, for reference: BuilderBeta.vue implements
// it, and tests stub it.
export const VIEW_API = [
  'editing', // boolean: the editor is open (false: the drafts landing)
  'dialog', // string: the open dialog, '' for none
  'draftsTab', // string: the landing's shown tab: mine, shared or published
  'showMinimap', // boolean
  'canZoomIn', // boolean
  'canZoomOut', // boolean
  'focusMode', // boolean: focus mode is on (see focusMode.js)
  'openDialog', // (name) publish, export, import, scenario
  'openPalette', // ({query, command}) the command palette: dialog 'commands'
  'openShortcuts', // () the shortcut sheet: dialog 'shortcuts'
  'openSettings', // () the Builder settings: dialog 'settings'
  'openHistory', // () loads the history, then opens its dialog
  'openLanding', // (name) the landing's import (Upload) or generate (Import)
  'startBlank', // () a new blank draft
  'openDraft', // (item) a listed draft or published diagram
  'closeEditor', // () back to the drafts
  'showDraftsTab', // (id) mine, shared or published
  'toggleMinimap', // ()
  'toggleFocusMode', // () turns focus mode on or off, and says which
  // () the view as a new session shows it: default column widths, the
  // minimap, the starting zoom, panels scrolled to the top, sections closed
  'resetView',
  'setTheme', // (theme) system, light or dark
  'zoomIn', // ()
  'zoomOut', // ()
  'fitView', // ()
  'focusCanvas', // ()
  'focusOutline', // ()
  'focusInspector', // ({field}) its first field when field is true
  'showNode', // (id) focuses a node on the canvas and pans it into view
  // () the part of the canvas in view, in flow coordinates: {x, y, width,
  // height}, or null
  'visibleArea',
  'revealNode', // (id or ids) brings nodes into view, leaving focus as it is
  // () before a save: commits the focused text field, as its change event
  // would, and settles the Inspector's unapplied edits; resolves to why
  // some stay unapplied ('1 field needs attention'), or ''
  'settleEdits',
];

export const READ_ONLY = 'This draft is read-only.';

/**
 * @param {object} options
 * @param {object} options.store the Builder store
 * @param {object} options.view the view adapter (VIEW_API)
 * @returns {{store: object, view: object}}
 */
export function createCommandContext({ store, view }) {
  return { store, view };
}

// --- conditions --------------------------------------------------------------

function editable({ store }) {
  return store.readOnly ? READ_ONLY : true;
}

// Blank, Import and Upload make a draft, which a role without the configs
// create permission cannot (the store's canCreateDrafts).
function creatable({ store }) {
  return store.canCreateDrafts === false
    ? 'Your role cannot create drafts.'
    : true;
}

function publishable(ctx) {
  const reason = editable(ctx);

  if (reason !== true) {
    return reason;
  }

  return ctx.store.canPublish === false
    ? 'Your role cannot publish diagrams.'
    : true;
}

function hasSelection(store) {
  return store.selection.nodes.length > 0 || store.selection.edges.length > 0;
}

// Ungroup acts on the first selected node, which must be a group.
function selectedGroup(store) {
  const id = store.selection.nodes[0];

  return (store.doc.nodes || []).find(
    (node) => node.id === id && node.kind === 'group',
  );
}

function devices(doc) {
  return (doc.nodes || []).filter((node) => node.kind === 'device');
}

function switches(doc) {
  return (doc.nodes || []).filter((node) => node.kind === 'switch');
}

// What a rename is for: the focused node or connection, else the one node
// or connection selected. {kind, id, name, node}, or null.
function renameTarget(ctx) {
  const { selection, doc } = ctx.store;
  const size = selection.nodes.length + selection.edges.length;
  const item =
    ctx.item ||
    (size === 1
      ? selection.nodes.length
        ? { kind: 'nodes', id: selection.nodes[0] }
        : { kind: 'edges', id: selection.edges[0] }
      : null);

  if (item?.kind === 'nodes') {
    const node = findNode(doc, item.id);

    return node ? { ...item, name: nodeLabel(node), node } : null;
  }

  return item && (doc.edges || []).some((edge) => edge.id === item.id)
    ? { ...item, name: 'connection', node: null }
    : null;
}

// --- actions shared with the toolbar -------------------------------------------

// Delete and Ungroup remove what the user was working on, so focus moves on
// to the outline row that takes its place, or to the canvas when no row is
// left (WCAG 2.4.3). Outline rows carry their node id in their test id.
function outlineRows() {
  return [
    ...document.querySelectorAll(
      '[data-testid="builder-outline"] button[data-testid^="outline-item-"]',
    ),
  ];
}

function rowNodeId(row) {
  return row.dataset.testid.replace('outline-item-', '');
}

// The canvas <section> is named and handles the canvas keys. It is also
// where a deleted connection was selected, since the outline has no rows
// for connections.
function focusRowOrCanvas(row) {
  if (row) {
    row.focus();
  } else {
    document.getElementById('builder-canvas')?.focus();
  }
}

// Ids are generated, but quoted and escaped all the same.
function attr(value) {
  return `"${String(value).replace(/["\\]/g, '\\$&')}"`;
}

function outlineRow(id) {
  return document.querySelector(
    `[data-testid="builder-outline"] button[data-testid=${attr(`outline-item-${id}`)}]`,
  );
}

function canvasItem(kind, id) {
  const type = kind === 'edges' ? 'edge' : 'node';

  return document.querySelector(
    `#builder-canvas .vue-flow__${type}[data-id=${attr(id)}]`,
  );
}

// The part of the view an element is in, for a place to put focus when the
// element is gone: 'canvas' (the canvas, its nodes and connections, not its
// zoom buttons or Keyboard help), 'outline', 'inspector', or '' elsewhere.
function regionOf(element) {
  if (element?.closest?.(CANVAS)) {
    return element.matches(CANVAS) || element.matches(CANVAS_ITEM)
      ? 'canvas'
      : '';
  }
  if (element?.closest?.('.builder-outline')) {
    return 'outline';
  }

  return element?.closest?.('.builder-inspector') ? 'inspector' : '';
}

// The heading of each region takes focus in its place; the canvas section
// is its own. On the landing, the shown tab does.
const REGION_FOCUS = {
  canvas: '#builder-canvas',
  outline: '#outline-title',
  inspector: '#inspector-title',
};

/**
 * Notes what has focus before a command runs, so that focus can go back to
 * the same thing if the command re-renders or removes it (see keepFocus):
 * an outline row or a canvas node or connection, by its id, else an element
 * by its id or test id. Only focus inside the Builder is noted.
 *
 * @returns {{element: Element, find: () => (Element|null), region: string,
 *   editing: boolean}|null}
 */
function noteFocus() {
  const doc = globalThis.document;
  const element = doc?.activeElement;

  if (!element || element === doc.body || !element.closest?.('.builder-root')) {
    return null;
  }

  const row = element.matches(OUTLINE_ROW) && element.dataset.testid;
  const item = itemOf(element, 'canvas');
  const testId = element.dataset?.testid;
  let find = () => null;

  if (row) {
    find = () => outlineRow(row.replace('outline-item-', ''));
  } else if (item) {
    find = () => canvasItem(item.kind, item.id);
  } else if (element.id) {
    find = () => doc.getElementById(element.id);
  } else if (
    testId &&
    doc.querySelectorAll(`[data-testid=${attr(testId)}]`).length === 1
  ) {
    find = () => doc.querySelector(`[data-testid=${attr(testId)}]`);
  }

  return {
    element,
    find,
    region: regionOf(element),
    editing: Boolean(element.closest('.builder-root--editing')),
  };
}

function focusLost() {
  const active = document.activeElement;

  return !active || active === document.body || !active.isConnected;
}

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

/**
 * Focuses an element as soon as it can take focus. Vue Flow shows a node
 * it has just rendered only once it has measured it, a frame or two later,
 * and a hidden node cannot take focus, so this tries again on the next
 * frames. It gives up, as done, once focus has gone anywhere else.
 *
 * @param {Element} element
 * @returns {Promise<boolean>} false when the element never took focus and
 *   nothing else did either
 */
async function focusSoon(element) {
  const from = document.activeElement;

  for (let frame = 0; frame < 10 && element?.isConnected; frame += 1) {
    const active = document.activeElement;

    if (active !== from && !focusLost()) {
      return true;
    }

    element.focus();

    if (document.activeElement === element) {
      return true;
    }

    await nextFrame();
  }

  return false;
}

/**
 * Puts focus back after a command, when the command left it on <body> or on
 * an element it removed (WCAG 2.4.3): on the same item, re-rendered (a row
 * that moved into a group, a node an undo put back), or else on the heading
 * of the region it was in, the canvas, or on the landing its shown tab.
 * Vue Flow renders a tick after the store, so this waits two.
 *
 * @param {object|null} note noteFocus's
 */
async function keepFocus(note) {
  if (!note) {
    return;
  }

  await nextTick();
  await nextTick();

  const candidates = [
    note.element.isConnected && note.element,
    note.find(),
    note.editing
      ? document.querySelector(REGION_FOCUS[note.region] || REGION_FOCUS.canvas)
      : document.querySelector('.builder-tab[aria-selected="true"]'),
  ];

  for (const element of candidates) {
    if (!focusLost() || (element && (await focusSoon(element)))) {
      return;
    }
  }
}

/**
 * Deletes the selection, then focuses the outline row that took its place.
 *
 * @param {object} store
 */
export async function deleteSelection(store) {
  const removed = new Set(store.selection.nodes);
  const index = outlineRows().findIndex((row) => removed.has(rowNodeId(row)));

  store.removeSelection();
  await nextTick();

  const rows = outlineRows();
  focusRowOrCanvas(index < 0 ? null : rows[Math.min(index, rows.length - 1)]);
}

/**
 * Ungroups the selected group, then focuses one of its members' rows.
 *
 * @param {object} store
 */
export async function ungroupSelection(store) {
  const groupId = selectedGroup(store).id;
  const members = new Set(
    store.doc.nodes
      .filter((node) => node.parentId === groupId)
      .map((node) => node.id),
  );
  const index = outlineRows().findIndex((row) => rowNodeId(row) === groupId);

  store.ungroup();
  await nextTick();

  const rows = outlineRows();
  focusRowOrCanvas(
    rows.find((row) => members.has(rowNodeId(row))) ||
      rows[Math.min(index, rows.length - 1)],
  );
}

/**
 * Groups the selected nodes, then focuses the new group: its outline row,
 * or with `canvas` (the key came from the canvas) its node there. Grouping
 * moves the members' rows and nodes into the group, which re-renders the
 * one that had focus.
 *
 * @param {object} store
 * @param {object} [options]
 * @param {boolean} [options.canvas]
 */
async function groupSelection(store, { canvas = false } = {}) {
  const group = store.group();

  if (!group || !globalThis.document) {
    return;
  }

  // Vue Flow renders the new node a tick after the store changes.
  await nextTick();
  await nextTick();

  const row = outlineRow(group.id);
  const node = canvasItem('nodes', group.id);
  const [first, second] = canvas ? [node, row] : [row, node];

  if (!(first && (await focusSoon(first)))) {
    await focusSoon(second);
  }
}

// The save states in which nothing waits to be sent, once none is pending.
const NOTHING_PENDING = ['idle', 'saved', 'offline'];

function nothingToSave({ autosave, saveState }) {
  return (
    Boolean(autosave) &&
    saveState?.pending === 0 &&
    NOTHING_PENDING.includes(saveState.status)
  );
}

/**
 * Saves now, as Save now's key and the palette do, what the user sees: the
 * focused text field is committed first, as its change event would commit
 * it (the Diagram name commits only on change), and the Inspector's
 * unapplied edits are settled (view.settleEdits): valid edits are merged
 * into their element as it is now and applied, as Apply would, even while a
 * redo is pending. Edits it cannot apply stay in the Inspector, and the
 * outcome says so. When that leaves nothing to save, nothing is sent and no
 * snapshot is made, and the outcome says that instead.
 *
 * @param {object} ctx createCommandContext's
 * @returns {Promise<object>} the save state
 */
export async function saveDraft({ store, view }) {
  // The queue's work when the key was pressed, and whether it had ended by
  // the time the edits are settled: until then, an edit may still be on its
  // way to the queue, and the save state would not count it yet.
  const work = store.queueWork;
  let idle = !work;

  Promise.resolve(work).then(() => {
    idle = true;
  });

  const unapplied = (await view.settleEdits?.()) || '';
  const note = unapplied
    ? `The Inspector's changes are not applied yet: ${unapplied}.`
    : '';

  if (idle && store.queueWork === work && nothingToSave(store)) {
    store.announce(
      note ? `No changes to save. ${note}` : 'No changes to save.',
      { slot: 'save' },
    );

    return store.saveState;
  }

  return store.saveNow({ announce: true, note });
}

// In a free spot of the part of the canvas in view (see addInView).
function addNode(ctx, options) {
  addInView(ctx, options);
}

// --- choices -------------------------------------------------------------------

function imageOf(node) {
  const drives = node.device?.spec?.hardware?.drives;

  return (Array.isArray(drives) && drives[0]?.image) || '';
}

// Items by id; the first wins, as find() would have it.
function byId(items) {
  const map = new Map();

  for (const item of items) {
    if (!map.has(item.id)) {
      map.set(item.id, item);
    }
  }

  return map;
}

// The networks a node is on: a device through its connections and its
// interfaces' VLANs, a switch through the network it is bound to. The
// connections and networks are read once, here, rather than for every node,
// so listing thousands of nodes stays linear.
//
// Returns networksOf(node): the node's networks, in the document's order.
function nodeNetworks(doc) {
  const networks = doc.networks || [];
  const ids = byId(networks);
  const order = new Map(networks.map((network, index) => [network, index]));
  const named = new Map();
  const linked = new Map();

  for (const network of networks) {
    const name = String(network.name || '').toLowerCase();

    named.set(name, [...(named.get(name) || []), network]);
  }

  for (const edge of doc.edges || []) {
    const network = ids.get(edge.networkId);

    for (const id of network ? [edge.sourceNodeId, edge.targetNodeId] : []) {
      linked.set(id, (linked.get(id) || new Set()).add(network));
    }
  }

  return (node) => {
    if (node.kind === 'switch') {
      return [ids.get(node.switch?.networkId)].filter(Boolean);
    }

    const found = new Set(linked.get(node.id));

    for (const iface of specInterfaces(node)) {
      const name = String(iface?.vlan || '').toLowerCase();

      for (const network of (name && named.get(name)) || []) {
        found.add(network);
      }
    }

    return [...found].sort((a, b) => order.get(a) - order.get(b));
  };
}

// What a node is found by beside its name, named so the command palette can
// say which one matched.
function nodeFields(node, networks, interfaces, image) {
  const hostname = node.device?.hostname;

  return [
    ['Hostname', hostname],
    ['Label', node.label !== hostname && node.label],
    ['Image', image],
    ...networks.flatMap((network) => [
      ['Network', network.name],
      ['VLAN', network.alias && `VLAN ${network.alias}`],
    ]),
    ...interfaces.flatMap((iface) => [
      ['IP address', iface?.address],
      ['MAC address', iface?.mac],
    ]),
  ]
    .filter(([, value]) => value)
    .map(([label, value]) => ({ label, value: String(value) }));
}

/**
 * Every node, as a choice for Go to node. Beside the name, a node is found
 * by its hostname, label, image, networks and VLAN aliases, and the IP and
 * MAC addresses of its interfaces: its `fields`, each {label, value}, and as
 * plain words its keywords.
 *
 * The document is read in one pass, whatever its size. It is read as the
 * plain object: the store replaces its document on every edit rather than
 * changing it, so Vue need not track every field of every node.
 *
 * @param {object} doc
 * @param {string[]} [ids] only these nodes, in the document's order
 * @returns {object[]} choices with the node's id as `value`
 */
export function nodeChoices(doc, ids = null) {
  const plain = toRaw(doc);
  const nodes = plain.nodes || [];
  const nodesById = byId(nodes);
  const networksOf = nodeNetworks(plain);
  const wanted = ids && new Set(ids);

  return nodes.flatMap((node) => {
    if (wanted && !wanted.has(node.id)) {
      return [];
    }

    const networks = networksOf(node);
    const interfaces = specInterfaces(node);
    const image = imageOf(node);
    const parent = node.parentId ? nodesById.get(node.parentId) : null;
    const from = includedFrom(node);
    const detail = [
      kindMeta(node.kind).label,
      node.kind === 'device' && (image || 'no image'),
      parent && `in ${nodeLabel(parent)}`,
      networks.map((network) => network.name).join(', '),
      from && `included from ${from}, read only`,
    ].filter(Boolean);

    return {
      id: node.id,
      title: nodeLabel(node),
      detail: detail.join(' · '),
      icon: nodeIconKey(node),
      keywords: [
        node.device?.hostname,
        node.label,
        image,
        ...networks.flatMap((network) => [
          network.name,
          network.alias && `VLAN ${network.alias}`,
          network.alias && String(network.alias),
        ]),
        ...interfaces.flatMap((iface) => [iface?.address, iface?.mac]),
      ].filter(Boolean),
      fields: nodeFields(node, networks, interfaces, image),
      value: node.id,
    };
  });
}

/**
 * Every network, as a choice for Go to network: by name or VLAN alias. Its
 * switches are counted in one pass, as nodeChoices reads the document.
 *
 * @param {object} doc
 * @returns {object[]} choices with the network's id as `value`
 */
function networkChoices(doc) {
  const plain = toRaw(doc);
  const switchCount = new Map();

  for (const node of switches(plain)) {
    const id = node.switch?.networkId;

    switchCount.set(id, (switchCount.get(id) || 0) + 1);
  }

  return (plain.networks || []).map((network) => {
    const on = switchCount.get(network.id) || 0;

    return {
      id: network.id,
      title: network.name,
      detail: [
        network.alias ? `VLAN ${network.alias}` : 'no VLAN alias',
        count(on, 'switch', 'switches'),
      ].join(' · '),
      // 'vlan' draws a note, the note kind's registry icon.
      icon: 'network-wired',
      keywords: network.alias
        ? [String(network.alias), `VLAN ${network.alias}`]
        : [],
      fields: network.alias
        ? [{ label: 'VLAN', value: `VLAN ${network.alias}` }]
        : [],
      disabled: on ? '' : 'No switch shows this network.',
      value: network.id,
    };
  });
}

// Selects a node alone, or with `additive` adds it to the selection or takes
// it out, as Shift+Enter does on the canvas; the plain choice then shows it.
// The palette, which stays open for Shift+Enter, says the change on its own
// status line: the live region waits for the palette to close, and by then
// the message may no longer hold.
function goToNode(ctx, id) {
  const { store } = ctx;

  if (ctx.additive) {
    const { selection, message } = pressSelection(
      store.doc,
      store.selection,
      { kind: 'nodes', id },
      true,
    );

    store.select(selection);

    if (ctx.source !== 'palette') {
      store.announce(message);
    }

    return;
  }

  store.select({ nodes: [id], edges: [] });
  store.announce(`Selected ${nodeLabel(findNode(store.doc, id)) || 'node'}`);
  ctx.view.showNode(id);
}

function draftChoices(store) {
  const name = (item) => item.name || item.title || item.target || item.id;
  const from = (items, where) =>
    (items || []).map((item) => ({
      id: `${where}:${item.owner || ''}:${item.id}`,
      title: name(item),
      detail: [where, item.owner && `Owner: ${item.owner}`, item.description]
        .filter(Boolean)
        .join(' · '),
      keywords: [item.target, item.owner].filter(Boolean),
      value: item,
    }));

  return [
    ...from(store.drafts?.mine, 'My Drafts'),
    ...from(store.drafts?.shared, 'Shared Drafts'),
    ...from(store.documents, 'Published Diagrams'),
  ];
}

// --- the registry ----------------------------------------------------------------

const BOTH = ['editor', 'landing'];
const LANDING = ['landing'];

function theme(value, title) {
  return {
    id: `view.theme.${value}`,
    title: `Theme: ${title}`,
    group: 'View',
    keywords: ['appearance', 'colors', 'dark mode', 'light mode'],
    views: BOTH,
    when: ({ store }) =>
      store.theme === value ? 'This is the current theme.' : true,
    run: ({ view }) => view.setTheme(value),
  };
}

// Lays the diagram out with one layout, which the draft then keeps (the
// toolbar's layout menu has the same choices).
function layoutChoice({ id, label, summary }) {
  return {
    id: `structure.layout.${id}`,
    title: `Layout: ${label}`,
    group: 'Structure',
    keywords: ['auto layout', 'arrange', 'algorithm'],
    when: editable,
    detail: ({ store }) =>
      store.currentLayout === id ? `${summary} · Current layout` : summary,
    run: ({ store }) => store.layout({ algorithm: id }),
  };
}

// Auto-group with one strategy (the toolbar's Auto-group menu has the same
// choices).
function autoGroupChoice({ id, label, summary }) {
  return {
    id: `structure.autoGroup.${id}`,
    title: `Auto-group ${label.toLowerCase()}`,
    group: 'Structure',
    keywords: ['auto group', 'cluster', 'organize'],
    when: editable,
    detail: ({ store }) =>
      store.selection.nodes.length
        ? `${summary} · Selected nodes only`
        : summary,
    run: ({ store }) => store.autoGroup(id),
  };
}

function landingTab(id, title) {
  return {
    id: `drafts.tab.${id}`,
    title: `Show ${title}`,
    group: 'Drafts',
    keywords: ['tab', 'list'],
    views: LANDING,
    when: ({ view }) =>
      view.draftsTab === id ? 'This tab is already shown.' : true,
    run: ({ view }) => view.showDraftsTab(id),
  };
}

export const COMMANDS = [
  // --- General
  {
    id: 'palette.open',
    title: 'Command palette',
    group: 'General',
    keys: ['Mod+K'],
    scope: 'fields',
    page: true,
    views: BOTH,
    palette: false,
    phrase: 'opens the command palette',
    run: ({ view }) => view.openPalette({}),
  },
  {
    id: 'shortcuts.open',
    title: 'Keyboard shortcuts',
    group: 'General',
    keywords: ['help', 'keys', 'keyboard', 'hotkeys', 'bindings'],
    keys: ['?'],
    scope: 'editor',
    page: true,
    views: BOTH,
    phrase: 'lists every shortcut',
    run: ({ view }) => view.openShortcuts(),
  },
  {
    // The header's Settings. No keys by default: the user may give it some.
    id: 'settings.open',
    title: 'Settings…',
    group: 'General',
    keywords: [
      'preferences',
      'options',
      'layout algorithm',
      'theme',
      'minimap',
      'zoom',
      'motion',
      'single-key',
    ],
    views: BOTH,
    run: ({ view }) => view.openSettings(),
  },
  {
    id: 'help.open',
    title: 'Builder Flow help',
    group: 'General',
    keywords: ['documentation', 'docs', 'manual'],
    views: BOTH,
    detail: () => 'Opens the documentation in a new tab',
    run: () => globalThis.window?.open(HELP_URL, '_blank', 'noopener'),
  },

  // --- Edit
  {
    id: 'edit.undo',
    title: 'Undo',
    group: 'Edit',
    keys: ['Mod+Z'],
    scope: 'editor',
    phrase: 'undoes',
    when: (ctx) =>
      editable(ctx) === true
        ? ctx.store.canUndo || 'Nothing to undo.'
        : READ_ONLY,
    detail: ({ store }) =>
      store.canUndo ? `Last change: ${store.history.undoLabel()}` : '',
    run: ({ store }) => store.undo(),
  },
  {
    id: 'edit.redo',
    title: 'Redo',
    group: 'Edit',
    keys: { mac: ['Mod+Shift+Z'], other: ['Mod+Shift+Z', 'Mod+Y'] },
    scope: 'editor',
    phrase: 'redoes',
    when: (ctx) =>
      editable(ctx) === true
        ? ctx.store.canRedo || 'Nothing to redo.'
        : READ_ONLY,
    run: ({ store }) => store.redo(),
  },
  {
    id: 'edit.copy',
    title: 'Copy',
    group: 'Edit',
    keys: ['Mod+C'],
    scope: 'editor',
    phrase: 'copies',
    when: ({ store }) => hasSelection(store) || 'Nothing is selected to copy.',
    // Selected text is the browser's to copy.
    skipKey: () => Boolean(String(globalThis.window?.getSelection?.() || '')),
    run: ({ store }) => store.copy(),
  },
  {
    id: 'edit.paste',
    title: 'Paste',
    group: 'Edit',
    keys: ['Mod+V'],
    scope: 'editor',
    phrase: 'pastes',
    when: (ctx) =>
      editable(ctx) === true
        ? Boolean(ctx.store.clipboard) || 'Clipboard is empty.'
        : READ_ONLY,
    run: ({ store }) => store.paste(),
  },
  {
    id: 'edit.duplicate',
    title: 'Duplicate',
    group: 'Edit',
    keywords: ['clone', 'copy'],
    keys: ['Mod+D'],
    scope: 'editor',
    phrase: 'duplicates',
    when: (ctx) =>
      editable(ctx) === true
        ? hasSelection(ctx.store) || 'Nothing is selected to duplicate.'
        : READ_ONLY,
    detail: () => 'Copies the selection and pastes it beside the original',
    run: ({ store }) => store.duplicate(),
  },
  {
    id: 'edit.delete',
    title: 'Delete selection',
    group: 'Edit',
    keywords: ['remove'],
    keys: { mac: ['Backspace', 'Delete'], other: ['Delete', 'Backspace'] },
    scope: ['canvas', 'outline'],
    local: true,
    fixed: true,
    when: (ctx) =>
      editable(ctx) === true
        ? hasSelection(ctx.store) || 'Nothing is selected to delete.'
        : READ_ONLY,
    run: ({ store }) => deleteSelection(store),
  },
  {
    // An outline row renames in place (BuilderOutline handles F2 there); on
    // the canvas and from the palette, the Inspector's first field, the
    // name or the connection's label, takes focus.
    id: 'edit.rename',
    title: 'Rename',
    group: 'Edit',
    keywords: ['name', 'hostname', 'label'],
    keys: ['F2'],
    scope: ['canvas', 'outline'],
    fixed: true,
    phrase: 'renames the focused item in the Inspector',
    label: (ctx) => {
      const target = renameTarget(ctx);

      return target ? `Rename ${target.name}` : 'Rename';
    },
    when: (ctx) => {
      if (ctx.store.readOnly) {
        return READ_ONLY;
      }

      const target = renameTarget(ctx);
      if (!target) {
        return 'Select one node or connection to rename.';
      }

      return (
        includedReason(target.node) ||
        (target.node?.kind === 'switch'
          ? networkRefusal(ctx.store.doc, target.node.switch?.networkId)
          : '') ||
        true
      );
    },
    run: (ctx) => {
      const target = renameTarget(ctx);

      ctx.store.select({ nodes: [], edges: [], [target.kind]: [target.id] });
      ctx.view.focusInspector({ field: true });
    },
  },

  // --- Selection
  {
    id: 'selection.all',
    title: 'Select all',
    group: 'Selection',
    keywords: ['everything'],
    keys: ['Mod+A'],
    scope: 'editor',
    phrase: 'selects everything',
    when: ({ store }) =>
      store.doc.nodes.length > 0 ||
      (store.doc.edges || []).length > 0 ||
      'The diagram is empty.',
    run: ({ store }) => store.selectAll(),
  },
  {
    id: 'selection.clear',
    title: 'Clear selection',
    group: 'Selection',
    keywords: ['deselect', 'none'],
    keys: ['Escape'],
    scope: 'canvas',
    local: true,
    fixed: true,
    phrase: 'clears the selection',
    when: ({ store }) => hasSelection(store) || 'Nothing is selected.',
    run: ({ store }) => {
      store.clearSelection();
      store.announce('Selection cleared');
    },
  },
  {
    id: 'selection.press',
    title: 'Select or deselect the focused item',
    group: 'Selection',
    keys: ['Enter', 'Space'],
    scope: ['canvas', 'outline'],
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'selection.toggle',
    title: 'Add the focused item to the selection, or take it out',
    group: 'Selection',
    keys: ['Shift+Enter'],
    scope: ['canvas', 'outline'],
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'selection.nudge',
    title: 'Move the selected nodes 10 pixels',
    group: 'Selection',
    keys: ['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'selection.nudgeFine',
    title: 'Move the selected nodes 1 pixel',
    group: 'Selection',
    keys: [
      'Shift+ArrowLeft',
      'Shift+ArrowUp',
      'Shift+ArrowRight',
      'Shift+ArrowDown',
    ],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    // Right and Down grow the group from its bottom right corner, Left and
    // Up shrink it (BuilderCanvas.vue).
    id: 'selection.resize',
    title: 'Resize the selected group 10 pixels',
    group: 'Selection',
    keys: [
      'Alt+Shift+ArrowLeft',
      'Alt+Shift+ArrowUp',
      'Alt+Shift+ArrowRight',
      'Alt+Shift+ArrowDown',
    ],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'outline.move',
    title: 'Move between outline rows',
    group: 'Selection',
    keys: ['ArrowUp', 'ArrowDown', 'Home', 'End'],
    scope: 'outline',
    local: true,
    fixed: true,
    palette: false,
  },

  // --- Structure
  {
    id: 'structure.group',
    title: 'Group selection',
    group: 'Structure',
    keywords: ['container', 'wrap'],
    keys: ['Mod+G'],
    scope: 'editor',
    phrase: 'groups the selection',
    when: (ctx) =>
      editable(ctx) === true
        ? ctx.store.selection.nodes.length > 0 ||
          'Select at least one node to group.'
        : READ_ONLY,
    detail: ({ store }) =>
      store.selection.nodes.length
        ? `${count(store.selection.nodes.length, 'node')} selected`
        : '',
    run: ({ store }) =>
      groupSelection(store, {
        canvas: regionOf(globalThis.document?.activeElement) === 'canvas',
      }),
  },
  {
    id: 'structure.ungroup',
    title: 'Ungroup',
    group: 'Structure',
    keys: ['Mod+Shift+G'],
    scope: 'editor',
    phrase: 'ungroups',
    when: (ctx) =>
      editable(ctx) === true
        ? Boolean(selectedGroup(ctx.store)) || 'Select a group first.'
        : READ_ONLY,
    run: ({ store }) => ungroupSelection(store),
  },
  ...GROUPING_STRATEGIES.map(autoGroupChoice),
  {
    // Runs the draft's layout again; each layout has a command of its own.
    id: 'structure.layout',
    title: 'Auto layout',
    group: 'Structure',
    keywords: ['arrange', 'tidy', 'organize'],
    when: editable,
    detail: ({ store }) =>
      `Arrange the nodes with ${layoutAlgorithm(store.currentLayout)?.label || 'the current layout'}`,
    run: ({ store }) => store.layout(),
  },
  ...LAYOUT_ALGORITHMS.map(layoutChoice),
  {
    id: 'structure.restoreLayout',
    title: 'Restore previous layout',
    group: 'Structure',
    keywords: ['undo layout', 'arrange'],
    when: (ctx) =>
      editable(ctx) === true
        ? ctx.store.canRestoreLayout || 'There is no earlier layout to restore.'
        : READ_ONLY,
    detail: () => 'Put every node back where it was before the last layout',
    run: ({ store }) => store.restoreLayout(),
  },
  {
    id: 'structure.connect',
    title: 'Connect device to a switch',
    group: 'Structure',
    keywords: ['link', 'cable', 'interface', 'network'],
    steps: ['Device', 'Switch'],
    when: (ctx) => {
      if (ctx.store.readOnly) {
        return READ_ONLY;
      }
      if (!devices(ctx.store.doc).length) {
        return 'Add a device first.';
      }

      return switches(ctx.store.doc).length > 0 || 'Add a switch first.';
    },
    detail: () => 'Choose a device, then a switch',
    choices: ({ store }, picked = []) => {
      if (picked.length === 0) {
        return devices(store.doc).map((node) => ({
          id: node.id,
          title: nodeLabel(node),
          detail: imageOf(node) || kindMeta(node.kind).label,
          icon: nodeIconKey(node),
          disabled: includedReason(node),
          value: node.id,
        }));
      }

      return switches(store.doc).map((node) => {
        const network = findNetwork(store.doc, node.switch?.networkId);

        return {
          id: node.id,
          title: nodeLabel(node),
          detail: [
            network && `network ${network.name}`,
            network?.alias && `VLAN ${network.alias}`,
            'adds a new interface',
          ]
            .filter(Boolean)
            .join(' · '),
          icon: 'switch',
          value: node.id,
        };
      });
    },
    run: ({ store }, _, picked) =>
      store.connect({
        sourceNodeId: picked[0].value,
        sourceHandleId: null,
        targetNodeId: picked[1].value,
      }),
  },
  {
    // The outline lists nodes only, so this is where a connection is found
    // by name and removed without a pointer; Delete on a canvas connection
    // does the same. The interface stays, free to connect again.
    id: 'structure.disconnect',
    title: 'Disconnect',
    group: 'Structure',
    keywords: [
      'remove connection',
      'delete connection',
      'unlink',
      'cable',
      'interface',
      'network',
    ],
    steps: ['Connection'],
    when: (ctx) =>
      editable(ctx) === true
        ? (ctx.store.doc.edges || []).length > 0 ||
          'The diagram has no connections.'
        : READ_ONLY,
    detail: () => 'Choose a connection to remove',
    choices: ({ store }) =>
      connectionList(toRaw(store.doc)).map((link) => ({
        id: link.id,
        title: link.name,
        detail: [
          `network ${link.networkName}`,
          link.label && `labelled ${link.label}`,
        ]
          .filter(Boolean)
          .join(' · '),
        icon: 'link',
        keywords: [link.networkName, link.label].filter(Boolean),
        disabled: link.locked,
        value: link.id,
      })),
    run: ({ store }, choice) =>
      store.remove({ nodes: [], edges: [choice.value] }),
  },

  // --- Add
  {
    id: 'add.device',
    title: 'Add device',
    group: 'Add',
    keywords: ['vm', 'host', 'node', 'template'],
    steps: ['Template'],
    when: editable,
    detail: () => 'Choose a template',
    choices: () => [
      {
        id: 'device',
        title: 'Device',
        detail: PALETTE.find((item) => item.kind === 'device').hint,
        icon: 'server',
        value: '',
      },
      ...DEVICE_TEMPLATES.map((template) => ({
        id: template.id,
        title: template.label,
        detail: `${template.description} · ${template.image || 'no image'}`,
        icon: template.iconKey,
        value: template.id,
      })),
    ],
    run: (ctx, choice) => addNode(ctx, paletteNode('device', choice?.value)),
  },
  ...['switch', 'note', 'group'].map((kind) => {
    const item = PALETTE.find((entry) => entry.kind === kind);

    return {
      id: `add.${kind}`,
      title: `Add ${item.label.toLowerCase()}`,
      group: 'Add',
      when: editable,
      detail: () => item.hint,
      run: (ctx) => addNode(ctx, { kind }),
    };
  }),

  // --- Go to
  {
    id: 'goto.node',
    title: 'Go to node',
    group: 'Go to',
    keywords: ['find', 'search', 'jump', 'select'],
    keys: ['Mod+Shift+O'],
    scope: 'editor',
    prefix: '@',
    steps: ['Node'],
    phrase: 'goes to a node by name or address',
    when: ({ store }) => store.doc.nodes.length > 0 || 'The diagram is empty.',
    choices: ({ store }) => nodeChoices(store.doc),
    run: (ctx, choice) => goToNode(ctx, choice.value),
  },
  {
    id: 'goto.network',
    title: 'Go to network',
    group: 'Go to',
    keywords: ['vlan', 'find', 'switch'],
    prefix: '#',
    steps: ['Network'],
    when: ({ store }) =>
      (store.doc.networks || []).length > 0 || 'The diagram has no networks.',
    choices: ({ store }) => networkChoices(store.doc),
    run: (ctx, choice) => {
      const ids = switches(ctx.store.doc)
        .filter((node) => node.switch?.networkId === choice.value)
        .map((node) => node.id);

      if (!ids.length) {
        ctx.store.announce(choice.disabled || 'No switch shows this network.');

        return;
      }

      ctx.store.select({ nodes: ids, edges: [] });
      ctx.store.announce(
        `Selected ${count(ids.length, 'switch', 'switches')} of network ${choice.title}`,
      );
      ctx.view.showNode(ids[0]);
    },
  },
  {
    id: 'goto.canvas',
    title: 'Focus canvas',
    group: 'Go to',
    keywords: ['diagram'],
    run: ({ view }) => view.focusCanvas(),
  },
  {
    id: 'goto.outline',
    title: 'Focus outline',
    group: 'Go to',
    run: ({ view }) => view.focusOutline(),
  },
  {
    id: 'goto.inspector',
    title: 'Focus Inspector',
    group: 'Go to',
    keywords: ['properties', 'fields'],
    run: ({ view }) => view.focusInspector({ field: false }),
  },
  {
    id: 'goto.drafts',
    title: 'Back to drafts',
    group: 'Go to',
    keywords: ['close', 'landing', 'list'],
    run: ({ view }) => view.closeEditor(),
  },

  // --- View
  {
    id: 'view.zoomIn',
    title: 'Zoom in',
    group: 'View',
    keywords: ['magnify', 'larger'],
    keys: ['=', '+'],
    scope: 'canvas',
    phrase: 'zooms in',
    when: ({ view }) =>
      view.canZoomIn !== false || 'The diagram is at its largest zoom.',
    run: ({ view }) => view.zoomIn(),
  },
  {
    id: 'view.zoomOut',
    title: 'Zoom out',
    group: 'View',
    keywords: ['smaller'],
    keys: ['-'],
    scope: 'canvas',
    phrase: 'zooms out',
    when: ({ view }) =>
      view.canZoomOut !== false || 'The diagram is at its smallest zoom.',
    run: ({ view }) => view.zoomOut(),
  },
  {
    id: 'view.fit',
    title: 'Fit diagram to view',
    group: 'View',
    keywords: ['zoom', 'whole', 'all'],
    keys: ['Shift+1'],
    scope: 'canvas',
    phrase: 'fits the diagram to the view',
    run: ({ view }) => view.fitView(),
  },
  {
    id: 'view.minimap',
    title: 'Show or hide minimap',
    group: 'View',
    keywords: ['overview', 'map'],
    label: ({ view }) => (view.showMinimap ? 'Hide minimap' : 'Show minimap'),
    run: ({ view }) => view.toggleMinimap(),
  },
  {
    // The header's Reset view. It leaves the diagram, the theme and the
    // shortcuts alone.
    id: 'view.reset',
    title: 'Reset view',
    group: 'View',
    keywords: ['default', 'columns', 'widths', 'panels', 'zoom', 'scroll'],
    detail: () => 'Column widths, zoom, minimap and scrolling',
    run: ({ view }) => view.resetView(),
  },
  {
    // The header's Focus mode button, which becomes Exit focus mode. The
    // same keys leave it; the browser's Escape leaves only full screen (see
    // focusMode.js). They work in text fields too, as they type nothing.
    id: 'view.focusMode',
    title: 'Focus mode',
    group: 'View',
    keywords: [
      'full screen',
      'fullscreen',
      'maximize',
      'distraction free',
      'hide navigation',
    ],
    keys: ['Mod+Shift+F'],
    scope: 'fields',
    phrase: 'turns focus mode on or off',
    label: ({ view }) => (view.focusMode ? 'Exit focus mode' : 'Focus mode'),
    detail: ({ view }) =>
      view.focusMode
        ? 'Show the phenix navigation bar again'
        : 'Hide the phenix navigation bar and fill the screen',
    run: ({ view }) => view.toggleFocusMode(),
  },
  theme('system', 'System'),
  theme('light', 'Light'),
  theme('dark', 'Dark'),

  // --- Draft
  {
    // No button: every edit is saved as it is made. The key saves what is
    // still in a field or the Inspector (see saveDraft).
    id: 'draft.save',
    title: 'Save now',
    group: 'Draft',
    keys: ['Mod+S'],
    scope: 'fields',
    phrase: 'saves now',
    when: editable,
    run: (ctx) => saveDraft(ctx),
  },
  {
    id: 'draft.history',
    title: 'Draft history…',
    group: 'Draft',
    keywords: ['snapshot', 'restore', 'version'],
    run: ({ view }) => view.openHistory(),
  },
  {
    id: 'draft.export',
    title: 'Export…',
    group: 'Draft',
    keywords: ['download', 'image', 'json', 'yaml', 'png', 'svg'],
    detail: () => 'JSON, YAML, PNG or SVG',
    run: ({ view }) => view.openDialog('export'),
  },
  {
    // The toolbar's Upload; the landing's is drafts.upload.
    id: 'draft.upload',
    title: 'Upload…',
    group: 'Draft',
    keywords: ['open', 'file', 'import'],
    detail: () => 'A Builder document from a file',
    when: creatable,
    run: ({ view }) => view.openDialog('import'),
  },
  {
    id: 'draft.scenario',
    title: 'Scenario…',
    group: 'Draft',
    keywords: ['apps'],
    when: editable,
    run: ({ view }) => view.openDialog('scenario'),
  },
  {
    id: 'draft.publish',
    title: 'Publish…',
    group: 'Draft',
    keywords: ['experiment', 'topology', 'config'],
    when: publishable,
    detail: () => 'Topology, scenario or experiment',
    run: ({ view }) => view.openDialog('publish'),
  },

  // --- Drafts landing
  {
    id: 'drafts.blank',
    title: 'Blank diagram',
    group: 'Drafts',
    keywords: ['new', 'create', 'start'],
    views: LANDING,
    when: creatable,
    run: ({ view }) => view.startBlank(),
  },
  {
    id: 'drafts.import',
    title: 'Import…',
    group: 'Drafts',
    keywords: ['topology', 'experiment', 'convert', 'generate'],
    views: LANDING,
    detail: () => 'From a topology or experiment config on the server',
    when: creatable,
    run: ({ view }) => view.openLanding('generate'),
  },
  {
    // The landing's Upload, listed with the other ways to start a draft.
    id: 'drafts.upload',
    title: 'Upload…',
    group: 'Drafts',
    keywords: ['open', 'file', 'import', 'new'],
    views: LANDING,
    detail: () => 'A Builder document from a file, as a new draft',
    when: creatable,
    run: ({ view }) => view.openLanding('import'),
  },
  {
    id: 'drafts.open',
    title: 'Open draft',
    group: 'Drafts',
    keywords: ['diagram', 'published', 'find'],
    views: LANDING,
    steps: ['Draft'],
    when: ({ store }) =>
      draftChoices(store).length > 0 || 'There are no drafts to open.',
    choices: ({ store }) => draftChoices(store),
    run: ({ view }, choice) => view.openDraft(choice.value),
  },
  landingTab('mine', 'My Drafts'),
  landingTab('shared', 'Shared Drafts'),
  landingTab('published', 'Published Diagrams'),
];

const BY_ID = new Map(COMMANDS.map((command) => [command.id, command]));

/**
 * @param {string|object} idOrCommand
 * @returns {object|undefined} the command
 */
export function getCommand(idOrCommand) {
  return typeof idOrCommand === 'string' ? BY_ID.get(idOrCommand) : idOrCommand;
}

// A command without a scope of its own, which only a user's key can give
// keys, takes the editor-wide one.
function scopesOf(command) {
  return [].concat(command.scope || 'editor');
}

function viewsOf(command) {
  return command.views || ['editor'];
}

/**
 * @param {{view: object}} ctx
 * @returns {'editor'|'landing'}
 */
export function viewOf(ctx) {
  return ctx?.view?.editing ? 'editor' : 'landing';
}

/**
 * The commands of the open view, for the palette: every one it lists,
 * available or not.
 *
 * @param {object} ctx
 * @returns {object[]}
 */
export function paletteCommands(ctx) {
  const view = viewOf(ctx);

  return COMMANDS.filter(
    (command) => command.palette !== false && viewsOf(command).includes(view),
  );
}

/**
 * Whether a command can run now.
 *
 * @param {string|object} idOrCommand
 * @param {object} ctx
 * @returns {true|string} true, or why it cannot
 */
export function availability(idOrCommand, ctx) {
  const command = getCommand(idOrCommand);

  if (!command) {
    return 'There is no such command.';
  }

  if (!viewsOf(command).includes(viewOf(ctx))) {
    return viewOf(ctx) === 'editor'
      ? 'Go back to the drafts first.'
      : 'Open a draft first.';
  }

  const state = command.when ? command.when(ctx) : true;

  if (state === true) {
    return true;
  }

  return typeof state === 'string' && state
    ? state
    : `${command.title} is not available now.`;
}

/**
 * The title as things are now.
 *
 * @param {string|object} idOrCommand
 * @param {object} ctx
 * @returns {string}
 */
export function commandTitle(idOrCommand, ctx) {
  const command = getCommand(idOrCommand);

  return command?.label?.(ctx) || command?.title || '';
}

/**
 * Runs a command, or says why it cannot run (through store.announce). A
 * command that asks for choices, run without them, opens the palette on it
 * instead: at its prefix ('@') or at its first step. Focus that the command
 * re-renders or removes goes back to the same item, or near it (keepFocus).
 *
 * @param {string|object} idOrCommand
 * @param {object} ctx createCommandContext's, with `source` and so on
 * @param {object} [choice] the last choice, for a command with steps
 * @param {object[]} [picked] every choice, in order
 * @returns {boolean} whether it ran (or opened the palette)
 */
export function runCommand(idOrCommand, ctx, choice, picked) {
  const command = getCommand(idOrCommand);
  const state = availability(command, ctx);

  if (state !== true) {
    ctx.store.announce(state);

    return false;
  }

  if (command.choices && choice === undefined) {
    ctx.view.openPalette(
      command.prefix ? { query: command.prefix } : { command: command.id },
    );

    return true;
  }

  const focus = noteFocus();
  const running = command.run?.(
    ctx,
    choice,
    picked || (choice === undefined ? [] : [choice]),
  );

  // After the command's own focus moves: Delete, Group and Ungroup move
  // focus once the diagram has rendered.
  Promise.resolve(running).finally(() => keepFocus(focus));

  return true;
}

// --- keys --------------------------------------------------------------------------

/**
 * A command's default keys on a platform.
 *
 * @param {string|object} idOrCommand
 * @param {'mac'|'other'} [platform]
 * @returns {string[]}
 */
export function defaultKeys(idOrCommand, platform = currentPlatform()) {
  const keys = getCommand(idOrCommand)?.keys;

  if (!keys) {
    return [];
  }

  return Array.isArray(keys) ? keys : keys[platform] || [];
}

/**
 * Whether users may change a command's keys.
 *
 * @param {string|object} idOrCommand
 * @returns {boolean}
 */
export function isCustomizable(idOrCommand) {
  const command = getCommand(idOrCommand);

  return Boolean(command && !command.fixed && !command.local);
}

/**
 * The keys a command answers to now: the user's keys or the defaults, less
 * the one-character keys while the single-key switch is off. Reactive.
 *
 * @param {string|object} idOrCommand
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @param {boolean} [options.all] keep one-character keys even while they are
 *   switched off (for the shortcut sheet and conflict checks)
 * @returns {string[]}
 */
export function commandKeys(
  idOrCommand,
  { platform = currentPlatform(), all = false } = {},
) {
  const command = getCommand(idOrCommand);

  if (!command) {
    return [];
  }

  const override = isCustomizable(command)
    ? shortcutOverride(command.id)
    : undefined;
  const keys = override ?? defaultKeys(command, platform);

  return all || keymapState.singleKeys
    ? keys
    : keys.filter((key) => !isCharacterKey(key));
}

function alternatives(labels) {
  if (labels.length <= 1) {
    return labels[0] || '';
  }

  return `${labels.slice(0, -1).join(', ')} or ${labels.at(-1)}`;
}

/**
 * A command's keys as the platform writes them: '⇧⌘Z', or 'Ctrl+Shift+Z or
 * Ctrl+Y'. '' when it has none.
 *
 * @param {string|object} idOrCommand
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @param {boolean} [options.text] name the named keys in words (keyText), for
 *   running text
 * @returns {string}
 */
export function shortcutLabel(
  idOrCommand,
  { platform = currentPlatform(), text = false } = {},
) {
  const label = text ? keyText : keyLabel;

  return alternatives(
    commandKeys(idOrCommand, { platform }).map((key) => label(key, platform)),
  );
}

/**
 * A command's aria-keyshortcuts value, or undefined when it has no keys, so
 * Vue leaves the attribute out.
 *
 * @param {string|object} idOrCommand
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @returns {string|undefined}
 */
export function ariaShortcuts(
  idOrCommand,
  { platform = currentPlatform() } = {},
) {
  const value = commandKeys(idOrCommand, { platform })
    .map((key) => ariaKey(key, platform))
    .join(' ');

  return value || undefined;
}

/**
 * A tooltip's text with the command's keys: 'Undo (⌘Z)', or the text alone
 * when the command has none.
 *
 * @param {string} text
 * @param {string|object} idOrCommand
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @returns {string}
 */
export function withShortcut(
  text,
  idOrCommand,
  { platform = currentPlatform() } = {},
) {
  const keys = shortcutLabel(idOrCommand, { platform });

  return keys ? `${text} (${keys})` : text;
}

function reachOf(command) {
  return new Set(scopesOf(command).flatMap((scope) => SCOPES[scope] || []));
}

/**
 * Whether a command's keys work in text fields, where a key that types a
 * character must be left to the field (see dispatchKeydown).
 *
 * @param {string|object} idOrCommand
 * @returns {boolean}
 */
export function worksInTextFields(idOrCommand) {
  return reachOf(getCommand(idOrCommand) || {}).has('field');
}

/**
 * The command whose keys work in text fields (Command palette, Save now)
 * that a key press in one is for, if any, as dispatchKeydown finds it there.
 * A field that keeps its other keys to itself (the outline's rename field)
 * lets these go on to the dispatcher.
 *
 * @param {KeyboardEvent} event
 * @returns {object|null}
 */
export function textFieldCommand(event) {
  return (
    COMMANDS.find(
      (entry) =>
        !entry.local &&
        worksInTextFields(entry) &&
        commandKeys(entry).some(
          (key) => !typesCharacter(key) && matchesKey(event, key),
        ),
    ) || null
  );
}

/**
 * The other commands a key would clash with if a command took it: those
 * that answer to it where the command would too.
 *
 * @param {string|object} idOrCommand
 * @param {string} spec
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @returns {object[]}
 */
export function shortcutConflicts(
  idOrCommand,
  spec,
  { platform = currentPlatform() } = {},
) {
  const command = getCommand(idOrCommand);
  const reach = reachOf(command || {});
  const views = viewsOf(command || {});

  return COMMANDS.filter(
    (other) =>
      other !== command &&
      viewsOf(other).some((view) => views.includes(view)) &&
      [...reachOf(other)].some((focus) => reach.has(focus)) &&
      commandKeys(other, { platform, all: true }).some((key) =>
        sameKey(key, spec, platform),
      ),
  );
}

// --- the dispatcher -------------------------------------------------------------------

// Text fields keep their own keys: typing, and their own undo and clipboard.
export const TYPING =
  'input, textarea, select, [contenteditable]:not([contenteditable="false"])';
const CANVAS = '.builder-canvas';
const CANVAS_ITEM = '.vue-flow__node, .vue-flow__edge';
const OUTLINE_ROW = '[data-testid="builder-outline"] .builder-outline__item';

// The app's own dialogs (Buefy modals) and the Builder's.
const APP_DIALOG =
  'dialog, [role="dialog"], [role="alertdialog"], [aria-modal="true"]';

/**
 * Where a key press is, for the scopes: 'dialog' (an open dialog keeps its
 * own keys), 'field', 'landing' (the drafts landing), 'canvas' (the canvas
 * itself, a node or a connection; not the zoom buttons or the Keyboard
 * help), 'outline' (an outline row), 'editor' (anywhere else in the editor,
 * and the page itself when the focused control was just removed), 'page'
 * (outside the Builder, on the rest of its page, such as the app header
 * link that brought the user here), or 'outside' (a text field or a dialog
 * of the app's, outside the Builder).
 *
 * @param {Element} target the event's target
 * @param {object} options
 * @param {Element} options.root the Builder's root element
 * @param {boolean} options.editing whether the editor is open
 * @returns {string}
 */
export function focusScope(target, { root, editing }) {
  const doc = target?.ownerDocument;

  if (!target || !doc) {
    return 'outside';
  }

  if (!(target === doc.body || root?.contains(target))) {
    return target.matches?.(TYPING) ||
      target.closest?.(APP_DIALOG) ||
      doc.querySelector?.('dialog[open]')
      ? 'outside'
      : 'page';
  }

  if (target.closest?.('dialog') || doc?.querySelector?.('dialog[open]')) {
    return 'dialog';
  }

  if (target.matches?.(TYPING)) {
    return 'field';
  }

  if (!editing) {
    return 'landing';
  }

  if (
    target.closest?.(CANVAS) &&
    (target.matches(CANVAS) || target.matches(CANVAS_ITEM))
  ) {
    return 'canvas';
  }

  return target.matches?.(OUTLINE_ROW) ? 'outline' : 'editor';
}

// The node, connection or outline row a key was pressed on.
function itemOf(target, focus) {
  if (focus === 'canvas') {
    const kind =
      (target.classList?.contains('vue-flow__node') && 'nodes') ||
      (target.classList?.contains('vue-flow__edge') && 'edges') ||
      '';
    const id = kind && target.getAttribute('data-id');

    return id ? { kind, id } : null;
  }

  if (focus === 'outline') {
    const id = String(target.dataset?.testid || '').replace(
      'outline-item-',
      '',
    );

    return id ? { kind: 'nodes', id } : null;
  }

  return null;
}

/**
 * Runs the command a key press is for, if any: the one key handler for the
 * whole Builder view. Bound once, to the window, so it sees presses on the
 * page itself as well; outside the Builder, only the commands marked `page`
 * answer (the palette's and the shortcut sheet's keys). A control that
 * handled the press itself (it called preventDefault) keeps it, as does IME
 * composition, and a text field keeps every key that types a character
 * (typesCharacter: on macOS, ⌥ keys too), whatever command the user gave it
 * to. A matched key is always taken from the browser, even when its command
 * cannot run: the reason is announced instead. Copy leaves selected text to
 * the browser.
 *
 * @param {KeyboardEvent} event
 * @param {object} ctx createCommandContext's
 * @param {object} options
 * @param {Element} options.root the Builder's root element
 * @returns {object|null} the command the press was for
 */
export function dispatchKeydown(event, ctx, { root }) {
  if (event.defaultPrevented || event.isComposing || event.keyCode === 229) {
    return null;
  }

  const focus = focusScope(event.target, {
    root,
    editing: Boolean(ctx.view.editing),
  });

  if (focus === 'outside' || focus === 'dialog') {
    return null;
  }

  const view = viewOf(ctx);
  const command = COMMANDS.find(
    (entry) =>
      !entry.local &&
      viewsOf(entry).includes(view) &&
      (focus === 'page'
        ? entry.page
        : scopesOf(entry).some((scope) => SCOPES[scope]?.includes(focus))) &&
      commandKeys(entry).some(
        (key) =>
          (focus !== 'field' || !typesCharacter(key)) && matchesKey(event, key),
      ),
  );

  if (!command) {
    return null;
  }

  const run = {
    ...ctx,
    source: 'key',
    event,
    focus,
    item: itemOf(event.target, focus),
  };

  if (command.skipKey?.(run)) {
    return null;
  }

  event.preventDefault();
  runCommand(command, run);

  return command;
}

// --- help text ---------------------------------------------------------------------

function phraseFor(id, platform) {
  const command = getCommand(id);
  const keys = shortcutLabel(command, { platform, text: true });

  return keys ? `${keys} ${command.phrase}` : '';
}

function sentence(parts) {
  const list = listOf(parts.filter(Boolean));

  return list ? `${list[0].toUpperCase()}${list.slice(1)}.` : '';
}

/**
 * The canvas's Keyboard help, one line per item, as the keys are now.
 *
 * @param {object} options
 * @param {boolean} options.readOnly
 * @param {'mac'|'other'} [options.platform]
 * @returns {string[]}
 */
export function canvasHelp({ readOnly, platform = currentPlatform() }) {
  const text = (id) => shortcutLabel(id, { platform, text: true });
  const lines = [];

  if (readOnly) {
    lines.push(
      'This draft is read-only: you can select items but not change them.',
    );
  }

  lines.push(
    'Tab moves between the connections and nodes.',
    `${text('selection.press')} selects the focused item alone, or deselects ` +
      'it when it is the only selected item. ' +
      `${text('selection.toggle')} adds it to the selection or removes it.`,
    `${text('selection.clear')} clears the selection.`,
  );

  if (!readOnly) {
    lines.push(
      'Arrow keys move the selected nodes 10 pixels, and with Shift 1 pixel.',
      `${platform === 'mac' ? 'Option' : 'Alt'} and Shift with the arrow ` +
        'keys resize the selected group 10 pixels: Right and Down grow it, ' +
        'Left and Up shrink it.',
      `${text('edit.delete')} deletes the focused item, or the whole ` +
        'selection when the focused item is part of it.',
      sentence([phraseFor('edit.rename', platform)]),
    );
  }

  lines.push(
    sentence([
      phraseFor('view.zoomIn', platform),
      phraseFor('view.zoomOut', platform),
      phraseFor('view.fit', platform),
    ]),
    'These keys work on the connections, the nodes and the canvas itself, ' +
      'not on the zoom buttons or this help.',
  );

  const everywhere = sentence(
    (readOnly
      ? ['selection.all', 'edit.copy', 'goto.node', 'shortcuts.open']
      : [
          'selection.all',
          'edit.copy',
          'edit.paste',
          'edit.duplicate',
          'edit.undo',
          'edit.redo',
          'structure.group',
          'structure.ungroup',
          'goto.node',
          'shortcuts.open',
        ]
    ).map((id) => phraseFor(id, platform)),
  );

  if (everywhere) {
    lines.push(`Anywhere in the editor but a text field: ${everywhere}`);
  }

  const fields = sentence(
    (readOnly
      ? ['palette.open', 'view.focusMode']
      : ['palette.open', 'draft.save', 'view.focusMode']
    ).map((id) => phraseFor(id, platform)),
  );

  if (fields) {
    lines.push(`Anywhere in the editor, text fields too: ${fields}`);
  }

  if (!readOnly) {
    lines.push(
      'To add nodes or connect them without a pointer, use Add nodes and the Outline.',
    );
  }

  return lines.filter(Boolean);
}

/**
 * What the keys do on a focused node or connection, and a summary for the
 * canvas itself: their accessible descriptions.
 *
 * @param {object} options
 * @param {boolean} options.readOnly
 * @param {'mac'|'other'} [options.platform]
 * @returns {{node: string, edge: string, canvas: string}}
 */
export function canvasHints({ readOnly, platform = currentPlatform() }) {
  const first = (id) => {
    const [key] = commandKeys(id, { platform });

    return key ? keyText(key, platform) : '';
  };
  const select =
    `${first('selection.press')} selects or deselects, ` +
    `${first('selection.toggle')} adds to the selection, ` +
    `${first('selection.clear')} clears the selection.`;
  const canvas =
    `Tab moves between the connections and nodes, and ${first('selection.press')} ` +
    'selects the focused one. The Keyboard help below the canvas lists every key.';
  const remove = first('edit.delete');

  return readOnly
    ? { node: select, edge: select, canvas: `Read-only draft. ${canvas}` }
    : {
        node: `${select} Arrow keys move the selected nodes, ${remove} removes.`,
        edge: `${select} ${remove} removes. Edit the label in the Inspector.`,
        canvas,
      };
}

/**
 * The outline's keyboard hint, as the keys are now.
 *
 * @param {object} options
 * @param {boolean} options.readOnly
 * @param {'mac'|'other'} [options.platform]
 * @returns {string}
 */
export function outlineHint({ readOnly, platform = currentPlatform() }) {
  const text = (id) => shortcutLabel(id, { platform, text: true });
  const parts = [
    'Keyboard: Up and Down arrows, Home and End move between rows.',
    `${text('selection.press')} selects a row, or deselects it when it is ` +
      'the only one selected.',
    `${text('selection.toggle')} adds a row to the selection or takes it out.`,
  ];

  if (!readOnly) {
    parts.push(
      `${text('edit.rename')} renames.`,
      `${text('edit.delete')} removes the row, or the selection it is in.`,
    );
  }

  return parts.join(' ');
}
