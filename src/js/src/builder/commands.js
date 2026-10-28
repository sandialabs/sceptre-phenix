// The Builder's command registry. The key dispatcher, the palette, the
// shortcut sheet, the tooltips and the canvas hints all read it, so none of
// them can promise a key that another does not handle. Customized keys are
// stored under a command's id ('area.action'). A choice's `disabled` tells
// why the user cannot choose it, and its `value` is what run needs.

import { nextTick, toRaw } from 'vue';

import { interfacesByNetwork, parseSubnet } from './addressing.js';
import { count, listOf } from './announce.js';
import { PALETTE, kindMeta, nodeIconKey } from './catalog.js';
import { DUPLICATE_NEEDS_NODES } from './clipboard.js';
import { GROUPING_STRATEGIES } from './grouping.js';
import {
  collectionChoices,
  editorTemplateChoices,
  iconChoices,
  libraryTemplateChoices,
} from './librarySearch.js';
import { foldKey } from './goCase.js';
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
  LAYER_KINDS,
  effectiveLayers,
  effectiveNodeLayer,
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  layerCounts,
  layerName,
  layerTitles,
  networkRefusal,
  nodeLabel,
  placeholderLabel,
  removalRefusal,
  someNodeOnLayer,
  specInterfaces,
} from './model.js';
import { connectionList } from './diagramText.js';
import { MINIMAP_DEFAULT_WIDTH, minimapSize } from './panes.js';
import { driveImages } from './resources.js';
import { pressSelection } from './selection.js';
import { templatesFull } from './templates.js';
import { isPlainObject } from './values.js';
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
//   - 'canvas': on the canvas itself, a node or a connection.
export const SCOPES = {
  fields: ['field', 'editor', 'canvas', 'landing'],
  editor: ['editor', 'canvas', 'landing'],
  canvas: ['canvas'],
};

// What the view adapter provides, for reference: Builder.vue implements
// it, and tests stub it.
export const VIEW_API = [
  'editing', // boolean: the editor is open (false: the drafts landing)
  // string: the landing's shown tab: drafts, published, templates, layers
  // or icons
  'draftsTab',
  'showMinimap', // boolean
  'showNodeIcons', // boolean: the canvas shows node icons (a setting)
  // boolean: devices and groups show their descriptions (a setting)
  'showNodeDescriptions',
  // boolean: the canvas shows the connections between nodes (a setting)
  'showConnections',
  // string: the size every node icon is drawn at on the canvas, or off for
  // the diagram's sizes (the setting nodeIconSize)
  'nodeIconSize',
  // boolean: drags, drops and new nodes go to the grid (a setting)
  'snapToGrid',
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
  // (name, options): publish, download, upload, scenario, group-pattern,
  // connect, regroup, template, collection or node-editor. views/Builder.vue
  // reads each dialog's options.
  'openDialog',
  'openPalette', // ({query, command}) the command palette: dialog 'commands'
  'openShortcuts', // () the shortcut sheet: dialog 'shortcuts'
  // () the Builder settings: dialog 'settings'
  'openSettings',
  // () the layers editor of the open diagram: dialog 'layers'
  'openLayers',
  'openHistory', // () loads the history, then opens its dialog
  'openLanding', // (name) the landing's import or upload dialog
  'startBlank', // () a new blank draft
  'openDraft', // (item) a listed draft or published diagram
  // (item) a new draft with a copy of a listed draft, or of the open draft
  // without one, which then opens
  'duplicateDraft',
  // () a new draft of the open diagram whose included nodes are its own
  'combineIncluded',
  // () the experiment the open diagram was published with, in this tab,
  // once leaving the draft is settled
  'openExperiment',
  'closeEditor', // () back to the drafts
  // (show) back to the drafts, as closeEditor, then their Node Templates
  // tab, showing the list that `show` names (a value of its Show field), if
  // one is given
  'openTemplateLibrary',
  // (name) back to the drafts, as closeEditor, then their Icons tab, with
  // the preview of the icon of that name, if one is given
  'openIcons',
  // (id) drafts, published, templates, layers or icons
  'showDraftsTab',
  'toggleMinimap', // ()
  // () shows or hides node icons, keeps that as the setting, and says which
  'toggleNodeIcons',
  // () shows or hides node descriptions, keeps that as the setting, and says
  // which
  'toggleNodeDescriptions',
  // () shows or hides the connections, keeps that as the setting, and says
  // which
  'toggleConnections',
  // (size) off, small, medium or large: keeps it as the setting
  // nodeIconSize, and says what the canvas now draws
  'setNodeIconSize',
  // () turns Snap to grid on or off, keeps that as the setting, and says
  // which
  'toggleSnapToGrid',
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
  'focusInspector', // ({field}) its first field when field is true
  // (id, {zoomIn}) focuses a node on the canvas and pans it into view. With
  // zoomIn (Go to), a zoom too small to read zooms in on it.
  'showNode',
  // () the part of the canvas in view, in flow coordinates: {x, y, width,
  // height}, or null
  'visibleArea',
  // (id or ids, {zoomIn}) brings nodes into view, leaving focus as it is.
  // With zoomIn, as showNode.
  'revealNode',
  // () before a save: commits the focused text field, as its change event
  // would, and settles the Inspector's unapplied edits. Resolves to the
  // reason that some stay unapplied ('1 field needs attention'), or ''.
  'settleEdits',
  // object[]: the icons of the icon library (iconLibrary.js), as listed
  'icons',
];

export const READ_ONLY = 'This draft is read-only.';

/**
 * The context that commands run with: the Builder store and the view adapter
 * (VIEW_API).
 */
export function createCommandContext({ store, view }) {
  return { store, view };
}

// --- conditions --------------------------------------------------------------

function editable({ store }) {
  return store.readOnly ? READ_ONLY : true;
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

// The nodes Open Node Editor offers to choose: the devices and switches.
function editableNodes(doc) {
  return (doc.nodes || []).filter(
    (node) => node.kind === 'device' || node.kind === 'switch',
  );
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

// Why Open Node Editor cannot run: no node is selected (see editTarget),
// and the diagram has no device or switch to choose.
export const SELECT_ONE_NODE = 'Select one node.';

// The node Open Node Editor edits: the focused node on the canvas, else the
// one node selected with no connections. Lines, drawings, groups and notes
// count, as the Inspector edits each of them.
function editTarget(ctx) {
  const { selection, doc } = ctx.store;

  if (ctx.item) {
    return ctx.item.kind === 'nodes' ? findNode(doc, ctx.item.id) : null;
  }

  return selection.nodes.length === 1 && selection.edges.length === 0
    ? findNode(doc, selection.nodes[0])
    : null;
}

// --- actions shared with the toolbar -------------------------------------------

// Ids are generated, but quoted and escaped all the same.
function attr(value) {
  return `"${String(value).replace(/["\\]/g, '\\$&')}"`;
}

function canvasItem(kind, id) {
  const type = kind === 'edges' ? 'edge' : 'node';

  return document.querySelector(
    `#builder-canvas .vue-flow__${type}[data-id=${attr(id)}]`,
  );
}

// The part of the view that holds an element, to find a place for focus when
// the element is gone: 'canvas' (the canvas, its nodes and connections, not
// its zoom buttons), 'inspector', or '' elsewhere.
function regionOf(element) {
  if (element?.closest?.(CANVAS)) {
    return element.matches(CANVAS) || element.matches(CANVAS_ITEM)
      ? 'canvas'
      : '';
  }

  return element?.closest?.('.builder-inspector') ? 'inspector' : '';
}

// The heading of each region takes focus in its place. The canvas section
// is its own heading. On the landing, the shown tab takes focus.
const REGION_FOCUS = {
  canvas: '#builder-canvas',
  inspector: '#inspector-title',
};

/**
 * Records what has focus before a command runs, so keepFocus can put focus
 * back if the command re-renders or removes it.
 */
function noteFocus() {
  const doc = globalThis.document;
  const element = doc?.activeElement;

  if (!element || element === doc.body || !element.closest?.('.builder-root')) {
    return null;
  }

  const item = itemOf(element, 'canvas');
  const testId = element.dataset?.testid;
  let find = () => null;

  if (item) {
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
 * Whether focus has fallen to the page: nothing has it, <body> has it, or its
 * element left the page.
 */
export function focusLost() {
  const active = document.activeElement;

  return !active || active === document.body || !active.isConnected;
}

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

/**
 * Focuses an element as soon as it can take focus. Vue Flow shows a new node
 * only after it measures it, so this tries again on the next frames.
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
 * Puts focus back after a command left it on <body> or on a removed element
 * (WCAG 2.4.3). Vue Flow renders a tick after the store, so it waits two ticks.
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

// Delete and Ungroup remove what the user was working on. Thus focus moves
// to the canvas, which is named and handles the canvas keys (WCAG 2.4.3).
function focusCanvas() {
  document.getElementById('builder-canvas')?.focus();
}

/**
 * Deletes the selection, then focuses the canvas.
 *
 * @param {object} store
 */
export async function deleteSelection(store) {
  store.removeSelection();
  await nextTick();

  focusCanvas();
}

/**
 * Ungroups the selected group, then focuses a member's node so the arrow keys
 * keep working.
 */
export async function ungroupSelection(store) {
  const groupId = selectedGroup(store).id;
  const member = store.doc.nodes.find((node) => node.parentId === groupId);

  store.ungroup();
  // Vue Flow renders the members a tick after the store changes.
  await nextTick();
  await nextTick();

  if (!(member && (await focusSoon(canvasItem('nodes', member.id))))) {
    focusCanvas();
  }
}

/**
 * Groups the selected nodes, then focuses the new group's node, because
 * grouping re-renders the focused node.
 */
async function groupSelection(store) {
  const group = store.group();

  if (!group || !globalThis.document) {
    return;
  }

  // Vue Flow renders the new node a tick after the store changes.
  await nextTick();
  await nextTick();

  await focusSoon(canvasItem('nodes', group.id));
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
 * Saves what the user sees now. It commits the focused text field and settles
 * the Inspector's unapplied edits first. With nothing left to save, it sends
 * nothing.
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

// The networks of each node, read once for the whole document so listing
// thousands of nodes stays linear. Returns networksOf(node).
function nodeNetworks(doc) {
  const networks = doc.networks || [];
  const ids = byId(networks);
  const order = new Map(networks.map((network, index) => [network, index]));
  const named = new Map();
  const linked = new Map();

  for (const network of networks) {
    const name = foldKey(network.name);

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
      const name = foldKey(iface?.vlan);

      for (const network of (name && named.get(name)) || []) {
        found.add(network);
      }
    }

    return [...found].sort((a, b) => order.get(a) - order.get(b));
  };
}

// The ranks of the fields that find a node, in the order that Go to node
// lists its matches: a node whose name matches comes first, then one whose
// IP address matches, and so on (see matchItem in fuzzy.js). The image is
// last: it finds a node, but it names no one node.
export const NODE_FIELD_RANKS = {
  Hostname: 0,
  'IP address': 1,
  'MAC address': 2,
  VLAN: 3,
  Network: 3,
  Annotation: 4,
  Label: 5,
  Description: 6,
  Image: 7,
};

// An annotation's value as text: phenix keeps any JSON value there.
function annotationText(value) {
  return value !== null && typeof value === 'object'
    ? JSON.stringify(value)
    : String(value ?? '');
}

// The key=value text of each entry of a map (labels, annotations).
function entryTexts(map, text = String) {
  return isPlainObject(map)
    ? Object.entries(map).map(([key, value]) => `${key}=${text(value)}`)
    : [];
}

// A field of an interface as the palette names it before the value:
// "eth0 IP address", or "IP address" for an interface with no name.
function interfaceField(iface, label) {
  return [iface?.name, label].filter(Boolean).join(' ');
}

// What a node is found by beside its name, in the order of NODE_FIELD_RANKS.
// `prefix` is what the palette shows before a matched value.
function nodeFields(node, networks, interfaces, image) {
  const hostname = node.device?.hostname;
  const spec = node.device?.spec;
  const vlans = new Set(interfaces.map((iface) => foldKey(iface?.vlan)));
  const description =
    node.kind === 'switch'
      ? networks[0]?.description
      : spec?.general?.description;
  const fields = [
    ['Hostname', hostname !== nodeLabel(node) && hostname, 'hostname'],
    ...interfaces.map((iface) => [
      'IP address',
      iface?.address,
      interfaceField(iface, 'IP address'),
    ]),
    ...interfaces.map((iface) => [
      'MAC address',
      iface?.mac,
      interfaceField(iface, 'MAC address'),
    ]),
    ...interfaces.map((iface) => [
      'VLAN',
      iface?.vlan,
      interfaceField(iface, 'VLAN'),
    ]),
    ...networks.flatMap((network) => [
      ['Network', !vlans.has(foldKey(network.name)) && network.name],
      ['VLAN', network.alias && `VLAN ${network.alias}`],
    ]),
    ...entryTexts(spec?.annotations, annotationText).map((text) => [
      'Annotation',
      text,
      'annotation',
    ]),
    ...entryTexts(spec?.labels).map((text) => ['Label', text, 'label']),
    ['Description', description, 'description'],
    ['Image', image],
  ];

  return fields
    .filter(([, value]) => value)
    .map(([label, value, prefix]) => ({
      label,
      value: String(value),
      rank: NODE_FIELD_RANKS[label],
      ...(prefix ? { prefix: String(prefix) } : {}),
    }));
}

/**
 * Every node, as a choice for Go to node, found also by its fields (see
 * nodeFields). It reads the raw document in one pass: the store replaces the
 * document on every edit, so Vue does not need to track each field.
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
    const fields = nodeFields(node, networks, interfaces, image);

    return {
      id: node.id,
      title: nodeLabel(node),
      detail: detail.join(' · '),
      icon: nodeIconKey(node),
      keywords: [
        ...new Set(
          [
            node.device?.hostname,
            node.label,
            ...networks.flatMap((network) => [
              network.name,
              network.alias && String(network.alias),
            ]),
            ...fields.map((field) => field.value),
          ].filter(Boolean),
        ),
      ],
      fields,
      value: node.id,
    };
  });
}

/** Every network, as a choice for Go to network, by name or VLAN alias. */
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
      // The hexagon of a network, as the Go to network command shows it.
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

// The devices with an interface on each network (see interfacesByNetwork),
// as choices of Select devices on network: their ids are the value.
function networkDeviceChoices(doc) {
  const plain = toRaw(doc);
  const members = interfacesByNetwork(plain);

  return (plain.networks || []).map((network) => {
    const ids = [
      ...new Set((members.get(network.id) || []).map(({ node }) => node.id)),
    ];

    return {
      id: network.id,
      title: network.name,
      detail: [
        count(ids.length, 'device'),
        network.alias ? `VLAN ${network.alias}` : 'no VLAN alias',
      ].join(' · '),
      icon: 'network-wired',
      keywords: network.alias
        ? [String(network.alias), `VLAN ${network.alias}`]
        : [],
      disabled: ids.length ? '' : 'No device is on this network.',
      value: ids,
    };
  });
}

// Each disk image of the devices' drives once, sorted, as choices of Select
// devices using image. The value is the ids of its devices.
function imageDeviceChoices(doc) {
  const devices = new Map();

  for (const node of toRaw(doc).nodes || []) {
    for (const image of driveImages(node)) {
      devices.set(image, (devices.get(image) || new Set()).add(node.id));
    }
  }

  return [...devices]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([image, ids]) => ({
      id: image,
      title: image,
      detail: count(ids.size, 'device'),
      value: [...ids],
    }));
}

// Selects the devices of a choice, says how many and why, and brings them
// into view, as Select nodes on layer does.
function selectDevices(ctx, ids, where) {
  if (!ids.length) {
    ctx.store.announce(`No device ${where}.`);

    return;
  }

  ctx.store.select({ nodes: ids, edges: [] });
  ctx.store.announce(`Selected ${count(ids.length, 'device')} ${where}`);
  ctx.view.revealNode?.(ids);
}

// The templates of the palette's sections, as choices of Add device. A
// template in several collections is in several sections, but it is one
// choice, under its first section.
function uniqueTemplateChoices(groups) {
  const seen = new Set();

  return groups.flatMap((group) =>
    group.entries
      .filter((entry) => {
        const first = !seen.has(entry.key);

        seen.add(entry.key);

        return first;
      })
      .map((entry) => ({
        id: entry.key,
        title: entry.name,
        detail: `${group.label} · ${entry.image || 'no image'}`,
        icon: entry.iconKey,
        keywords: [entry.description].filter(Boolean),
        value: entry.key,
      })),
  );
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
  ctx.view.showNode(id, { zoomIn: true });
}

// Find template's choices: in the editor, the templates a device can be made
// from. On the drafts page, the templates of the library.
function templateChoices(ctx) {
  return viewOf(ctx) === 'editor'
    ? editorTemplateChoices(ctx.store)
    : libraryTemplateChoices(ctx.store.templates);
}

// A template of the library, in the template editor: to change it. A
// built-in template and one of a template file are read only.
function openLibraryTemplate({ view }, template) {
  view.openDialog(
    'template',
    template.source === 'library'
      ? { mode: 'library-edit', id: template.id }
      : { mode: 'view', id: template.id, source: template.source },
  );
}

// A topology read from the Builder file that it names is listed with the
// published diagrams, by the handle of its row as an id, and says so.
// Without `published`, the drafts alone.
function draftChoices(store, { published = true } = {}) {
  const name = (item) => item.name || item.title || item.target || item.id;
  const from = (items, where) =>
    (items || []).map((item) => ({
      id: `${where}:${item.id}`,
      title: name(item),
      detail: [
        where,
        item.createdBy && `Created by ${item.createdBy}`,
        item.source === 'file' && `File: ${item.path}`,
        item.description,
      ]
        .filter(Boolean)
        .join(' · '),
      keywords: [item.target, item.createdBy, item.path].filter(Boolean),
      value: item,
    }));

  return [
    ...from(store.drafts?.all, 'Drafts'),
    ...(published ? from(store.documents, 'Published Diagrams') : []),
  ];
}

// --- layers ----------------------------------------------------------------------

// The selected nodes that take a layer: the devices and switches, in the
// document's order. The document is read once, whatever the size of the
// selection.
function layerTargets(store) {
  const selected = new Set(store.selection.nodes);

  return (toRaw(store.doc).nodes || []).filter(
    (node) => selected.has(node.id) && LAYER_KINDS.includes(node.kind),
  );
}

/**
 * Layers as choices, the highest ID first, named as the Inspector names them
 * with their node counts. Each choice keeps its `layer` for the icon and color.
 */
function layerChoices(doc, { all = false, empty = '' } = {}) {
  const plain = toRaw(doc);
  const counts = layerCounts(plain);
  const layers = effectiveLayers(plain);

  if (all) {
    for (const id of counts.keys()) {
      if (!layers.some((layer) => layer.id === id)) {
        layers.push({ id, label: placeholderLabel(id) });
      }
    }
  }

  const titles = layerTitles(layers);

  return layers
    .sort((a, b) => b.id - a.id)
    .map((layer) => {
      const nodes = counts.get(layer.id) || 0;

      return {
        id: String(layer.id),
        title: titles.get(layer.id),
        detail: count(nodes, 'node'),
        icon: 'layer-group',
        disabled: nodes ? '' : empty,
        layer,
        value: layer.id,
      };
    });
}

// Why Set layer of selection cannot run, or true.
function layerSettable(ctx) {
  const { store } = ctx;

  if (store.readOnly) {
    return READ_ONLY;
  }

  const targets = layerTargets(store);

  if (!targets.length) {
    return 'Select a device or a switch.';
  }

  return (
    targets.some((node) => !includedFrom(node)) || includedReason(targets[0])
  );
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

// The Settings dialog's Node icon size, one choice each: off for the
// diagram's sizes (ICON_SIZE_OFF in settings.js), or the size every icon
// is drawn at on the canvas. The view reads and keeps the setting.
function nodeIconSizeChoice(value, title) {
  return {
    id: `view.nodeIconSize.${value}`,
    title: `Node icon size: ${title}`,
    group: 'View',
    keywords: ['icons', 'pictures', 'symbols', 'bigger', 'smaller', 'setting'],
    when: ({ view }) =>
      view.nodeIconSize === value
        ? 'This is the current node icon size.'
        : true,
    detail: () =>
      value === 'off'
        ? 'Use the icon sizes of the diagram'
        : 'Every icon on your canvas. The diagram does not change.',
    run: ({ view }) => view.setNodeIconSize(value),
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

function landingTab(id, title) {
  return {
    id: `drafts.tab.${id}`,
    title: `Show ${title}`,
    group: 'Drafts',
    keywords: ['tab', 'list'],
    views: LANDING,
    when: (ctx) =>
      ctx.view.draftsTab === id ? 'This tab is already shown.' : true,
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
    // The layers editor of the open diagram. On the drafts page, the Layers
    // tab, which edits the server's layer sets.
    id: 'settings.layers',
    title: 'Edit layers…',
    group: 'General',
    keywords: [
      'layer',
      'layers',
      'tier',
      'level',
      'label',
      'stack',
      'layout',
      'default layers',
      'layer set',
      'purdue',
      'matcher',
    ],
    page: true,
    views: BOTH,
    detail: ({ view }) =>
      view.editing ? 'The layers of this diagram' : 'The layer sets',
    run: ({ view }) =>
      view.editing ? view.openLayers() : view.showDraftsTab('layers'),
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
      'Pastes a copy of the selected nodes beside them and does not change the clipboard',
    run: ({ store }) => store.duplicate(),
  },
  {
    id: 'edit.delete',
    title: 'Delete selection',
    group: 'Edit',
    keywords: ['remove'],
    keys: { mac: ['Backspace', 'Delete'], other: ['Delete', 'Backspace'] },
    scope: 'canvas',
    local: true,
    fixed: true,
    when: (ctx) =>
      editable(ctx) === true
        ? hasSelection(ctx.store) || 'Nothing is selected to delete.'
        : READ_ONLY,
    run: ({ store }) => deleteSelection(store),
  },
  {
    // On the canvas and from the palette, the Inspector's first field (the
    // name or the connection's label) takes focus.
    id: 'edit.rename',
    title: 'Rename',
    group: 'Edit',
    keywords: ['name', 'hostname', 'label'],
    keys: ['F2'],
    scope: 'canvas',
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
  {
    // The Node Editor (dialogs/NodeEditorDialog.vue): the Inspector's fields
    // of one node, in a large dialog. On the canvas, its keys open it for
    // the focused node. With one node selected, it opens on that node.
    // Otherwise it asks for a device or a switch, found as Go to node finds
    // it, and selects that node. A read-only draft opens it read only.
    id: 'node.edit',
    title: 'Open Node Editor',
    group: 'Edit',
    keywords: ['edit', 'properties', 'fields', 'inspector', 'dialog'],
    keys: ['Alt+Shift+E'],
    scope: 'editor',
    steps: ['Node'],
    when: (ctx) =>
      Boolean(editTarget(ctx)) ||
      editableNodes(ctx.store.doc).length > 0 ||
      SELECT_ONE_NODE,
    detail: (ctx) =>
      editTarget(ctx) ? nodeLabel(editTarget(ctx)) : 'Choose a node',
    choices: ({ store }) =>
      nodeChoices(
        store.doc,
        editableNodes(store.doc).map((node) => node.id),
      ),
    direct: (ctx) => {
      const node = editTarget(ctx);

      return node
        ? { id: node.id, title: nodeLabel(node), value: node.id }
        : undefined;
    },
    run: (ctx, choice) => {
      const node = findNode(ctx.store.doc, choice.value);
      const { nodes, edges } = ctx.store.selection;

      if (!node) {
        return undefined;
      }

      if (
        ctx.item ||
        nodes.length !== 1 ||
        nodes[0] !== node.id ||
        edges.length
      ) {
        ctx.store.select({ nodes: [node.id], edges: [] });
      }

      return ctx.view.openDialog('node-editor', { id: node.id });
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
    // Hidden connections are not selected, so no key acts on what the
    // canvas does not show.
    run: ({ store, view }) =>
      store.selectAll({ edges: view.showConnections !== false }),
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
    // Selects every node at a layer: the layer chosen for it, or else the
    // layer its fields match (see effectiveNodeLayer). The layers listed
    // are those of the diagram and any other layer a node is at. No keys
    // by default: users can give it some.
    id: 'selection.layer',
    title: 'Select nodes on layer',
    group: 'Selection',
    keywords: ['layer', 'tier', 'level', 'purdue', 'matcher'],
    steps: ['Layer'],
    when: ({ store }) =>
      someNodeOnLayer(toRaw(store.doc)) || 'No node is on a layer.',
    detail: () => 'Choose a layer',
    choices: ({ store }) =>
      layerChoices(store.doc, {
        all: true,
        empty: 'No node is on this layer.',
      }),
    run: (ctx, choice) => {
      const doc = toRaw(ctx.store.doc);
      const ids = (doc.nodes || [])
        .filter((node) => effectiveNodeLayer(doc, node) === choice.value)
        .map((node) => node.id);

      if (!ids.length) {
        ctx.store.announce('No node is on this layer.');

        return;
      }

      ctx.store.select({ nodes: ids, edges: [] });
      ctx.store.announce(
        `Selected ${count(ids.length, 'node')} on ${layerName(doc, choice.value)}`,
      );
      ctx.view.revealNode?.(ids);
    },
  },
  {
    // Every device with an interface on a network (see interfacesByNetwork).
    // No keys by default: users can give it some.
    id: 'selection.network',
    title: 'Select devices on network',
    group: 'Selection',
    keywords: ['network', 'vlan', 'subnet', 'interface', 'connected'],
    steps: ['Network'],
    when: ({ store }) =>
      (store.doc.networks || []).length > 0 || 'The diagram has no networks.',
    detail: () => 'Choose a network',
    choices: ({ store }) => networkDeviceChoices(store.doc),
    run: (ctx, choice) =>
      selectDevices(ctx, choice.value || [], `on network ${choice.title}`),
  },
  {
    // Selects every device with a drive of a disk image. No keys by
    // default: users can give it some.
    id: 'selection.image',
    title: 'Select devices using image',
    group: 'Selection',
    keywords: ['image', 'disk', 'drive', 'qcow', 'os', 'vm'],
    steps: ['Image'],
    when: ({ store }) =>
      imageDeviceChoices(store.doc).length > 0 || 'No device has a disk image.',
    detail: () => 'Choose a disk image',
    choices: ({ store }) => imageDeviceChoices(store.doc),
    run: (ctx, choice) =>
      selectDevices(ctx, choice.value || [], `using image ${choice.title}`),
  },
  {
    id: 'selection.press',
    title: 'Select or deselect the focused item',
    group: 'Selection',
    keys: ['Enter', 'Space'],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    id: 'selection.toggle',
    title:
      'Add the focused item to the selection, or remove it from the selection',
    group: 'Selection',
    keys: ['Shift+Enter'],
    scope: 'canvas',
    local: true,
    fixed: true,
    palette: false,
  },
  {
    // The canvas is one Tab stop. These commands move focus inside it
    // (BuilderCanvas.vue).
    id: 'canvas.move',
    title: 'Move to the nearest node in the direction of the arrow key',
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
    run: ({ store }) => groupSelection(store),
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
    run: ({ store }) => ungroupSelection(store),
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
    detail: () => 'Move every node to where it was before the last layout',
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
    // Finds a connection by name and removes it without a pointer. Delete
    // on a canvas connection does the same. The interface stays, free to
    // connect again.
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
          link.label && `labeled ${link.label}`,
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
    // Removes a network with its switches and their connections. A network
    // that no switch shows on the canvas can be removed only here.
    id: 'structure.removeNetwork',
    title: 'Remove network',
    group: 'Structure',
    keywords: ['delete network', 'vlan', 'switch'],
    steps: ['Network'],
    when: (ctx) =>
      editable(ctx) === true
        ? (ctx.store.doc.networks || []).length > 0 ||
          'The diagram has no networks.'
        : READ_ONLY,
    detail: () => 'Choose a network to remove with its switches',
    choices: ({ store }) =>
      networkChoices(store.doc).map((choice) => ({
        ...choice,
        disabled: removalRefusal(toRaw(store.doc), 'networks', choice.value),
      })),
    run: ({ store }, choice) => store.removeNetwork(choice.value),
  },
  {
    // Gives each static interface on a network that has no address the next
    // free address of the network's subnet, as one Undo step. The Inspector
    // of a switch has the same button.
    id: 'structure.assignAddresses',
    title: 'Assign addresses',
    group: 'Structure',
    keywords: ['ip', 'ip address', 'subnet', 'static', 'network', 'interface'],
    steps: ['Network'],
    when: (ctx) =>
      editable(ctx) === true
        ? (ctx.store.doc.networks || []).some((network) =>
            parseSubnet(network.subnet),
          ) || 'No network in the diagram has a subnet.'
        : READ_ONLY,
    detail: () => 'Choose a network with a subnet',
    choices: ({ store }) =>
      networkChoices(store.doc).map((choice) => {
        const { subnet } =
          (toRaw(store.doc).networks || []).find(
            (network) => network.id === choice.value,
          ) || {};

        return {
          ...choice,
          detail: subnet ? `${subnet} · ${choice.detail}` : choice.detail,
          disabled: parseSubnet(subnet) ? '' : 'This network has no subnet.',
        };
      }),
    run: ({ store }, choice) => store.assignAddresses(choice.value),
  },
  {
    // Move to group, in the Inspector's header: a dialog that moves a node
    // into a group, or out of the one it is in. No keys by default: users
    // can give it some.
    id: 'dialog.regroup',
    title: 'Move to a group…',
    group: 'Structure',
    keywords: ['group', 'regroup', 'member', 'container', 'parent'],
    when: editable,
    detail: () => 'Into a group, or out of one',
    run: ({ view }) => view.openDialog('regroup'),
  },
  {
    // Puts every selected device and switch at the layer chosen, or at
    // none, in one undo step (store.setNodesLayer). The layers listed are
    // those of the diagram. No keys by default: users can give it some.
    id: 'structure.setLayer',
    title: 'Set layer of selection',
    group: 'Structure',
    keywords: ['layer', 'tier', 'level', 'purdue', 'move to layer'],
    steps: ['Layer'],
    when: layerSettable,
    detail: ({ store }) => {
      const targets = layerTargets(store);
      const devices = targets.filter((node) => node.kind === 'device').length;
      const named = listOf(
        [
          devices && count(devices, 'device'),
          targets.length - devices &&
            count(targets.length - devices, 'switch', 'switches'),
        ].filter(Boolean),
      );

      return named ? `${named} selected` : '';
    },
    choices: ({ store }) => [
      ...layerChoices(store.doc),
      {
        id: 'none',
        title: 'No layer',
        detail: 'A device then goes to the layer its fields match, if any',
        icon: 'close',
        value: null,
      },
    ],
    run: ({ store }, choice) =>
      store.setNodesLayer(store.selection.nodes, choice.value),
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
      ...uniqueTemplateChoices(store.paletteTemplateGroups),
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
  // Every other entry of Nodes & Objects, by its id: add.switch, add.note,
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
    // Add connection, in the Inspector's header: a dialog that connects a
    // device to a switch, through a free interface or a new one. The
    // selection fills its fields. No keys by default: users can give it
    // some. It comes after the node commands, so a search for "add" that
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
    // The palette's "+" beside Templates: the template editor, on
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
    // Templates tab, where the template library is managed. Leaving the
    // draft goes as the Drafts button does.
    id: 'templates.library',
    title: 'Open Node Templates library',
    group: 'Go to',
    keywords: ['template', 'library', 'collection'],
    detail: () => 'On the Drafts page',
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
    nameFirst: true,
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
      ctx.view.showNode(ids[0], { zoomIn: true });
    },
  },
  {
    // t: in the palette. In the editor, a template adds a device made from
    // it, as a click in Nodes & Templates does. On the drafts page, it opens
    // in the template editor.
    id: 'goto.template',
    title: 'Find template',
    group: 'Go to',
    keywords: ['search', 'collection'],
    views: BOTH,
    prefix: 't:',
    steps: ['Template'],
    when: (ctx) => {
      if (viewOf(ctx) === 'editor' && editable(ctx) !== true) {
        return editable(ctx);
      }

      return templateChoices(ctx).length > 0 || 'There are no templates.';
    },
    detail: (ctx) =>
      viewOf(ctx) === 'editor'
        ? 'Adds a device made from a template'
        : 'Opens a template in the template editor',
    choices: (ctx) => templateChoices(ctx),
    run: (ctx, choice) =>
      viewOf(ctx) === 'editor'
        ? addInView(ctx, paletteNode(ctx.store, 'device', choice.value))
        : openLibraryTemplate(ctx, choice.value),
  },
  {
    // c: in the palette. From the editor, the editor closes first, as Open
    // Node Templates library does.
    id: 'goto.collection',
    title: 'Find collection',
    group: 'Go to',
    keywords: ['search', 'template'],
    views: BOTH,
    prefix: 'c:',
    steps: ['Collection'],
    when: ({ store }) =>
      collectionChoices(store.templates).length > 0 ||
      'There are no collections.',
    detail: () => 'Shows a collection on the Node Templates tab',
    choices: ({ store }) => collectionChoices(store.templates),
    run: ({ view }, choice) => view.openTemplateLibrary(choice.value),
  },
  {
    // i: in the palette. From the editor, the editor closes first.
    id: 'goto.icon',
    title: 'Find icon',
    group: 'Go to',
    keywords: ['search', 'custom icon', 'owner'],
    views: BOTH,
    prefix: 'i:',
    steps: ['Icon'],
    when: ({ view }) =>
      (view.icons || []).length > 0 || 'There are no custom icons.',
    detail: () => 'Shows an icon on the Icons tab',
    choices: ({ view }) => iconChoices(view.icons),
    run: ({ view }, choice) => view.openIcons(choice.value),
  },
  {
    id: 'goto.canvas',
    title: 'Focus canvas',
    group: 'Go to',
    keywords: ['diagram'],
    run: ({ view }) => view.focusCanvas(),
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
    title: 'Go to the Drafts page',
    group: 'Go to',
    keywords: ['back', 'close', 'landing', 'list'],
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
    // The Settings dialog's Show node icons: the icons of devices, switches
    // and groups on the canvas. The document keeps them.
    id: 'view.nodeIcons',
    title: 'Show or hide node icons',
    group: 'View',
    keywords: ['pictures', 'symbols'],
    label: ({ view }) =>
      view.showNodeIcons ? 'Hide node icons' : 'Show node icons',
    run: ({ view }) => view.toggleNodeIcons(),
  },
  {
    // The Settings dialog's Show node descriptions. A node's own toggle in the
    // Inspector can still hide its description. The document keeps them.
    id: 'view.nodeDescriptions',
    title: 'Show or hide node descriptions',
    group: 'View',
    keywords: ['comments', 'text', 'details'],
    label: ({ view }) =>
      view.showNodeDescriptions === false
        ? 'Show node descriptions'
        : 'Hide node descriptions',
    run: ({ view }) => view.toggleNodeDescriptions(),
  },
  {
    // Hidden connections stay in the document, publishing, downloads and the
    // checks. The canvas keys skip them (BuilderCanvas.vue).
    id: 'view.connections',
    title: 'Show or hide connections',
    group: 'View',
    keywords: ['links', 'lines', 'edges', 'cables', 'wires'],
    keys: ['Alt+Shift+C'],
    scope: 'editor',
    label: ({ view }) =>
      view.showConnections === false ? 'Show connections' : 'Hide connections',
    run: ({ view }) => view.toggleConnections(),
  },
  nodeIconSizeChoice('off', 'Off'),
  nodeIconSizeChoice('small', 'Small'),
  nodeIconSizeChoice('medium', 'Medium'),
  nodeIconSizeChoice('large', 'Large'),
  {
    // The Settings dialog's Snap to grid: drags, drops, new nodes and the
    // points of lines go to the diagram's grid. It has no default keys.
    id: 'view.snapToGrid',
    title: 'Snap to grid',
    group: 'View',
    keywords: ['align', 'snapping', 'magnet'],
    label: ({ view }) =>
      view.snapToGrid ? 'Turn off snap to grid' : 'Turn on snap to grid',
    run: ({ view }) => view.toggleSnapToGrid(),
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
  pane('start', 'Nodes & Templates', ['palette', 'nodes', 'templates']),
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
      'drawio',
      'gexf',
      'gephi',
      'graph',
      'topology',
    ],
    detail: () =>
      'Builder JSON or YAML, Topology YAML, PNG, SVG, draw.io or Gephi (GEXF)',
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
    'drawio',
    'draw.io',
    'The diagram as a drawing to edit in draw.io',
    ['drawio', 'diagrams.net', 'drawing', 'diagram'],
  ),
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
    run: ({ view }) => view.combineIncluded(),
  },
  {
    // In the editor, the open draft. On the drafts page, a draft to choose.
    // A published diagram shown read only has no draft to copy.
    id: 'draft.duplicate',
    title: 'Duplicate draft…',
    group: 'Draft',
    aliases: ['copy', 'clone'],
    keywords: ['new draft', 'save as'],
    views: BOTH,
    steps: ['Draft'],
    when: (ctx) => {
      if (viewOf(ctx) === 'editor') {
        return (
          !ctx.store.published ||
          'A published diagram has no draft to duplicate. Select Edit first.'
        );
      }

      return (
        draftChoices(ctx.store, { published: false }).length > 0 ||
        'There are no drafts to duplicate.'
      );
    },
    detail: (ctx) =>
      viewOf(ctx) === 'editor'
        ? 'A new draft with a copy of this diagram, without its history'
        : 'A new draft with a copy of a draft, without its history',
    choices: ({ store }) => draftChoices(store, { published: false }),
    direct: (ctx) =>
      viewOf(ctx) === 'editor'
        ? {
            id: 'open',
            title: ctx.store.doc.metadata?.name || 'This draft',
            value: null,
          }
        : undefined,
    run: ({ view }, choice) => view.duplicateDraft(choice?.value || null),
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
    run: ({ view }) => view.startBlank(),
  },
  {
    id: 'drafts.import',
    title: 'Import…',
    group: 'Drafts',
    keywords: ['topology', 'experiment', 'convert', 'generate', 'legacy'],
    views: LANDING,
    detail: () => 'From a topology or experiment config on the server',
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
  landingTab('drafts', 'Drafts'),
  landingTab('published', 'Published Diagrams'),
  {
    ...landingTab('templates', 'Node Templates'),
    keywords: ['tab', 'list', 'library', 'collection'],
  },
  {
    ...landingTab('layers', 'Layers'),
    keywords: ['tab', 'list', 'library', 'layer', 'layers', 'purdue'],
  },
  {
    ...landingTab('icons', 'Icons'),
    keywords: ['tab', 'list', 'library', 'custom icon', 'image'],
  },
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

/** The commands that the palette lists for the open view, available or not. */
export function paletteCommands(ctx) {
  const view = viewOf(ctx);

  return COMMANDS.filter(
    (command) =>
      command.palette !== false &&
      viewsOf(command).includes(view) &&
      command.offered?.(ctx) !== false,
  );
}

/** Whether a command can run now: true, or why it cannot. */
export function availability(idOrCommand, ctx) {
  const command = getCommand(idOrCommand);

  if (!command) {
    return 'There is no such command.';
  }

  if (!viewsOf(command).includes(viewOf(ctx))) {
    return viewOf(ctx) === 'editor'
      ? 'Return to the Drafts page first.'
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

/** The command's title as things are now. */
export function commandTitle(idOrCommand, ctx) {
  const command = getCommand(idOrCommand);

  return command?.label?.(ctx) || command?.title || '';
}

/**
 * Runs a command, or announces why it cannot run. A command that needs
 * choices opens the palette on it instead. Returns whether it ran.
 */
export function runCommand(idOrCommand, ctx, choice, picked) {
  const command = getCommand(idOrCommand);
  const state = availability(command, ctx);

  if (state !== true) {
    ctx.store.announce(state);

    return false;
  }

  // A key press runs a command with the choice it makes for keys (Add
  // device's plain Device), if it makes one. Then any run takes the choice
  // the state makes (Open Node Editor's selected node), if there is one.
  const chosen =
    choice === undefined
      ? ((ctx.source === 'key' ? command.keyChoice?.(ctx) : undefined) ??
        command.direct?.(ctx))
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

/** A command's default keys on a platform. */
export function defaultKeys(idOrCommand, platform = currentPlatform()) {
  const keys = getCommand(idOrCommand)?.keys;

  if (!keys) {
    return [];
  }

  return Array.isArray(keys) ? keys : keys[platform] || [];
}

/** Whether users may change a command's keys. */
export function isCustomizable(idOrCommand) {
  const command = getCommand(idOrCommand);

  return Boolean(command && !command.fixed && !command.local);
}

/**
 * The keys that a command answers to now (reactive). One-character keys are
 * left out while the single-key switch is off, unless `all` is set.
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

/** A command's keys as the platform writes them ('⇧⌘Z'), or '' without keys. */
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
 * What the Builder says when a key goes to a hidden connection: how to show
 * them again.
 */
export function connectionsHiddenText() {
  const keys = shortcutLabel('view.connections', { text: true });

  return keys
    ? `Connections are hidden. Press ${keys} to show them.`
    : 'Connections are hidden. Show them from the command palette.';
}

/**
 * A command's aria-keyshortcuts value, or undefined so Vue leaves the attribute
 * out.
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
 * A tooltip's text with the command's keys ('Undo (⌘Z)'), or the text alone.
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

/** Whether a command's keys work in text fields (see dispatchKeydown). */
export function worksInTextFields(idOrCommand) {
  return reachOf(getCommand(idOrCommand) || {}).has('field');
}

/**
 * Whether a command can take a letter alone as a key: only canvas-only commands
 * can.
 */
export function takesLetters(idOrCommand) {
  const reach = [...reachOf(getCommand(idOrCommand) || {})];

  return reach.length > 0 && reach.every((focus) => focus === 'canvas');
}

/** The other commands that a key would clash with if this command took it. */
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

// The app's own dialogs (Buefy modals) and the Builder's.
const APP_DIALOG =
  'dialog, [role="dialog"], [role="alertdialog"], [aria-modal="true"]';

/**
 * Where a key press is: 'dialog', 'field', 'landing', 'canvas', 'editor',
 * 'page' (outside the Builder on its page) or 'outside' (an app text field or
 * dialog).
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

  return 'editor';
}

// The node or connection a key was pressed on.
function itemOf(target, focus) {
  if (focus === 'canvas') {
    const kind =
      (target.classList?.contains('vue-flow__node') && 'nodes') ||
      (target.classList?.contains('vue-flow__edge') && 'edges') ||
      '';
    const id = kind && target.getAttribute('data-id');

    return id ? { kind, id } : null;
  }

  return null;
}

/**
 * The one key handler for the Builder, bound to the window. A key that types
 * a character in a text field goes to the field. A matched key is always
 * taken from the browser, and a command that cannot run announces why.
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
 * The accessible descriptions of the keys on a focused node, a connection and
 * the canvas.
 */
export function canvasHints({ readOnly, platform = currentPlatform() }) {
  const first = (id) => {
    const [key] = commandKeys(id, { platform });

    return key ? keyText(key, platform) : '';
  };
  const select =
    `${first('selection.press')} selects or deselects. ` +
    `${first('selection.toggle')} adds to the selection. ` +
    `${first('selection.clear')} clears the selection.`;
  const next = first('canvas.connections');
  const node = `Arrow keys move between nodes. ${next} moves through this node’s connections. ${select}`;
  const edge = `Arrow keys move to the nodes. ${next} moves to the next connection. ${select}`;
  // The advice for screen readers is here, with the canvas's own keys. In
  // browse mode, the arrow keys move the virtual cursor, not the focus (see
  // BuilderCanvas.vue).
  const sheet = shortcutLabel('shortcuts.open', { platform, text: true });
  const canvas =
    `Arrow keys move to the nodes. ${first('selection.press')} ` +
    'selects the focused one. With a screen reader, use its focus mode ' +
    '(forms mode in JAWS) for these keys. You can also find a node with ' +
    'Go to node in the command palette. ' +
    (sheet
      ? `${sheet} lists every shortcut.`
      : 'Shortcuts in the header lists every shortcut.');
  const remove = first('edit.delete');

  return readOnly
    ? { node, edge, canvas: `${READ_ONLY} ${canvas}` }
    : {
        node: `${node} Shift and an arrow key move the selected nodes. ${remove} removes.`,
        edge: `${edge} ${remove} removes. Edit the label in the Inspector.`,
        canvas,
      };
}
