// The Builder's commands: one registry of everything that the Builder can do
// by name or by key, in the editor and on the drafts landing. The key
// dispatcher below, the command palette, the shortcut sheet, the toolbar's
// tooltips and aria-keyshortcuts, and the hints of the canvas and the
// outline all read it. Thus none of them can promise a key that another does
// not handle.
//
// A command has these fields:
//   id        stable, 'area.action'. Customized keys are stored under it.
//   title     its name in the palette and the shortcut sheet. A title that
//             ends in '…' asks for more in a dialog before the command acts.
//   group     its heading in the palette and the sheet (GROUPS is the order)
//   aliases   lower-case single words that stand for its name ('export' for
//             Download). The palette and the sheet's filter match them as
//             words of the title, without highlighting them.
//   keywords  more words that the palette matches, without highlighting them
//   keys      default key specs (keymap.js), a list or {mac: [], other: []}
//   scope     where its keys work (SCOPES), one or a list
//   page      its keys also work on the rest of the Builder's page (the app
//             header), outside text fields and the app's dialogs
//   views     where it exists: ['editor'] when left out, ['landing'], or both
//   local     the focused control handles its keys itself (canvas items,
//             outline rows). Listed for reference, never dispatched.
//   fixed     its keys cannot be customized (local keys never can)
//   palette   false keeps it out of the command palette
//   offered(ctx)  false keeps it out of the palette for now (Share on a
//             draft that the user cannot share and that was not shared with
//             the user)
//   when(ctx) true, or the reason it cannot run now, in words
//   run(ctx, choice, picked)
//   choices(ctx, picked)  for a command that asks for more first: the
//             options of the next step, as choices (below). `steps` names
//             each step. run gets the last choice and every choice in order.
//   keyChoice(ctx)  the choice that its keys run it with, without asking
//             (the plain Device of Add device)
//   prefix    the palette query that searches the targets of the command
//   label(ctx)  the title in the current state ('Hide minimap')
//   detail(ctx) a second line for the palette
//
// A choice is {id, title, detail?, keywords?, icon?, disabled?, value?}.
// `disabled` is the reason it cannot be chosen, and `value` is what run
// needs.
//
// The context (createCommandContext) is {store, view}: the Builder store and
// the adapter that Builder.vue implements (VIEW_API). A run can add `source`
// ('key' from the dispatcher, 'palette' from the palette) and `additive`
// (Shift+Enter on a Go to node choice). A key press also adds `event`,
// `focus` (focusScope) and `item` (the focused node, connection or row).

import { nextTick, toRaw } from 'vue';

import { count } from './announce.js';
import { PALETTE, kindMeta, nodeIconKey } from './catalog.js';
import { DUPLICATE_NEEDS_NODES } from './clipboard.js';
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
  DRAWING_KINDS,
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  networkRefusal,
  nodeLabel,
  specInterfaces,
} from './model.js';
import { connectionList } from './outline.js';
import { MINIMAP_DEFAULT_WIDTH, minimapSize } from './panes.js';
import { pressSelection } from './selection.js';
import { templatesFull } from './templates.js';
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

// Where the keys of a scope work, by the result of focusScope:
//   - 'fields': everywhere in the view, text fields included (for keys that
//     mean nothing to a field)
//   - 'editor': everywhere except text fields
//   - 'canvas': on the canvas itself, a node or a connection
//   - 'outline': on an outline row.
export const SCOPES = {
  fields: ['field', 'editor', 'canvas', 'outline', 'landing'],
  editor: ['editor', 'canvas', 'outline', 'landing'],
  canvas: ['canvas'],
  outline: ['outline'],
};

// What the view adapter provides, for reference: Builder.vue implements
// it, and tests stub it.
export const VIEW_API = [
  'editing', // boolean: the editor is open (false: the drafts landing)
  'dialog', // string: the open dialog, '' for none
  // string: the landing's shown tab: mine, shared, published, templates or
  // others
  'draftsTab',
  'showMinimap', // boolean
  'showNodeNotes', // boolean: the canvas shows node notes (a setting)
  // {width, min, max}: the minimap's width and the widths it may take, in
  // pixels, or null without a canvas
  'minimapSize',
  // {hidden: {start, end}, stacked}: which side columns are hidden, and
  // whether a narrow window stacks the columns, which shows them all
  'panes',
  'canZoomIn', // boolean
  'canZoomOut', // boolean
  // boolean: after Fit, fitView goes back to the view from before it
  'fitRestores',
  'focusMode', // boolean: focus mode is on (see focusMode.js)
  // (name, options) publish, download, upload, scenario, share. download
  // takes {start}, the format to download at once: json, yaml, topology,
  // png, svg or gexf.
  // Also group-pattern, the Auto-group by name pattern dialog, connect, the
  // Add a connection dialog, and regroup, the Move to a group dialog.
  // Also template, the template editor: {mode: 'diagram-new'} for a new
  // template of the diagram, {mode: 'diagram-edit', id} for a template that
  // it has, {mode: 'library-new'} for a new template of the user's library
  // and {mode: 'library-edit', id} for a template of that library. Also
  // collection, a collection of the library: {id} to edit one, {templateIds}
  // for a new one that holds them.
  'openDialog',
  'openPalette', // ({query, command}) the command palette: dialog 'commands'
  'openShortcuts', // () the shortcut sheet: dialog 'shortcuts'
  'openSettings', // () the Builder settings: dialog 'settings'
  'openHistory', // () loads the history, then opens its dialog
  'openLanding', // (name) the landing's import or upload dialog
  'startBlank', // () a new blank draft
  'openDraft', // (item) a listed draft or published diagram
  // () a new draft of the open diagram whose included nodes are its own
  'combineIncluded',
  // () the experiment the open diagram was published with, in this tab,
  // once leaving the draft is settled
  'openExperiment',
  'closeEditor', // () back to the drafts
  // () back to the drafts, as closeEditor, then their Node Templates tab
  'openTemplateLibrary',
  'showDraftsTab', // (id) mine, shared, published, templates or others
  'toggleMinimap', // ()
  // () shows or hides node notes, keeps that as the setting, and says which
  'toggleNodeNotes',
  'resizeMinimap', // (width) in pixels, and says the new size
  'togglePane', // (side) hides or shows a side column: start or end
  'toggleFocusMode', // () turns focus mode on or off, and says which
  // () the view as a new session shows it: default column widths, the
  // minimap, the starting zoom, panels scrolled to the top, sections closed
  'resetView',
  'setTheme', // (theme) system, light or dark
  'zoomIn', // ()
  'zoomOut', // ()
  'fitView', // () fits the diagram, or goes back (fitRestores)
  'focusCanvas', // ()
  'focusOutline', // ()
  'focusInspector', // ({field}) its first field when field is true
  'showNode', // (id) focuses a node on the canvas and pans it into view
  // () the part of the canvas in view, in flow coordinates: {x, y, width,
  // height}, or null
  'visibleArea',
  'revealNode', // (id or ids) brings nodes into view, leaving focus as it is
  // () before a save: commits the focused text field, as its change event
  // would, and settles the Inspector's unapplied edits. Resolves to the
  // reason that some stay unapplied ('1 field needs attention'), or ''.
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

// How many of the diagram's devices come from included topologies.
function includedCount(store) {
  return store.summary?.included || 0;
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

// Delete and Ungroup remove what the user was working on. Thus focus moves
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
// for connections, and where focus goes while the outline is hidden.
function focusRowOrCanvas(row) {
  row?.focus();

  if (!row || document.activeElement !== row) {
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

// The part of the view that holds an element, to find a place for focus when
// the element is gone: 'canvas' (the canvas, its nodes and connections, not
// its zoom buttons), 'outline', 'inspector', or '' elsewhere.
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

// The heading of each region takes focus in its place. The canvas section
// is its own heading. On the landing, the shown tab takes focus.
const REGION_FOCUS = {
  canvas: '#builder-canvas',
  outline: '#outline-title',
  inspector: '#inspector-title',
};

/**
 * Records what has focus before a command runs, so that focus can go back to
 * the same thing if the command re-renders or removes it (see keepFocus).
 * This is an outline row or a canvas node or connection, by its id, or else
 * an element by its id or test id. Only focus inside the Builder is recorded.
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

/**
 * Whether focus has fallen to the page: nothing has it, <body> has it, or
 * the element that has it has left the page.
 *
 * @returns {boolean}
 */
export function focusLost() {
  const active = document.activeElement;

  return !active || active === document.body || !active.isConnected;
}

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

/**
 * Focuses an element as soon as it can take focus. Vue Flow shows a node
 * that it just rendered only after it measures it, a frame or two later. A
 * hidden node cannot take focus, so this function tries again on the next
 * frames. It stops, as done, when focus goes to any other element.
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
 * an element that it removed (WCAG 2.4.3). Focus goes to the same item,
 * re-rendered (a row that moved into a group, a node that an undo put back).
 * If not, it goes to the heading of its region, the canvas, or on the
 * landing to the shown tab. Vue Flow renders a tick after the store, so this
 * function waits two ticks.
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
 * Ungroups the selected group, then focuses one of its members: its outline
 * row, or with `canvas` (the key came from the canvas) its node there, so
 * the arrow keys continue to move between nodes.
 *
 * @param {object} store
 * @param {object} [options]
 * @param {boolean} [options.canvas]
 */
export async function ungroupSelection(store, { canvas = false } = {}) {
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
  const member = rows.find((row) => members.has(rowNodeId(row)));

  if (canvas && member) {
    // Vue Flow renders the members a tick after the outline.
    await nextTick();

    if (await focusSoon(canvasItem('nodes', rowNodeId(member)))) {
      return;
    }
  }

  // From the canvas, a group without members leaves focus on the canvas.
  focusRowOrCanvas(
    member || (canvas ? null : rows[Math.min(index, rows.length - 1)]),
  );
}

/**
 * Groups the selected nodes, then focuses the new group: its outline row,
 * or with `canvas` (the key came from the canvas) its node there. Grouping
 * moves the rows and nodes of the members into the group, which re-renders
 * the one that had focus.
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
 * Saves what the user sees now, as the Save now key and the palette do.
 * First, it commits the focused text field, as its change event would (the
 * Diagram name commits only on change). Then it settles the Inspector's
 * unapplied edits (view.settleEdits). Valid edits are merged into the
 * current state of their element and applied, as Apply would, even while a
 * redo is pending. Edits that it cannot apply stay in the Inspector, and the
 * outcome says so. When nothing is left to save, nothing is sent and no
 * snapshot is made, and the outcome says that.
 *
 * @param {object} ctx createCommandContext's
 * @returns {Promise<object>} the save state
 */
export async function saveDraft({ store, view }) {
  // The queue's work when the key was pressed, and whether that work ended
  // before the edits are settled. Until then, an edit can still be on its
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

// Items by id. The first item wins, as with find().
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
 * Every node, as a choice for Go to node. In addition to the name, these
 * find a node: its hostname, label, image, networks and VLAN aliases, and
 * the IP and MAC addresses of its interfaces. They are its `fields`, each
 * {label, value}, and also its keywords as plain words.
 *
 * The document is read in one pass, whatever its size. It is read as the
 * plain object. The store replaces its document on every edit and does not
 * change it, so Vue does not need to track every field of every node.
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

// Selects only a node, or with `additive` adds it to the selection or removes
// it, as Shift+Enter does on the canvas. The plain choice then shows it. The
// palette stays open for Shift+Enter, and says the change on its own status
// line. The live region waits for the palette to close, and by then the
// message can be out of date.
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

// A topology read from the Builder file that it names is listed with the
// published diagrams, by the handle of its row as an id, and says so.
function draftChoices(store) {
  const name = (item) => item.name || item.title || item.target || item.id;
  const from = (items, where) =>
    (items || []).map((item) => ({
      id: `${where}:${item.owner || ''}:${item.id}`,
      title: name(item),
      detail: [
        where,
        item.owner && `Owner: ${item.owner}`,
        item.source === 'file' && `File: ${item.path}`,
        item.description,
      ]
        .filter(Boolean)
        .join(' · '),
      keywords: [item.target, item.owner, item.path].filter(Boolean),
      value: item,
    }));

  return [
    ...from(store.drafts?.mine, 'My Drafts'),
    ...from(store.drafts?.shared, 'Shared Drafts'),
    ...from(store.documents, 'Published Diagrams'),
    ...from(store.drafts?.others, "Other users' drafts"),
  ];
}

// --- the registry ----------------------------------------------------------------

const BOTH = ['editor', 'landing'];
const LANDING = ['landing'];

// `width` gives the size's width from the view's minimapSize.
function minimapSizeCommand(id, title, width) {
  return {
    id: `view.minimapSize.${id}`,
    title: `Minimap size: ${title}`,
    group: 'View',
    keywords: ['overview', 'map', 'resize', 'larger', 'smaller'],
    when: ({ view }) => {
      if (!view.showMinimap || !view.minimapSize) {
        return 'The minimap is hidden.';
      }

      return view.minimapSize.width === width(view.minimapSize)
        ? 'This is the current size.'
        : true;
    },
    detail: ({ view }) => {
      const size = view.minimapSize && minimapSize(width(view.minimapSize));

      return size ? `${size.width} by ${size.height} pixels` : '';
    },
    run: ({ view }) => view.resizeMinimap(width(view.minimapSize)),
  };
}

function pane(side, name, keywords) {
  return {
    id: `view.pane.${side}`,
    title: `Show or hide ${name}`,
    group: 'View',
    keywords: ['column', 'panel', 'sidebar', 'collapse', 'expand', ...keywords],
    label: ({ view }) =>
      `${view.panes?.hidden?.[side] ? 'Show' : 'Hide'} ${name}`,
    when: ({ view }) =>
      !view.panes?.stacked || 'The window is too narrow to hide columns.',
    run: ({ view }) => view.togglePane(side),
  };
}

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

// Lays out the diagram with one layout, which the draft then keeps (the
// toolbar's layout menu has the same choices). The draft's own layout is
// marked. At Default, no layout is marked.
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

// The default keys of the Auto-group commands, by strategy: the first rule
// of the menu has some, and the user may give the others theirs.
const AUTO_GROUP_KEYS = { network: ['Alt+Shift+G'] };

// Auto-group with one strategy (the toolbar's Auto-group menu has the same
// choices). One that asks for its pattern first opens the dialog that takes
// it, and its title ends in an ellipsis.
function autoGroupChoice({ id, label, summary, asks }) {
  return {
    id: `structure.autoGroup.${id}`,
    title: `Auto-group ${label.toLowerCase()}${asks ? '…' : ''}`,
    group: 'Structure',
    keywords: [
      'auto group',
      'cluster',
      'organize',
      ...(asks ? ['regex', 'regexp', 'regular expression'] : []),
    ],
    ...(AUTO_GROUP_KEYS[id] && { keys: AUTO_GROUP_KEYS[id], scope: 'editor' }),
    when: editable,
    detail: ({ store }) =>
      store.selection.nodes.length
        ? `${summary} · Selected nodes only`
        : summary,
    run: asks
      ? ({ view }) => view.openDialog('group-pattern')
      : ({ store }) => store.autoGroup(id),
  };
}

// `empty(ctx)`, for a tab shown only while it lists something, is the
// reason it cannot be shown when it lists nothing.
function landingTab(id, title, empty = () => '') {
  return {
    id: `drafts.tab.${id}`,
    title: `Show ${title}`,
    group: 'Drafts',
    keywords: ['tab', 'list'],
    views: LANDING,
    when: (ctx) =>
      empty(ctx) ||
      (ctx.view.draftsTab === id ? 'This tab is already shown.' : true),
    run: ({ view }) => view.showDraftsTab(id),
  };
}

// The word the Download commands also answer to in the palette.
const DOWNLOAD_ALIASES = ['export'];

// Downloads the diagram in one format. The Download dialog opens and starts
// that download at once. Thus its result, its errors and what stops a
// topology from being published show where the dialog's own buttons show
// them. It works in a read-only draft, as Download… does.
function downloadAs(format, name, detail, keywords) {
  return {
    id: `draft.download.${format}`,
    title: `Download ${name}`,
    group: 'Draft',
    aliases: DOWNLOAD_ALIASES,
    keywords: ['save', 'file', ...keywords],
    detail: () => detail,
    run: ({ view }) => view.openDialog('download', { start: format }),
  };
}

// Share: offered to the owner, who can share. A draft shared with the user
// says who can share. It is not offered for the draft of any other user, or
// for a diagram with no draft.
function shareable({ store }) {
  if (store.canShare) {
    return true;
  }

  return store.sharedBy
    ? `Only ${store.sharedBy} can change who has access.`
    : 'Only the owner can share this draft.';
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
    run: ({ view }) => view.openShortcuts(),
  },
  {
    // The header's Settings.
    id: 'settings.open',
    title: 'Settings…',
    group: 'General',
    keywords: [
      'preferences',
      'options',
      'layout algorithm',
      'default layout',
      'theme',
      'minimap',
      'zoom',
      'motion',
      'single-key',
    ],
    keys: ['Alt+Shift+S'],
    scope: 'editor',
    page: true,
    views: BOTH,
    run: ({ view }) => view.openSettings(),
  },
  {
    id: 'help.open',
    title: 'Builder help',
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
    // A connection is copied with both of its nodes, so connections alone
    // have nothing to duplicate.
    when: (ctx) => {
      if (editable(ctx) !== true) {
        return READ_ONLY;
      }

      if (!hasSelection(ctx.store)) {
        return 'Nothing is selected to duplicate.';
      }

      return ctx.store.selection.nodes.length > 0 || DUPLICATE_NEEDS_NODES;
    },
    detail: () =>
      'Pastes a copy of the selected nodes beside them, leaving the clipboard as it is',
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
    // An outline row renames in place (BuilderOutline handles F2 there). On
    // the canvas and from the palette, the Inspector's first field (the name
    // or the connection's label) takes focus.
    id: 'edit.rename',
    title: 'Rename',
    group: 'Edit',
    keywords: ['name', 'hostname', 'label'],
    keys: ['F2'],
    scope: ['canvas', 'outline'],
    fixed: true,
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
    // The canvas is one Tab stop. These commands move focus inside it
    // (BuilderCanvas.vue).
    id: 'canvas.move',
    title: 'Move to the nearest node that way',
    group: 'Selection',
    keys: ['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'canvas.connections',
    title: 'Move through the focused node’s connections',
    group: 'Selection',
    keys: ['PageDown', 'PageUp'],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'selection.nudge',
    title: 'Move the selected nodes 10 pixels',
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
    // Right and Down grow the group, note, shape or icon from its bottom
    // right corner, Left and Up shrink it (BuilderCanvas.vue).
    id: 'selection.resize',
    title: 'Resize the selected group, note, shape or icon 10 pixels',
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
    when: (ctx) =>
      editable(ctx) === true
        ? Boolean(selectedGroup(ctx.store)) || 'Select a group first.'
        : READ_ONLY,
    run: ({ store }) =>
      ungroupSelection(store, {
        canvas: regionOf(globalThis.document?.activeElement) === 'canvas',
      }),
  },
  ...GROUPING_STRATEGIES.map(autoGroupChoice),
  {
    // Runs the draft's layout again, or for a draft with no layout, the
    // Settings default. Each layout has its own command.
    id: 'structure.layout',
    title: 'Auto layout',
    group: 'Structure',
    keywords: ['arrange', 'tidy', 'organize'],
    keys: ['Alt+Shift+L'],
    scope: 'editor',
    when: editable,
    detail: ({ store }) =>
      `Arrange the nodes with ${layoutAlgorithm(store.layoutToRun)?.label || 'the current layout'}`,
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
    // The outline lists only nodes, so here the user can find a connection
    // by name and remove it without a pointer. Delete on a canvas connection
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
  {
    // The toolbar's Move to group: a dialog that moves a node into a group,
    // or out of the one it is in.
    id: 'dialog.regroup',
    title: 'Move to a group…',
    group: 'Structure',
    keywords: ['group', 'regroup', 'member', 'container', 'parent'],
    when: editable,
    detail: () => 'Into a group, or out of one',
    run: ({ view }) => view.openDialog('regroup'),
  },

  // --- Add
  {
    // Its key, on the canvas, adds the plain Device without asking, as the
    // palette's Add Device button does, then focuses it.
    id: 'add.device',
    title: 'Add device',
    group: 'Add',
    keywords: ['vm', 'host', 'node', 'template'],
    keys: ['N'],
    scope: 'canvas',
    steps: ['Template'],
    keyChoice: () => ({ id: 'device', title: 'Device', value: '' }),
    when: editable,
    detail: () => 'Choose a template',
    // The plain Device, then every template of the palette, in its groups'
    // order (see paletteTemplateGroups in templates.js).
    choices: ({ store }) => [
      {
        id: 'device',
        title: 'Device',
        detail: PALETTE.find((item) => item.kind === 'device').hint,
        icon: 'server',
        value: '',
      },
      ...store.paletteTemplateGroups.flatMap((group) =>
        group.entries.map((entry) => ({
          id: entry.key,
          title: entry.name,
          detail: `${group.label} · ${entry.image || 'no image'}`,
          icon: entry.iconKey,
          keywords: [entry.description].filter(Boolean),
          value: entry.key,
        })),
      ),
    ],
    run: (ctx, choice) => {
      const node = addInView(
        ctx,
        paletteNode(ctx.store, 'device', choice?.value),
      );

      // From the canvas key, focus moves to the new device, so the next keys
      // act on it. The palette leaves focus where it is.
      return node && ctx.source === 'key'
        ? ctx.view.showNode?.(node.id)
        : undefined;
    },
  },
  // Every other entry of Add nodes, by its id: add.switch, add.note,
  // add.group, and the drawings add.rectangle, add.circle, add.icon and
  // add.line.
  ...PALETTE.filter((item) => item.kind !== 'device').map((item) => ({
    id: `add.${item.id}`,
    title: `Add ${item.label.toLowerCase()}`,
    group: 'Add',
    ...(DRAWING_KINDS.includes(item.kind)
      ? { keywords: ['draw', 'drawing', item.kind] }
      : {}),
    when: editable,
    detail: () => item.hint,
    run: (ctx) => addNode(ctx, { kind: item.kind, ...item.options }),
  })),
  {
    // The toolbar's Add connection: a dialog that connects a device to a
    // switch, through a free interface or a new one. The selection fills its
    // fields. It comes after the node commands, so a search for "add" that
    // matches all of them equally lists the node commands first.
    id: 'dialog.connect',
    title: 'Add a connection…',
    group: 'Add',
    keywords: ['connect', 'link', 'cable', 'interface', 'switch', 'network'],
    when: editable,
    detail: () => 'A device, its interface and a switch',
    run: ({ view }) => view.openDialog('connect'),
  },
  {
    // The palette's "+" beside Device templates: the template editor, on
    // the selected device when one device is selected, else on a plain one.
    id: 'templates.new',
    title: 'New device template',
    group: 'Add',
    keywords: ['template', 'reuse'],
    when: (ctx) =>
      editable(ctx) === true ? templatesFull(ctx.store.doc) || true : READ_ONLY,
    detail: ({ store }) =>
      store.selectedNode?.kind === 'device'
        ? `Saved in this diagram, from ${nodeLabel(store.selectedNode)}`
        : 'Saved in this diagram',
    run: ({ view }) => view.openDialog('template', { mode: 'diagram-new' }),
  },
  {
    // The palette's library button, beside "+": the drafts page's Node
    // Templates tab, where the user's library is managed. Leaving the
    // draft goes as Back to drafts does.
    id: 'templates.library',
    title: 'Open Node Templates library',
    group: 'Go to',
    keywords: ['template', 'library', 'collection'],
    detail: () => 'On the drafts page',
    run: ({ view }) => view.openTemplateLibrary(),
  },

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
    when: ({ view }) =>
      view.canZoomOut !== false || 'The diagram is at its smallest zoom.',
    run: ({ view }) => view.zoomOut(),
  },
  {
    // The zoom controls' Fit button, which becomes Restore previous view
    // after Fit, until the view changes some other way.
    id: 'view.fit',
    title: 'Fit diagram to view',
    group: 'View',
    keywords: ['zoom', 'whole', 'all', 'fit', 'restore', 'previous', 'back'],
    keys: ['Shift+1'],
    scope: 'canvas',
    label: ({ view }) =>
      view.fitRestores ? 'Restore previous view' : 'Fit diagram to view',
    detail: ({ view }) =>
      view.fitRestores ? 'The zoom and position from before Fit' : '',
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
    // The Settings dialog's Show node notes: the notes of devices and
    // switches, below them on the canvas.
    id: 'view.nodeNotes',
    title: 'Show or hide node notes',
    group: 'View',
    keywords: ['notes', 'comments', 'annotations', 'remarks'],
    label: ({ view }) =>
      view.showNodeNotes ? 'Hide node notes' : 'Show node notes',
    run: ({ view }) => view.toggleNodeNotes(),
  },
  // What the minimap's handle does by dragging or keys, for a pointer that
  // does not drag (WCAG 2.5.7).
  minimapSizeCommand('smallest', 'Smallest', ({ min }) => min),
  minimapSizeCommand('default', 'Default', () => MINIMAP_DEFAULT_WIDTH),
  minimapSizeCommand('large', 'Large', ({ max }) =>
    Math.round((MINIMAP_DEFAULT_WIDTH + max) / 2),
  ),
  minimapSizeCommand('largest', 'Largest', ({ max }) => max),
  // The Hide and Show toggles of the side columns.
  pane('start', 'Add nodes and Outline', ['palette', 'outline']),
  pane('end', 'Inspector', ['properties']),
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
    // The Focus mode button of the headers, which becomes Exit focus mode, in
    // the editor and on the drafts. The same keys leave focus mode. The
    // browser's Escape leaves only full screen (see focusMode.js). The keys
    // also work in text fields, because they type nothing.
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
    views: BOTH,
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
    when: editable,
    run: (ctx) => saveDraft(ctx),
  },
  {
    id: 'draft.history',
    title: 'Draft History…',
    group: 'Draft',
    keywords: ['snapshot', 'restore', 'version'],
    run: ({ view }) => view.openHistory(),
  },
  {
    id: 'draft.download',
    title: 'Download…',
    group: 'Draft',
    aliases: DOWNLOAD_ALIASES,
    keywords: [
      'save',
      'file',
      'copy',
      'image',
      'json',
      'yaml',
      'png',
      'svg',
      'gexf',
      'gephi',
      'graph',
      'topology',
    ],
    detail: () =>
      'Builder JSON or YAML, Topology YAML, PNG, SVG or Gephi (GEXF)',
    run: ({ view }) => view.openDialog('download'),
  },
  downloadAs('json', 'Builder JSON', 'The whole Builder document', [
    'document',
  ]),
  downloadAs('yaml', 'Builder YAML', 'The whole Builder document, as YAML', [
    'document',
  ]),
  downloadAs(
    'topology',
    'Topology YAML',
    'The phenix Topology config Publish would write',
    ['config', 'phenix'],
  ),
  downloadAs('png', 'PNG', 'A picture of the whole diagram', [
    'image',
    'picture',
  ]),
  downloadAs('svg', 'SVG', 'A picture of the whole diagram, as SVG', [
    'image',
    'picture',
    'vector',
  ]),
  downloadAs(
    'gexf',
    'Gephi (GEXF)',
    'The network as a graph to analyze in Gephi',
    ['graph', 'analyze'],
  ),
  {
    // The toolbar's Upload. The landing's Upload is drafts.upload. It also
    // converts a legacy Builder diagram, so "legacy" finds it.
    id: 'draft.upload',
    title: 'Upload…',
    group: 'Draft',
    keywords: ['open', 'file', 'import', 'legacy'],
    detail: () => 'A Builder document from a file',
    when: creatable,
    run: ({ view }) => view.openDialog('upload'),
  },
  {
    id: 'draft.scenario',
    title: 'Scenarios…',
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
  {
    // Offered only while the diagram shows nodes of included topologies.
    // It makes a draft, so a role that cannot create one is told why not.
    id: 'draft.combineIncluded',
    title: 'Combine included nodes into a new draft',
    group: 'Draft',
    keywords: [
      'include',
      'includeTopologies',
      'flatten',
      'merge',
      'read only',
      'editable',
      'unlock',
    ],
    detail: ({ store }) =>
      `${count(includedCount(store), 'included node')} ${
        includedCount(store) === 1 ? 'becomes' : 'become'
      } editable in a copy of this diagram`,
    offered: ({ store }) => includedCount(store) > 0,
    when: creatable,
    run: ({ view }) => view.combineIncluded(),
  },
  {
    id: 'draft.share',
    title: 'Share…',
    group: 'Draft',
    aliases: ['send'],
    keywords: ['access', 'people', 'users', 'collaborate', 'permissions'],
    detail: () => 'Who can view or edit this draft',
    offered: ({ store }) => Boolean(store.canShare || store.sharedBy),
    when: shareable,
    run: ({ view }) => view.openDialog('share'),
  },
  {
    // The toolbar's Exp: offered only while the diagram's publication has
    // an experiment (the store's experimentName).
    id: 'draft.experiment',
    title: 'Open experiment',
    group: 'Draft',
    keywords: ['exp', 'run', 'published'],
    detail: ({ store }) => store.experimentName,
    offered: ({ store }) => Boolean(store.experimentName),
    when: ({ store }) =>
      Boolean(store.experimentName) ||
      'This diagram was not published with an experiment.',
    run: ({ view }) => view.openExperiment(),
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
    keywords: ['topology', 'experiment', 'convert', 'generate', 'legacy'],
    views: LANDING,
    detail: () => 'From a topology or experiment config on the server',
    when: creatable,
    run: ({ view }) => view.openLanding('import'),
  },
  {
    // The landing's Upload, listed with the other ways to start a draft.
    id: 'drafts.upload',
    title: 'Upload…',
    group: 'Drafts',
    keywords: ['open', 'file', 'import', 'new', 'legacy'],
    views: LANDING,
    detail: () => 'A Builder document from a file, as a new draft',
    when: creatable,
    run: ({ view }) => view.openLanding('upload'),
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
  {
    ...landingTab('templates', 'Node Templates'),
    keywords: ['tab', 'list', 'library', 'collection'],
  },
  landingTab('others', "Other users' drafts", ({ store }) =>
    (store.drafts?.others || []).length ||
    (store.damagedDrafts?.others || []).length
      ? ''
      : "No other users' drafts are listed.",
  ),
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
    (command) =>
      command.palette !== false &&
      viewsOf(command).includes(view) &&
      command.offered?.(ctx) !== false,
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
 * instead: at its prefix ('@') or at its first step. When the command
 * re-renders or removes the focused element, focus goes back to the same
 * item, or near it (keepFocus).
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

  // A key press runs a command with the choice it makes for keys (Add
  // device's plain Device), if it makes one.
  const chosen =
    choice === undefined && ctx.source === 'key'
      ? command.keyChoice?.(ctx)
      : choice;

  if (command.choices && chosen === undefined) {
    ctx.view.openPalette(
      command.prefix ? { query: command.prefix } : { command: command.id },
    );

    return true;
  }

  const focus = noteFocus();
  const running = command.run?.(
    ctx,
    chosen,
    picked || (chosen === undefined ? [] : [chosen]),
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
 * The keys that a command answers to now: the user's keys or the defaults,
 * without the one-character keys while the single-key switch is off.
 * Reactive.
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
 * Whether a command's keys work in text fields, where the field must get a
 * key that types a character (see dispatchKeydown).
 *
 * @param {string|object} idOrCommand
 * @returns {boolean}
 */
export function worksInTextFields(idOrCommand) {
  return reachOf(getCommand(idOrCommand) || {}).has('field');
}

/**
 * Whether a command can take a letter alone as a key (see keyRefusal): a
 * command whose keys work only on the canvas.
 *
 * @param {string|object} idOrCommand
 * @returns {boolean}
 */
export function takesLetters(idOrCommand) {
  const reach = [...reachOf(getCommand(idOrCommand) || {})];

  return reach.length > 0 && reach.every((focus) => focus === 'canvas');
}

/**
 * The command whose keys work in text fields (Command palette, Save now)
 * for a key press in a text field, if any, as dispatchKeydown finds it. A
 * field that keeps its other keys (the outline's rename field) lets these
 * keys go to the dispatcher.
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
 * The other commands that a key would clash with if a command took it: the
 * commands that answer to it where the command would also answer.
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

// Input types that are not text: a change event sent to one reads as a new
// choice. A color input commits on its own change (see
// InspectorColorControl).
const NOT_TYPED = [
  'checkbox',
  'radio',
  'button',
  'submit',
  'reset',
  'file',
  'color',
];

// Whether a field holds typed text that is read only on change, so that
// sending it a change event commits what it holds and nothing else.
export function isTextEntry(field) {
  return (
    Boolean(field) &&
    !field.readOnly &&
    (field.tagName === 'TEXTAREA' ||
      (field.tagName === 'INPUT' && !NOT_TYPED.includes(field.type)))
  );
}
// A checkbox takes no typed text: Space is the one key it reads, and no
// shortcut can be Space (see keyRefusal).
const CHECKBOX = 'input[type="checkbox"]';
const CANVAS = '.builder-canvas';
const CANVAS_ITEM = '.vue-flow__node, .vue-flow__edge';
const OUTLINE_ROW = '[data-testid="builder-outline"] .builder-outline__item';

// The app's own dialogs (Buefy modals) and the Builder's.
const APP_DIALOG =
  'dialog, [role="dialog"], [role="alertdialog"], [aria-modal="true"]';

/**
 * Where a key press is, for the scopes:
 *   - 'dialog': an open dialog keeps its own keys
 *   - 'field'
 *   - 'landing': the drafts landing, including the checkboxes that select
 *     its cards
 *   - 'canvas': the canvas itself, a node or a connection, but not the zoom
 *     buttons
 *   - 'outline': an outline row
 *   - 'editor': anywhere else in the editor, and the page itself when the
 *     focused control was just removed
 *   - 'page': outside the Builder, on the rest of its page, such as the app
 *     header link that brought the user here
 *   - 'outside': a text field or a dialog of the app, outside the Builder.
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

  // In the editor, a checkbox is a field of the Inspector's form, whose
  // edits wait for Apply. Thus the editing keys ignore it, as they ignore the
  // other fields of the form. On the drafts landing, it only selects a card.
  if (target.matches?.(TYPING) && (editing || !target.matches(CHECKBOX))) {
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
 * Runs the command for a key press, if any. This is the one key handler for
 * the whole Builder view. It is bound once, to the window, so it also gets
 * presses on the page itself. Outside the Builder, only the commands marked
 * `page` answer (the keys of the palette and the shortcut sheet). These
 * presses go to the control, not to a command:
 *   - a press that a control handled itself (it called preventDefault)
 *   - a press during IME composition
 *   - in a text field, every key that types a character (typesCharacter:
 *     on macOS, ⌥ keys too), whatever command the user gave it to.
 * A matched key is always taken from the browser, even when its command
 * cannot run. The reason is announced instead. Copy leaves selected text to
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

/**
 * What the keys do on a focused node or connection, and a summary for the
 * canvas itself, as their accessible descriptions.
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
  const next = first('canvas.connections');
  const node = `Arrow keys move between nodes, ${next} through this node’s connections. ${select}`;
  const edge = `Arrow keys move to the nodes, ${next} to the next connection. ${select}`;
  // The advice for screen readers is here, with the canvas's own keys. In
  // browse mode, the arrow keys move the virtual cursor, not the focus (see
  // BuilderCanvas.vue).
  const sheet = shortcutLabel('shortcuts.open', { platform, text: true });
  const canvas =
    `Arrow keys move to the nodes, and ${first('selection.press')} ` +
    'selects the focused one. With a screen reader, turn on its focus mode ' +
    '(forms mode in JAWS) for these keys, or use the Outline. ' +
    (sheet
      ? `${sheet} lists every shortcut.`
      : 'Shortcuts in the header lists every shortcut.');
  const remove = first('edit.delete');

  return readOnly
    ? { node, edge, canvas: `Read-only draft. ${canvas}` }
    : {
        node: `${node} Shift and an arrow key move the selected nodes, ${remove} removes.`,
        edge: `${edge} ${remove} removes. Edit the label in the Inspector.`,
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
