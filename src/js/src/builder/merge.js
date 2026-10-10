// Three-way merge of Builder documents.
//
// When a save finds that someone else saved the draft first, the editor
// holds three versions of the document: the base (the server snapshot its
// unsaved changes started from), mine (the document on screen) and theirs
// (the server's current document). mergeDocuments combines them field by
// field: a change made on one side only is taken, and only a field both
// sides changed to different values is a clash, which the user settles
// (applyChoices, and MergeDialog.vue).
//
// The rules:
//
// - Nodes, networks, edges and templates are matched by id, custom icons by
//   name. An element one side added is added; one side deleted and the
//   other left as it was is deleted; one side deleted and the other changed
//   is a clash.
// - A node one side deleted is a clash too when the other side connected
//   it: added a connection to it, or changed one that ends at it. So is a
//   device's interface one side deleted and the other connected. Keeping
//   the deletion drops those connections, and keeping the other side keeps
//   the node or interface with them.
// - Within an element, objects merge key by key. Lists whose items all carry
//   a unique id, or else a unique name, merge item by item the same way: a
//   device's interface handles by id, and its spec's interfaces by the id
//   of the handle of the same name (by name where no handle has it), so an
//   interface renamed is still the same interface. Any other list (a
//   line's points, an edge's route, the diagram's notes), a node's position
//   and its size, and every scalar are one value each: they clash when both
//   sides changed them to different values.
// - The diagram's scenarios merge as a set of names: what either side added
//   is added and what either side removed is removed.
// - The viewport is mine and never clashes. What the server stamps on every
//   save ($schema, revision, metadata.id and the creator, creation time,
//   last editor and last edit time) is theirs.
// - A merged list holds theirs order, then the items only mine holds, in
//   mine's order.
// - A device's label, hostname and spec hostname change together when it is
//   renamed, so they are one choice: when any of them clashes, the clash
//   covers all three, and all three come from the side chosen. A switch's
//   label is its network's name (namedSwitches in model.js names it again),
//   so it never clashes. An interface's name is its handle's and its spec
//   entry's (see renameInterface in model.js), so it is one choice too.
// - A connection whose node or device interface is not in the merged
//   document, which the user chose to delete, is dropped, and the merge
//   names it (dropped, see droppedText).
//
// The functions are pure: they never change their arguments.

import { count, listOf } from './announce.js';
import { nodeLabel } from './model.js';

// Set by the server on every save; they come from theirs.
const STAMPED = ['id', 'createdBy', 'createdAt', 'updatedBy', 'updatedAt'];

// The element lists of a document, matched by id.
const ELEMENT_LISTS = ['nodes', 'networks', 'edges', 'templates'];

// Optional document keys left out when empty, as the server writes them.
const OMITTED_EMPTY = new Set(['templates', 'scenarios', 'icons']);

// A node's keys that are one value each.
const ATOMIC_NODE_KEYS = new Set(['position', 'size']);

// The payload keys of a node, which a field's label leaves out.
const PAYLOADS = new Set([
  'device',
  'switch',
  'note',
  'group',
  'shape',
  'icon',
  'line',
]);

// A device's fields that a rename changes together (see renamePatch and
// updateNode in model.js): one choice, named "name".
const DEVICE_NAME_FIELDS = new Set([
  'label',
  'device.hostname',
  'device.spec.general.hostname',
]);

// The same fields as paths within the node, the label first: a clash of the
// name is described by the first of them that clashes.
const DEVICE_NAME_PATHS = [...DEVICE_NAME_FIELDS].map((field) =>
  field.split('.'),
);

// Where a device keeps its interface handles and its spec's interfaces.
const HANDLES_PATH = ['device', 'interfaces'];
const SPEC_INTERFACES_PATH = ['device', 'spec', 'network', 'interfaces'];

// What the spec interfaces of a device being merged are matched by: the id
// of the handle of the same name, or "name:" and the name where no handle
// has it. A symbol, so sameValue and a merged object leave it out, and the
// merged device loses it (see untagSpecInterfaces).
const HANDLE = Symbol('handle');

// What a field is called, by its path within its element, where the path
// alone would not say it plainly.
const FIELD_LABELS = {
  parentId: 'group',
  'device.iconKey': 'icon',
  'device.spec.general.description': 'description',
  'device.spec.general.notes': 'notes',
  'switch.networkId': 'network',
  'note.text': 'text',
  'group.title': 'title',
  sourceNodeId: 'device',
  sourceHandleId: 'interface',
  targetNodeId: 'switch',
  targetHandleId: 'switch handle',
  networkId: 'network',
  iconSize: 'icon size',
  'device.purdueLevel': 'Purdue layer',
  'switch.purdueLevel': 'Purdue layer',
};

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function unique(list) {
  return [...new Set(list)];
}

/**
 * Whether two JSON values are the same. A key whose value is undefined is a
 * key the object does not have.
 *
 * @param {*} a
 * @param {*} b
 * @returns {boolean}
 */
export function sameValue(a, b) {
  if (a === b) {
    return true;
  }

  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, index) => sameValue(item, b[index]))
    );
  }

  if (!isObject(a) || !isObject(b)) {
    return false;
  }

  const keys = (value) =>
    Object.keys(value).filter((key) => value[key] !== undefined);
  const own = keys(a);

  return (
    own.length === keys(b).length &&
    own.every((key) => sameValue(a[key], b[key]))
  );
}

// Whether every item of `list` is an object with its own value of `field`,
// a text or a number that no other item has.
function uniqueBy(list, field) {
  const seen = new Set();

  return list.every((item) => {
    const key = isObject(item) ? item[field] : undefined;
    const usable =
      (typeof key === 'string' && key !== '') || Number.isFinite(key);

    if (!usable || seen.has(key)) {
      return false;
    }

    seen.add(key);

    return true;
  });
}

// The field the items of the lists are matched by: the first of `fields`
// every item has a unique value of, or '' when the lists are one value
// each.
function matchField(lists, fields = ['id', 'name']) {
  return (
    fields.find((field) => lists.every((list) => uniqueBy(list, field))) || ''
  );
}

function words(segment) {
  return String(segment)
    .replace(/_/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .toLowerCase();
}

// What a path segment that is an item of a list ({item, label}) is called:
// the item's name where it has one, else the id or name it is matched by.
function itemLabel(segment) {
  return segment.label ?? segment.item;
}

// What a field is called, from its path within its element. A path segment
// that is an item ({item}) is shown by its name (see itemLabel), and an
// interface handle of a device as "interface eth0".
function fieldLabel(path) {
  const plain = path.map((segment) =>
    isObject(segment) ? `[${segment.item}]` : segment,
  );
  const joined = plain.join('.');

  if (DEVICE_NAME_FIELDS.has(joined)) {
    return 'name';
  }

  if (FIELD_LABELS[joined]) {
    return FIELD_LABELS[joined];
  }

  const [payload, list, handle, ...rest] = path;

  if (payload === 'device' && list === 'interfaces' && isObject(handle)) {
    return [
      `interface ${itemLabel(handle)}`,
      ...rest.map((segment) =>
        isObject(segment) ? itemLabel(segment) : words(segment),
      ),
    ].join(' ');
  }

  return path
    .filter(
      (segment, index) =>
        !(index === 0 && PAYLOADS.has(segment)) && segment !== 'spec',
    )
    .map((segment) => (isObject(segment) ? itemLabel(segment) : words(segment)))
    .join(' ');
}

function shorten(text, most = 80) {
  const flat = String(text).replace(/\s+/g, ' ').trim();

  return flat.length > most ? `${flat.slice(0, most - 1)}…` : flat;
}

function rounded(value) {
  return Math.round(Number(value) * 100) / 100;
}

/**
 * A value as a choice of the merge dialog shows it: a text as it is (cut
 * after 80 characters), a position as "x 120, y 40", a size as "160 wide,
 * 80 high", a list of texts joined by commas and a longer list by how many
 * items it has.
 *
 * @param {*} value
 * @returns {string}
 */
export function valueText(value) {
  if (value === undefined || value === null) {
    return 'none';
  }

  if (typeof value === 'string') {
    return value.trim() === '' ? 'empty' : shorten(value);
  }

  if (typeof value === 'boolean') {
    return value ? 'on' : 'off';
  }

  if (typeof value === 'number') {
    return String(value);
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      return 'none';
    }

    return value.every((item) => ['string', 'number'].includes(typeof item))
      ? shorten(value.join(', '))
      : count(value.length, 'item');
  }

  const keys = Object.keys(value).sort().join(',');

  if (keys === 'x,y') {
    return `x ${rounded(value.x)}, y ${rounded(value.y)}`;
  }

  if (keys === 'height,width') {
    return `${rounded(value.width)} wide, ${rounded(value.height)} high`;
  }

  return shorten(JSON.stringify(value));
}

// The paths, relative to `path`, of the fields that differ between a and b.
function changedPaths(a, b, path = [], found = []) {
  if (sameValue(a, b)) {
    return found;
  }

  if (isObject(a) && isObject(b)) {
    unique([...Object.keys(a), ...Object.keys(b)]).forEach((key) =>
      changedPaths(a[key], b[key], [...path, key], found),
    );
  } else {
    found.push(path);
  }

  return found;
}

function sentence(text) {
  return `${text.charAt(0).toUpperCase()}${text.slice(1)}`;
}

// What an element or field is called in a sentence.
function nameOf(at) {
  const field = fieldLabel(at.path);

  return field ? `${at.element} ${field}` : at.element;
}

// "its name" when the change from base to `changed` is in one field, else
// "it".
function whatChanged(at, base, changed) {
  const labels = unique(
    changedPaths(base, changed).map((path) =>
      fieldLabel([
        ...at.path,
        ...(ATOMIC_NODE_KEYS.has(path[0]) ? [path[0]] : path),
      ]),
    ),
  );

  return labels.length === 1 && labels[0] ? `its ${labels[0]}` : 'it';
}

// The key of what a connection end uses: its node, or with a handle, that
// handle of the node.
function endKey(nodeId, handleId) {
  return JSON.stringify(handleId ? [nodeId, handleId] : [nodeId]);
}

// What the connections `doc` added or changed since `base` use: how many of
// them end at each node, and at each handle of a node (see endKey).
function connectionsOf(doc, base) {
  const before = new Map(
    (Array.isArray(base?.edges) ? base.edges : [])
      .filter(isObject)
      .map((edge) => [edge.id, edge]),
  );
  const uses = new Map();
  const add = (key) => uses.set(key, (uses.get(key) || 0) + 1);

  (Array.isArray(doc?.edges) ? doc.edges : []).forEach((edge) => {
    if (!isObject(edge) || sameValue(before.get(edge.id), edge)) {
      return;
    }

    [
      [edge.sourceNodeId, edge.sourceHandleId],
      [edge.targetNodeId, edge.targetHandleId],
    ].forEach(([nodeId, handleId]) => {
      if (nodeId === undefined || nodeId === '') {
        return;
      }

      add(endKey(nodeId));

      if (handleId) {
        add(endKey(nodeId, handleId));
      }
    });
  });

  return uses;
}

// The match key of a spec interface no handle has the name of.
const NAME_KEY = 'name:';

// What a connection may use at `at`: a node element's node, or a device's
// interface, as its handle or as its spec entry matched by the handle's id.
// key is its endKey, and at the position of the clash about it: a spec
// entry's clash is its handle's, so the interface is one choice.
function connectionTarget(at) {
  if (at.nodeId === undefined) {
    return null;
  }

  if (at.isElement) {
    return { key: endKey(at.nodeId), at };
  }

  const last = at.path[at.path.length - 1];
  const list = at.path.slice(0, -1).join('.');

  if (!isObject(last)) {
    return null;
  }

  if (list === HANDLES_PATH.join('.')) {
    return { key: endKey(at.nodeId, last.item), at };
  }

  if (
    list === SPEC_INTERFACES_PATH.join('.') &&
    last.by === HANDLE &&
    !String(last.item).startsWith(NAME_KEY)
  ) {
    return {
      key: endKey(at.nodeId, last.item),
      at: {
        ...at,
        key: `${at.root}.${HANDLES_PATH.join('.')}[${JSON.stringify(last.item)}]`,
        path: [...HANDLES_PATH, { item: last.item, label: last.label }],
      },
    };
  }

  return null;
}

// One merge: the choices it applies, and what it found.
function createRun(choices, docs) {
  const nodes = new Map();
  const [base, mine, theirs] = docs;

  // The names of nodes, for connections: base first, then theirs, then
  // mine, as an element is named.
  [...docs].reverse().forEach((doc) => {
    (doc?.nodes || []).forEach((node) => {
      if (node?.id !== undefined) {
        nodes.set(node.id, node);
      }
    });
  });

  return {
    choices: choices || {},
    clashes: [],
    mine: [],
    theirs: [],
    nodes,
    connections: {
      mine: connectionsOf(mine, base),
      theirs: connectionsOf(theirs, base),
    },

    note(side, at) {
      this[side].push({ key: at.key, label: nameOf(at) });
    },

    // The position of the clash about the node or interface at `at` when
    // `side` added or changed connections that use it (see
    // connectionTarget), with how many; null when it did not.
    connected(side, at) {
      const target = connectionTarget(at);
      const uses = target ? this.connections[side].get(target.key) : 0;

      return uses ? { ...target.at, connected: { side, count: uses } } : null;
    },

    // A field or element both sides changed: recorded once per key, and
    // settled by the choice made for it, theirs until one is made.
    clash(base, mine, theirs, at) {
      const { key } = at;

      if (!this.clashes.some((clash) => clash.key === key)) {
        this.clashes.push(describeClash(key, base, mine, theirs, at));
      }

      return this.choices[key] === 'mine' ? mine : theirs;
    },
  };
}

function describeClash(key, base, mine, theirs, at) {
  const field = fieldLabel(at.path);
  const label = nameOf(at);
  // An element is deleted, a field removed; what is kept of the other side
  // is the element with that side's changes, or the field's value.
  const [done, undo] = at.isElement
    ? ['deleted', 'delete']
    : ['removed', 'remove'];
  const kept = (value, whose) =>
    at.isElement ? `keep ${label} with ${whose} changes` : valueText(value);
  let kind = 'change';
  let mineText = valueText(mine);
  let theirsText = valueText(theirs);
  let summary = `You and they both changed ${label}.`;

  if (at.connected) {
    // One side deleted it, and the other connected it, and maybe changed
    // it too (see connected in createRun): deleting it drops those
    // connections.
    const { side, count: uses } = at.connected;
    const whose = side === 'mine' ? 'your' : 'their';
    const links = `${whose} ${uses === 1 ? 'connection' : 'connections'}`;
    const drop = `${undo} ${label} and drop ${links} to it`;
    const keptWith = at.changed ? `${whose} changes` : links;
    const keep = `keep ${label} with ${keptWith}`;
    const changed = whatChanged(at, base, side === 'mine' ? mine : theirs);
    const change = at.changed ? `changed ${changed} and ` : '';

    kind = side === 'mine' ? 'removed-theirs' : 'removed-mine';
    mineText = side === 'mine' ? keep : drop;
    theirsText = side === 'mine' ? drop : keep;
    summary =
      side === 'mine'
        ? `They ${done} ${label}; you ${change}connected it.`
        : `You ${done} ${label}; they ${change}connected it.`;
  } else if (mine === undefined && base !== undefined) {
    kind = 'removed-mine';
    mineText = `${undo} ${label}`;
    theirsText = kept(theirs, 'their');
    summary = `You ${done} ${label}; they changed ${whatChanged(at, base, theirs)}.`;
  } else if (theirs === undefined && base !== undefined) {
    kind = 'removed-theirs';
    mineText = kept(mine, 'your');
    theirsText = `${undo} ${label}`;
    summary = `They ${done} ${label}; you changed ${whatChanged(at, base, mine)}.`;
  }

  return {
    key,
    kind,
    element: at.element,
    field,
    label,
    base,
    mine,
    theirs,
    mineText,
    theirsText,
    summary: sentence(summary),
  };
}

// The position of a child of `at`, the object being merged.
function childAt(at, key) {
  const path = [...at.path, key];

  return {
    key: `${at.key}.${key}`,
    root: at.root,
    element: at.element,
    kind: at.kind,
    nodeId: at.nodeId,
    forceItems: at.forceItems,
    path,
    atomic: at.scope === 'node' && ATOMIC_NODE_KEYS.has(key),
    // A switch is named after its network.
    derived: at.scope === 'node' && at.kind === 'switch' && key === 'label',
    scope: '',
  };
}

// What an element of a document list is called.
function elementName(list, item, run) {
  const nodeName = (id) => {
    const node = run.nodes.get(id);

    return node ? nodeLabel(node) : 'a removed node';
  };

  switch (list) {
    case 'nodes':
      return nodeLabel(item) || 'node';
    case 'networks':
      return `network ${item?.name || item?.id}`;
    case 'edges':
      return `connection from ${nodeName(item?.sourceNodeId)} to ${nodeName(item?.targetNodeId)}`;
    case 'templates':
      return `template ${item?.name || item?.id}`;
    default:
      return String(item?.name || item?.id || list);
  }
}

// The position of the item `id` of a list at `at`, whose items are matched
// by `field`: an element of its own in a document list, else a part of the
// element the list is in, named by the item's name where it has one.
function itemAt(at, id, item, run, field) {
  const key = `${at.key}[${JSON.stringify(id)}]`;

  if (at.list) {
    return {
      key,
      root: key,
      element: elementName(at.list, item, run),
      kind: at.list === 'nodes' ? item?.kind : '',
      nodeId: at.list === 'nodes' ? id : undefined,
      path: [],
      atomic: false,
      derived: false,
      scope: at.list === 'nodes' ? 'node' : '',
      isElement: true,
    };
  }

  const name = isObject(item) ? item.name : undefined;

  return {
    ...at,
    key,
    path: [
      ...at.path,
      {
        item: id,
        label: typeof name === 'string' && name !== '' ? name : undefined,
        by: field,
      },
    ],
    atomic: false,
    derived: false,
    scope: '',
    isElement: false,
  };
}

// Whether `path` leads to a device's interface handles or spec interfaces,
// or is one of those lists.
function onInterfacePath(path) {
  const leadsTo = (list) =>
    path.length <= list.length &&
    path.every((segment, index) => segment === list[index]);

  return leadsTo(HANDLES_PATH) || leadsTo(SPEC_INTERFACES_PATH);
}

// Whether the merge at `at` goes into the three versions, even where one
// side left them as they were, rather than take the other side's whole:
// on the way to a device's interface lists when one side deleted an
// interface the other connected (see dropsConnectedInterface), so that
// mergeItem asks about it.
function itemwise(base, mine, theirs, at) {
  if (!at.forceItems || !onInterfacePath(at.path)) {
    return false;
  }

  if (isObject(base) && isObject(mine) && isObject(theirs)) {
    return true;
  }

  return (
    [base, mine, theirs].every(Array.isArray) &&
    matchField([base, mine, theirs], [HANDLE, 'id', 'name']) !== ''
  );
}

function mergeValue(base, mine, theirs, at, run) {
  if (sameValue(mine, theirs)) {
    return mine;
  }

  const whole = !itemwise(base, mine, theirs, at);

  if (whole && sameValue(base, mine)) {
    run.note('theirs', at);

    return theirs;
  }

  if (whole && sameValue(base, theirs)) {
    run.note('mine', at);

    return mine;
  }

  if (at.derived) {
    return theirs;
  }

  if (!at.atomic) {
    if (
      isObject(mine) &&
      isObject(theirs) &&
      (base === undefined || isObject(base))
    ) {
      return mergeObject(base || {}, mine, theirs, at, run);
    }

    if (
      Array.isArray(mine) &&
      Array.isArray(theirs) &&
      (base === undefined || Array.isArray(base))
    ) {
      const field = matchField(
        [base || [], mine, theirs],
        [HANDLE, 'id', 'name'],
      );

      if (field) {
        return mergeList(base || [], mine, theirs, field, at, run);
      }
    }
  }

  return run.clash(base, mine, theirs, at);
}

function mergeObject(base, mine, theirs, at, run) {
  const merged = {};

  unique([
    ...Object.keys(theirs),
    ...Object.keys(mine),
    ...Object.keys(base),
  ]).forEach((key) => {
    const value = at.entries
      ? mergeItem(base[key], mine[key], theirs[key], at.entries(key), run)
      : mergeValue(base[key], mine[key], theirs[key], childAt(at, key), run);

    if (value !== undefined) {
      merged[key] = value;
    }
  });

  return merged;
}

// The value at `path` within `value`, or undefined.
function valueAt(value, path) {
  return path.reduce(
    (part, key) => (isObject(part) ? part[key] : undefined),
    value,
  );
}

// A copy of the object `value` with `field` at `path`, or without the field
// when `field` is undefined.
function withValueAt(value, [key, ...rest], field) {
  if (rest.length === 0) {
    const copy = { ...value };

    if (field === undefined) {
      delete copy[key];
    } else {
      copy[key] = field;
    }

    return copy;
  }

  if (!isObject(value[key])) {
    return field === undefined
      ? value
      : { ...value, [key]: withValueAt({}, rest, field) };
  }

  return { ...value, [key]: withValueAt(value[key], rest, field) };
}

// The three versions of a device, with its name settled: when any field of
// the name (DEVICE_NAME_FIELDS) clashes, that is one clash, described by the
// first field that clashes, and every version is given all three fields as
// the side the choice keeps (theirs until one is made) has them, so the rest
// of the merge takes the name whole from that side.
function settleDeviceName(base, mine, theirs, at, run) {
  const versions = [base, mine, theirs];
  const clashing = DEVICE_NAME_PATHS.find((path) => {
    const [b, m, t] = versions.map((version) => valueAt(version, path));

    return !sameValue(m, t) && !sameValue(b, m) && !sameValue(b, t);
  });

  if (!clashing) {
    return versions;
  }

  const key = `${at.root}.name`;
  const values = versions.map((version) => valueAt(version, clashing));

  run.clash(...values, { ...at, key, path: clashing, isElement: false });

  const kept = run.choices[key] === 'mine' ? mine : theirs;
  const settle = (version) =>
    DEVICE_NAME_PATHS.reduce(
      (settled, path) => withValueAt(settled, path, valueAt(kept, path)),
      version,
    );

  return versions.map((version) =>
    isObject(version) ? settle(version) : version,
  );
}

// The items of the list at `path` within `value` that are objects, or none.
function objectsAt(value, path) {
  const list = valueAt(value, path);

  return Array.isArray(list) ? list.filter(isObject) : [];
}

// A copy of the device `version` with its interface `handleId` named
// `name`: the handle, and the spec entries of the handle's old name (see
// renameInterface in model.js).
function withInterfaceName(version, handleId, name) {
  const handles = valueAt(version, HANDLES_PATH);
  const old = objectsAt(version, HANDLES_PATH).find(
    (handle) => handle.id === handleId,
  )?.name;

  if (!isObject(version) || old === undefined || old === name) {
    return version;
  }

  const renamed = withValueAt(
    version,
    HANDLES_PATH,
    handles.map((handle) =>
      isObject(handle) && handle.id === handleId ? { ...handle, name } : handle,
    ),
  );
  const specs = valueAt(renamed, SPEC_INTERFACES_PATH);

  return Array.isArray(specs)
    ? withValueAt(
        renamed,
        SPEC_INTERFACES_PATH,
        specs.map((iface) =>
          isObject(iface) && iface.name === old ? { ...iface, name } : iface,
        ),
      )
    : renamed;
}

// The three versions of a device, with its interfaces' names settled: an
// interface (a handle all three have) whose name all three differ in is one
// clash, its name, and every version is given the name the side the choice
// keeps (theirs until one is made) has, on the handle and its spec entry,
// so the rest of the merge keeps one interface of that name.
function settleInterfaceNames(versions, at, run) {
  const [base, mine, theirs] = versions;
  const byId = (version) =>
    new Map(
      objectsAt(version, HANDLES_PATH).map((handle) => [handle.id, handle]),
    );
  const [inBase, inTheirs] = [byId(base), byId(theirs)];

  return objectsAt(mine, HANDLES_PATH).reduce((settled, handle) => {
    const [b, t] = [inBase.get(handle.id), inTheirs.get(handle.id)];
    const names = [b?.name, handle.name, t?.name];

    if (
      !b ||
      !t ||
      sameValue(names[1], names[2]) ||
      sameValue(names[0], names[1]) ||
      sameValue(names[0], names[2])
    ) {
      return settled;
    }

    const key = `${at.root}.${HANDLES_PATH.join('.')}[${JSON.stringify(handle.id)}].name`;

    run.clash(...names, {
      ...at,
      key,
      path: [...HANDLES_PATH, { item: handle.id, label: b.name }, 'name'],
      isElement: false,
    });

    const kept = run.choices[key] === 'mine' ? names[1] : names[2];

    return settled.map((version) =>
      withInterfaceName(version, handle.id, kept),
    );
  }, versions);
}

// Whether one side deleted an interface of the device (a handle) that the
// other side left there and connected (see connectionsOf).
function dropsConnectedInterface([base, mine, theirs], at, run) {
  const ids = (version) =>
    new Set(objectsAt(version, HANDLES_PATH).map((handle) => handle.id));
  const [inMine, inTheirs] = [ids(mine), ids(theirs)];

  return objectsAt(base, HANDLES_PATH).some(({ id }) =>
    [
      ['mine', inMine, inTheirs],
      ['theirs', inTheirs, inMine],
    ].some(
      ([side, kept, other]) =>
        kept.has(id) &&
        !other.has(id) &&
        run.connections[side].has(endKey(at.nodeId, id)),
    ),
  );
}

// A copy of the device `version` whose spec interfaces each carry what they
// are matched by (HANDLE): the id of the handle of the same name, or
// NAME_KEY and the name where no handle has it.
function tagSpecInterfaces(version) {
  const specs = valueAt(version, SPEC_INTERFACES_PATH);

  if (!Array.isArray(specs)) {
    return version;
  }

  const ids = new Map();

  objectsAt(version, HANDLES_PATH).forEach((handle) => {
    if (typeof handle.name === 'string' && !ids.has(handle.name)) {
      ids.set(handle.name, handle.id);
    }
  });

  const tag = (name) => ids.get(name) ?? `${NAME_KEY}${name}`;

  return withValueAt(
    version,
    SPEC_INTERFACES_PATH,
    specs.map((iface) =>
      isObject(iface) && typeof iface.name === 'string'
        ? { ...iface, [HANDLE]: tag(iface.name) }
        : iface,
    ),
  );
}

// The merged device without what tagSpecInterfaces added.
function untagSpecInterfaces(node) {
  const specs = valueAt(node, SPEC_INTERFACES_PATH);

  if (
    !Array.isArray(specs) ||
    !specs.some((iface) => isObject(iface) && HANDLE in iface)
  ) {
    return node;
  }

  return withValueAt(
    node,
    SPEC_INTERFACES_PATH,
    specs.map((iface) => {
      if (!isObject(iface) || !(HANDLE in iface)) {
        return iface;
      }

      const copy = { ...iface };

      delete copy[HANDLE];

      return copy;
    }),
  );
}

// One item of a matched list, or one entry of a map of elements.
function mergeItem(base, mine, theirs, at, run) {
  if (sameValue(mine, theirs)) {
    return mine;
  }

  if (base === undefined) {
    if (mine === undefined) {
      run.note('theirs', at);

      return theirs;
    }

    if (theirs === undefined) {
      run.note('mine', at);

      return mine;
    }
  } else if (mine === undefined || theirs === undefined) {
    const keeper = mine === undefined ? 'theirs' : 'mine';
    const kept = keeper === 'mine' ? mine : theirs;
    // Deleting what the other side connected would leave connections to
    // nothing, so the user chooses, even when it is otherwise as it was.
    const connected = run.connected(keeper, at);

    if (connected) {
      return run.clash(base, mine, theirs, {
        ...connected,
        changed: connected.key === at.key && !sameValue(base, kept),
      });
    }

    if (!sameValue(base, kept)) {
      return run.clash(base, mine, theirs, at);
    }

    run.note(keeper === 'mine' ? 'theirs' : 'mine', at);

    return undefined;
  }

  if (at.isElement && at.kind === 'device') {
    const versions = settleInterfaceNames(
      settleDeviceName(base, mine, theirs, at, run),
      at,
      run,
    ).map(tagSpecInterfaces);
    const position = dropsConnectedInterface(versions, at, run)
      ? { ...at, forceItems: true }
      : at;

    return untagSpecInterfaces(mergeValue(...versions, position, run));
  }

  return mergeValue(base, mine, theirs, at, run);
}

function mergeList(base, mine, theirs, field, at, run) {
  const index = (list) => new Map(list.map((item) => [item[field], item]));
  const [inBase, inMine, inTheirs] = [base, mine, theirs].map(index);
  const merged = [];
  const one = (id) => {
    const item = inBase.get(id) ?? inTheirs.get(id) ?? inMine.get(id);
    const value = mergeItem(
      inBase.get(id),
      inMine.get(id),
      inTheirs.get(id),
      itemAt(at, id, item, run, field),
      run,
    );

    if (value !== undefined) {
      merged.push(value);
    }
  };

  theirs.forEach((item) => one(item[field]));
  mine.forEach((item) => {
    if (!inTheirs.has(item[field])) {
      one(item[field]);
    }
  });

  return merged;
}

/**
 * The scenarios of a merged document: theirs, without those mine removed,
 * then those mine added, in mine's order. Names are compared ignoring case,
 * as the document holds each once ignoring case.
 *
 * @param {string[]} [base]
 * @param {string[]} [mine]
 * @param {string[]} [theirs]
 * @returns {string[]}
 */
export function mergeScenarios(base = [], mine = [], theirs = []) {
  const fold = (name) => String(name).toLowerCase();
  const inBase = new Set((base || []).map(fold));
  const inMine = new Set((mine || []).map(fold));
  const removed = new Set(
    (base || []).map(fold).filter((name) => !inMine.has(name)),
  );
  const merged = (theirs || []).filter((name) => !removed.has(fold(name)));
  const held = new Set(merged.map(fold));

  (mine || []).forEach((name) => {
    if (!inBase.has(fold(name)) && !held.has(fold(name))) {
      merged.push(name);
      held.add(fold(name));
    }
  });

  return merged;
}

function mergeMetadata(base = {}, mine = {}, theirs = {}, run) {
  const merged = {};

  unique([
    ...Object.keys(theirs),
    ...Object.keys(mine),
    ...Object.keys(base),
  ]).forEach((key) => {
    const value = STAMPED.includes(key)
      ? (theirs[key] ?? (key === 'id' ? mine[key] : undefined))
      : mergeValue(
          base[key],
          mine[key],
          theirs[key],
          {
            key: `metadata.${key}`,
            root: 'metadata',
            element: 'Diagram',
            path: [key],
          },
          run,
        );

    if (value !== undefined) {
      merged[key] = value;
    }
  });

  return merged;
}

// One root key of a document.
function mergeRoot(key, base, mine, theirs, run) {
  if (key === '$schema' || key === 'revision') {
    return theirs[key] ?? mine[key];
  }

  if (key === 'viewport') {
    return mine.viewport ?? theirs.viewport;
  }

  if (key === 'metadata') {
    return mergeMetadata(base.metadata, mine.metadata, theirs.metadata, run);
  }

  if (key === 'scenarios') {
    return mergeScenarios(base.scenarios, mine.scenarios, theirs.scenarios);
  }

  if (ELEMENT_LISTS.includes(key)) {
    const lists = [base[key] || [], mine[key] || [], theirs[key] || []];

    if (matchField(lists) === 'id') {
      return mergeList(...lists, 'id', { key, list: key, path: [] }, run);
    }
  }

  if (key === 'icons') {
    return mergeObject(
      base.icons || {},
      mine.icons || {},
      theirs.icons || {},
      {
        key,
        path: [],
        entries: (name) => ({
          key: `icons[${JSON.stringify(name)}]`,
          root: `icons[${JSON.stringify(name)}]`,
          element: `icon ${name}`,
          path: [],
          isElement: true,
        }),
      },
      run,
    );
  }

  const element = { grid: 'Grid', source: 'Source' }[key];

  return mergeValue(
    base[key],
    mine[key],
    theirs[key],
    element
      ? { key, root: key, element, path: [] }
      : { key, root: key, element: 'Diagram', path: [key] },
    run,
  );
}

// The merged document's connections without those whose node, or whose
// device interface, it does not have, which a choice to delete them left;
// each one dropped is named ({key, label}) in `dropped`.
function dropDangling(doc, run, dropped) {
  if (!Array.isArray(doc.edges) || !Array.isArray(doc.nodes)) {
    return;
  }

  const nodes = new Map(
    doc.nodes.filter(isObject).map((node) => [node.id, node]),
  );
  const live = (nodeId, handleId) => {
    const node = nodes.get(nodeId);

    if (!node) {
      return false;
    }

    return (
      node.kind !== 'device' ||
      !handleId ||
      objectsAt(node, HANDLES_PATH).some((handle) => handle.id === handleId)
    );
  };

  const kept = doc.edges.filter((edge) => {
    if (
      !isObject(edge) ||
      (live(edge.sourceNodeId, edge.sourceHandleId) &&
        live(edge.targetNodeId, edge.targetHandleId))
    ) {
      return true;
    }

    dropped.push({
      key: `edges[${JSON.stringify(edge.id)}]`,
      label: elementName('edges', edge, run),
    });

    return false;
  });

  if (kept.length !== doc.edges.length) {
    doc.edges = kept;
  }
}

/**
 * What a merge says of the connections it dropped (see mergeDocuments):
 * "Dropped the connection from bravo to EXP: the device or interface it
 * connects was deleted."
 *
 * @param {{label: string}[]} [dropped]
 * @returns {string} '' when it dropped none
 */
export function droppedText(dropped = []) {
  if (!dropped.length) {
    return '';
  }

  const which = listOf(dropped.map((entry) => `the ${entry.label}`));

  return dropped.length === 1
    ? `Dropped ${which}: the device or interface it connects was deleted.`
    : `Dropped ${which}: the devices or interfaces they connect were deleted.`;
}

/**
 * Merges the changes two versions made to a document since the version they
 * both started from (see the rules at the top of this file).
 *
 * @param {object} base the version both started from
 * @param {object} mine the version on screen
 * @param {object} theirs the server's version
 * @param {Object<string, 'mine'|'theirs'>} [choices] the version kept of
 *   each clash, by its key; a clash without one keeps theirs
 * @returns {{doc: object, clashes: object[], mineChanges: object[],
 *   theirChanges: object[], dropped: object[], base: object, mine: object,
 *   theirs: object}} the merged document; each clash, with a stable key,
 *   its element and field, a label (element and field), the base, mine and
 *   theirs values, how each choice shows its value (mineText, theirsText)
 *   and a summary; the changes taken from each side ({key, label}); the
 *   connections dropped because the choices deleted what they connect
 *   ({key, label}, see droppedText); and the three versions, which
 *   applyChoices merges again
 */
export function mergeDocuments(base, mine, theirs, choices = {}) {
  const [b, m, t] = [base, mine, theirs].map((doc) =>
    isObject(doc) ? doc : {},
  );
  const run = createRun(choices, [b, m, t]);
  const doc = {};
  const dropped = [];

  unique([...Object.keys(t), ...Object.keys(m), ...Object.keys(b)]).forEach(
    (key) => {
      const value = mergeRoot(key, b, m, t, run);
      const empty =
        (Array.isArray(value) && value.length === 0) ||
        (isObject(value) && Object.keys(value).length === 0);

      if (value !== undefined && !(empty && OMITTED_EMPTY.has(key))) {
        doc[key] = value;
      }
    },
  );

  dropDangling(doc, run, dropped);

  return {
    doc,
    clashes: run.clashes,
    mineChanges: run.mine,
    theirChanges: run.theirs,
    dropped,
    base,
    mine,
    theirs,
  };
}

/**
 * The merged document with the version chosen for each clash.
 *
 * @param {object} result mergeDocuments() result
 * @param {Object<string, 'mine'|'theirs'>} choices by clash key; a clash
 *   without one keeps theirs
 * @returns {object} document
 */
export function applyChoices(result, choices = {}) {
  return mergeDocuments(result.base, result.mine, result.theirs, choices).doc;
}

/**
 * Who a merge takes changes from, as the editor names them: the user who
 * saved the server's version, "another tab" when that is the user, or
 * "another editor" when the server did not say who.
 *
 * @param {string} by who saved the server's version
 * @param {string} me the signed-in user
 * @returns {string}
 */
export function changesFrom(by, me) {
  if (by && by === me) {
    return 'another tab';
  }

  return by || 'another editor';
}

/**
 * @param {string} from changesFrom() result
 * @returns {string} "bob's", "another tab's"
 */
export function possessive(from) {
  return `${from}'s`;
}
