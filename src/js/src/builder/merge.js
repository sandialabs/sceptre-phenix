// Three-way merge of Builder documents: base, mine (on screen) and theirs (the
// server's). A change on one side is taken. A field that both sides changed to
// different values is a clash for MergeDialog.vue. The functions are pure.
// skills/phenix/references/builder/saving.md has the merge rules.

import { count, listOf } from './announce.js';
import { iconLabel } from './iconLabels.js';
import {
  layersProblem,
  nodeLabel,
  normalizedLayers,
  placeholderLayers,
} from './model.js';
import { isPlainObject } from './values.js';

// The server sets these on every save. They come from theirs.
const STAMPED = ['id', 'createdBy', 'createdAt', 'updatedBy', 'updatedAt'];

// The element lists of a document, matched by id.
const ELEMENT_LISTS = ['nodes', 'networks', 'edges', 'templates'];

// Optional document keys left out when empty, as the server writes them.
const OMITTED_EMPTY = new Set(['templates', 'scenarios', 'layers', 'icons']);

// A node's keys that are one value each.
const ATOMIC_NODE_KEYS = new Set(['position', 'size']);

// A template's keys that are one value each: its source names one version
// of one library template, with the digest of that version's copy.
const ATOMIC_TEMPLATE_KEYS = new Set(['source']);

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

// A device's fields that a rename changes together (see updateNode in
// model.js): one choice, named "name".
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

// What the spec interfaces of a device in a merge match by: the id of the
// handle of the same name, or "name:" and the name where no handle has it.
// It is a symbol, so sameIgnoringUndefined and a merged object leave it
// out, and the merged device loses it (see untagSpecInterfaces).
const HANDLE = Symbol('handle');

// What a field is called, by its path within its element, where the path
// alone would not say it plainly.
const FIELD_LABELS = {
  parentId: 'group',
  'device.iconKey': 'icon',
  'device.spec.general.description': 'description',
  'switch.networkId': 'network',
  'note.text': 'text',
  'group.title': 'title',
  sourceNodeId: 'device',
  sourceHandleId: 'interface',
  targetNodeId: 'switch',
  targetHandleId: 'switch connection point',
  // A template's link to the library template it was copied from.
  source: 'library link',
  networkId: 'network',
  iconSize: 'icon size',
};

function unique(list) {
  return [...new Set(list)];
}

/** Whether two JSON values are the same. An undefined value is no key. */
export function sameIgnoringUndefined(a, b) {
  if (a === b) {
    return true;
  }

  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, index) => sameIgnoringUndefined(item, b[index]))
    );
  }

  if (!isPlainObject(a) || !isPlainObject(b)) {
    return false;
  }

  const keys = (value) =>
    Object.keys(value).filter((key) => value[key] !== undefined);
  const own = keys(a);

  return (
    own.length === keys(b).length &&
    own.every((key) => sameIgnoringUndefined(a[key], b[key]))
  );
}

// Whether every item of `list` is an object with its own value of `field`,
// a text or a number that no other item has.
function uniqueBy(list, field) {
  const seen = new Set();

  return list.every((item) => {
    const key = isPlainObject(item) ? item[field] : undefined;
    const usable =
      (typeof key === 'string' && key !== '') || Number.isFinite(key);

    if (!usable || seen.has(key)) {
      return false;
    }

    seen.add(key);

    return true;
  });
}

// The field by which the items of the lists match: the first of `fields`
// for which every item has a unique value, or '' when each list is one
// value.
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

// The name of a path segment that is an item of a list ({item, label}): the
// item's name where it has one, or else the id or name it matches by.
function itemLabel(segment) {
  return segment.label ?? segment.item;
}

// What a field is called, from its path within its element. A path segment
// that is an item ({item}) is shown by its name (see itemLabel), and an
// interface handle of a device as "interface eth0".
function fieldLabel(path) {
  const plain = path.map((segment) =>
    isPlainObject(segment) ? `[${segment.item}]` : segment,
  );
  const joined = plain.join('.');

  if (DEVICE_NAME_FIELDS.has(joined)) {
    return 'name';
  }

  if (FIELD_LABELS[joined]) {
    return FIELD_LABELS[joined];
  }

  const [payload, list, handle, ...rest] = path;

  if (payload === 'device' && list === 'interfaces' && isPlainObject(handle)) {
    return [
      `interface ${itemLabel(handle)}`,
      ...rest.map((segment) =>
        isPlainObject(segment) ? itemLabel(segment) : words(segment),
      ),
    ].join(' ');
  }

  return path
    .filter(
      (segment, index) =>
        !(index === 0 && PAYLOADS.has(segment)) && segment !== 'spec',
    )
    .map((segment) =>
      isPlainObject(segment) ? itemLabel(segment) : words(segment),
    )
    .join(' ');
}

// The most characters of a value that a choice shows, the ellipsis included.
const SHORT_TEXT_MOST = 80;

function shorten(text) {
  const flat = String(text).replace(/\s+/g, ' ').trim();

  return flat.length > SHORT_TEXT_MOST
    ? `${flat.slice(0, SHORT_TEXT_MOST - 1)}…`
    : flat;
}

function rounded(value) {
  return Math.round(Number(value) * 100) / 100;
}

// A value as a merge dialog choice shows it: a text cut after 80 characters,
// "x 120, y 40", "160 wide, 80 high", or a list joined by commas or counted.
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
  if (sameIgnoringUndefined(a, b)) {
    return found;
  }

  if (isPlainObject(a) && isPlainObject(b)) {
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
        ...(ATOMIC_NODE_KEYS.has(path[0]) ||
        (at.scope === 'template' && ATOMIC_TEMPLATE_KEYS.has(path[0]))
          ? [path[0]]
          : path),
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

// What the connections that `doc` added or changed since `base` use: how
// many of them end at each node, and at each handle of a node (see endKey).
function connectionsOf(doc, base) {
  const before = new Map(
    (Array.isArray(base?.edges) ? base.edges : [])
      .filter(isPlainObject)
      .map((edge) => [edge.id, edge]),
  );
  const uses = new Map();
  const add = (key) => uses.set(key, (uses.get(key) || 0) + 1);

  (Array.isArray(doc?.edges) ? doc.edges : []).forEach((edge) => {
    if (
      !isPlainObject(edge) ||
      sameIgnoringUndefined(before.get(edge.id), edge)
    ) {
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

// What a connection can use at `at`: a node element's node, or a device's
// interface, as its handle or as its spec entry matched by the handle's id.
// key is its endKey. at is the position of the clash about it. A spec
// entry's clash is the clash of its handle, so the interface is one choice.
function connectionTarget(at) {
  if (at.nodeId === undefined) {
    return null;
  }

  if (at.isElement) {
    return { key: endKey(at.nodeId), at };
  }

  const last = at.path[at.path.length - 1];
  const list = at.path.slice(0, -1).join('.');

  if (!isPlainObject(last)) {
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

// The nodes of `doc` by id.
function nodesById(doc) {
  return new Map(
    (Array.isArray(doc?.nodes) ? doc.nodes : [])
      .filter(isPlainObject)
      .map((node) => [node.id, node]),
  );
}

// The nodes that `doc` put in each group since `base`: those it added in
// the group, and those it moved into the group, by the group's id.
function membersOf(doc, base) {
  const before = nodesById(base);
  const members = new Map();

  nodesById(doc).forEach((node, id) => {
    const parent = node.parentId;

    if (!parent || before.get(id)?.parentId === parent) {
      return;
    }

    members.set(parent, [...(members.get(parent) || []), id]);
  });

  return members;
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
    nodes,
    versions: {
      base: nodesById(base),
      mine: nodesById(mine),
      theirs: nodesById(theirs),
    },
    connections: {
      mine: connectionsOf(mine, base),
      theirs: connectionsOf(theirs, base),
    },
    members: {
      mine: membersOf(mine, base),
      theirs: membersOf(theirs, base),
    },

    // The position of the clash about the group element at `at` when
    // `side` put nodes in it (see membersOf), with how many. null when it
    // did not.
    filled(side, at) {
      const placed = at.isElement ? this.members[side].get(at.nodeId) : null;

      return placed?.length
        ? { ...at, filled: { side, count: placed.length } }
        : null;
    },

    // The position of the clash about a node's group at `at` when one side
    // put the node in a group that the other side put inside the node (see
    // the rules at the top of this file). null when it did not.
    nests(base, mine, theirs, at) {
      if (
        at.nodeId === undefined ||
        at.path.length !== 1 ||
        at.path[0] !== 'parentId'
      ) {
        return null;
      }

      const side = sameIgnoringUndefined(base, theirs)
        ? 'mine'
        : sameIgnoringUndefined(base, mine)
          ? 'theirs'
          : '';
      const parent = side === 'mine' ? mine : theirs;

      if (!side || !parent) {
        return null;
      }

      // The groups that would hold the new group: where the other side
      // changed a group's group, its group, else the group in `side`.
      const other = this.versions[side === 'mine' ? 'theirs' : 'mine'];
      const own = this.versions[side];
      const groupOf = (id) => {
        const entry = other.get(id);
        const before = this.versions.base.get(id);

        return entry && entry.parentId !== before?.parentId
          ? entry.parentId
          : (own.get(id) ?? entry)?.parentId;
      };
      const seen = new Set();

      for (let id = parent; id && !seen.has(id); id = groupOf(id)) {
        if (id === at.nodeId) {
          return { ...at, nesting: { side, parent } };
        }

        seen.add(id);
      }

      return null;
    },

    // The position of the clash about the node or interface at `at` when
    // `side` added or changed connections that use it (see
    // connectionTarget), with how many. null when it did not.
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
        this.clashes.push(describeClash(key, base, mine, theirs, at, nodes));
      }

      return this.choices[key] === 'mine' ? mine : theirs;
    },
  };
}

// A value of the field at `path` as a choice shows it. An icon key shows
// its label, such as "Cell Tower" for cell-tower, and a node's group the
// group's name from `nodes`.
function choiceText(path, value, nodes) {
  if (path.length === 1 && path[0] === 'parentId') {
    return value ? groupName(nodes, value) : 'no group';
  }

  return valueText(
    path.at(-1) === 'iconKey' && typeof value === 'string'
      ? iconLabel(value)
      : value,
  );
}

// What the group `id` is called, from `nodes`.
function groupName(nodes, id) {
  return nodeLabel(nodes.get(id)) || 'a removed group';
}

function describeClash(key, base, mine, theirs, at, nodes) {
  const field = fieldLabel(at.path);
  const label = nameOf(at);
  // An element is deleted, or a field is removed. What is kept of the other
  // side is the element with that side's changes, or the field's value.
  const [done, undo] = at.isElement
    ? ['deleted', 'delete']
    : ['removed', 'remove'];
  const kept = (value, whose) =>
    at.isElement
      ? `keep ${label} with ${whose} changes`
      : choiceText(at.path, value, nodes);
  let kind = 'change';
  let mineText = choiceText(at.path, mine, nodes);
  let theirsText = choiceText(at.path, theirs, nodes);
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
        ? `They ${done} ${label}. You ${change}connected it.`
        : `You ${done} ${label}. They ${change}connected it.`;
  } else if (at.filled) {
    // One side deleted the group, and the other put nodes in it, and maybe
    // changed it too (see filled in createRun): deleting it moves those
    // nodes out of it.
    const { side, count: placed } = at.filled;
    const whose = side === 'mine' ? 'your' : 'their';
    const members = `${whose} ${placed === 1 ? 'node' : count(placed, 'node')}`;
    const move = `${undo} ${label} and move ${members} out of it`;
    const keep = at.changed
      ? `keep ${label} with ${whose} changes`
      : `keep ${label} with ${members} in it`;
    const changed = whatChanged(at, base, side === 'mine' ? mine : theirs);
    const change = at.changed ? `changed ${changed} and ` : '';
    const put = `put ${count(placed, 'node')} in it`;

    kind = side === 'mine' ? 'removed-theirs' : 'removed-mine';
    mineText = side === 'mine' ? keep : move;
    theirsText = side === 'mine' ? move : keep;
    summary =
      side === 'mine'
        ? `They ${done} ${label}. You ${change}${put}.`
        : `You ${done} ${label}. They ${change}${put}.`;
  } else if (at.nesting) {
    // One side put the node in a group that the other side put inside the
    // node (see nests in createRun).
    const { side, parent } = at.nesting;
    const group = groupName(nodes, parent);
    const [doer, other] = side === 'mine' ? ['You', 'They'] : ['They', 'You'];

    summary = `${doer} put ${at.element} in ${group}. ${other} put ${group} inside ${at.element}. Builder cannot keep both.`;
  } else if (mine === undefined && base !== undefined) {
    kind = 'removed-mine';
    mineText = `${undo} ${label}`;
    theirsText = kept(theirs, 'their');
    summary = `You ${done} ${label}. They changed ${whatChanged(at, base, theirs)}.`;
  } else if (theirs === undefined && base !== undefined) {
    kind = 'removed-theirs';
    mineText = kept(mine, 'your');
    theirsText = `${undo} ${label}`;
    summary = `They ${done} ${label}. You changed ${whatChanged(at, base, mine)}.`;
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
    atomic:
      (at.scope === 'node' && ATOMIC_NODE_KEYS.has(key)) ||
      (at.scope === 'template' && ATOMIC_TEMPLATE_KEYS.has(key)),
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

// The position of the item `id` of a list at `at`, whose items match by
// `field`. In a document list, it is a separate element. Otherwise it is a
// part of the element that holds the list, named by the item's name where it
// has one.
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
      scope: { nodes: 'node', templates: 'template' }[at.list] || '',
      isElement: true,
    };
  }

  const name = isPlainObject(item) ? item.name : undefined;

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
// side did not change them, and does not take the other side's whole value.
// This occurs on the path to a device's interface lists when one side
// deleted an interface that the other side connected (see
// dropsConnectedInterface), so that mergeItem asks about it.
function itemwise(base, mine, theirs, at) {
  if (!at.forceItems || !onInterfacePath(at.path)) {
    return false;
  }

  if (isPlainObject(base) && isPlainObject(mine) && isPlainObject(theirs)) {
    return true;
  }

  return (
    [base, mine, theirs].every(Array.isArray) &&
    matchField([base, mine, theirs], [HANDLE, 'id', 'name']) !== ''
  );
}

function mergeValue(base, mine, theirs, at, run) {
  if (sameIgnoringUndefined(mine, theirs)) {
    return mine;
  }

  const whole = !itemwise(base, mine, theirs, at);

  if (whole && sameIgnoringUndefined(base, mine)) {
    return theirs;
  }

  if (whole && sameIgnoringUndefined(base, theirs)) {
    return mine;
  }

  if (at.derived) {
    return theirs;
  }

  if (!at.atomic) {
    if (
      isPlainObject(mine) &&
      isPlainObject(theirs) &&
      (base === undefined || isPlainObject(base))
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
    (part, key) => (isPlainObject(part) ? part[key] : undefined),
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

  if (!isPlainObject(value[key])) {
    return field === undefined
      ? value
      : { ...value, [key]: withValueAt({}, rest, field) };
  }

  return { ...value, [key]: withValueAt(value[key], rest, field) };
}

// The three versions of a device, with its name settled. When any field of
// the name (DEVICE_NAME_FIELDS) clashes, that is one clash, described by the
// first field that clashes. Every version gets all three fields as the side
// that the choice keeps has them (theirs until the user makes a choice).
// Thus the rest of the merge takes the whole name from that side.
function settleDeviceName(base, mine, theirs, at, run) {
  const versions = [base, mine, theirs];
  const clashing = DEVICE_NAME_PATHS.find((path) => {
    const [b, m, t] = versions.map((version) => valueAt(version, path));

    return (
      !sameIgnoringUndefined(m, t) &&
      !sameIgnoringUndefined(b, m) &&
      !sameIgnoringUndefined(b, t)
    );
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
    isPlainObject(version) ? settle(version) : version,
  );
}

// The items of the list at `path` within `value` that are objects, or none.
function objectsAt(value, path) {
  const list = valueAt(value, path);

  return Array.isArray(list) ? list.filter(isPlainObject) : [];
}

// A copy of the device `version` with its interface `handleId` named
// `name`: the handle, and the spec entries of the old name of the handle
// (see reconcileDeviceHandles in model.js).
function withInterfaceName(version, handleId, name) {
  const handles = valueAt(version, HANDLES_PATH);
  const old = objectsAt(version, HANDLES_PATH).find(
    (handle) => handle.id === handleId,
  )?.name;

  if (!isPlainObject(version) || old === undefined || old === name) {
    return version;
  }

  const renamed = withValueAt(
    version,
    HANDLES_PATH,
    handles.map((handle) =>
      isPlainObject(handle) && handle.id === handleId
        ? { ...handle, name }
        : handle,
    ),
  );
  const specs = valueAt(renamed, SPEC_INTERFACES_PATH);

  return Array.isArray(specs)
    ? withValueAt(
        renamed,
        SPEC_INTERFACES_PATH,
        specs.map((iface) =>
          isPlainObject(iface) && iface.name === old
            ? { ...iface, name }
            : iface,
        ),
      )
    : renamed;
}

// Settles the interface names of a device's three versions. A handle whose
// name differs in all three is one clash. Every version takes the name of the
// kept side (theirs until a choice), so the merge keeps one interface.
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
      sameIgnoringUndefined(names[1], names[2]) ||
      sameIgnoringUndefined(names[0], names[1]) ||
      sameIgnoringUndefined(names[0], names[2])
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
// match by (HANDLE): the id of the handle of the same name, or NAME_KEY and
// the name where no handle has it.
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
      isPlainObject(iface) && typeof iface.name === 'string'
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
    !specs.some((iface) => isPlainObject(iface) && HANDLE in iface)
  ) {
    return node;
  }

  return withValueAt(
    node,
    SPEC_INTERFACES_PATH,
    specs.map((iface) => {
      if (!isPlainObject(iface) || !(HANDLE in iface)) {
        return iface;
      }

      const copy = { ...iface };

      delete copy[HANDLE];

      return copy;
    }),
  );
}

// The three versions of the node element at `at`. When one side put the
// node in a group that the other side put inside the node, its group is one
// clash (see nests in createRun), and every version gets the group of the
// side chosen.
function settleParent(versions, at, run) {
  if (!at.isElement || at.nodeId === undefined) {
    return versions;
  }

  const groups = versions.map((version) => version?.parentId);
  const position = childAt(at, 'parentId');
  const nesting = run.nests(...groups, position);

  if (!nesting) {
    return versions;
  }

  const parentId = run.clash(...groups, nesting);

  return versions.map((version) =>
    isPlainObject(version) ? withParent(version, parentId) : version,
  );
}

// One item of a matched list, or one entry of a map of elements.
function mergeItem(base, mine, theirs, at, run) {
  if (sameIgnoringUndefined(mine, theirs)) {
    return mine;
  }

  if (base === undefined) {
    if (mine === undefined) {
      return theirs;
    }

    if (theirs === undefined) {
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
        changed: connected.key === at.key && !sameIgnoringUndefined(base, kept),
      });
    }

    // Deleting a group that the other side put nodes in would leave them in
    // no group, so the user chooses here too.
    const filled = run.filled(keeper, at);

    if (filled) {
      return run.clash(base, mine, theirs, {
        ...filled,
        changed: !sameIgnoringUndefined(base, kept),
      });
    }

    if (!sameIgnoringUndefined(base, kept)) {
      return run.clash(base, mine, theirs, at);
    }

    return undefined;
  }

  const settled = settleParent([base, mine, theirs], at, run);

  if (at.isElement && at.kind === 'device') {
    const versions = settleInterfaceNames(
      settleDeviceName(...settled, at, run),
      at,
      run,
    ).map(tagSpecInterfaces);
    const position = dropsConnectedInterface(versions, at, run)
      ? { ...at, forceItems: true }
      : at;

    return untagSpecInterfaces(mergeValue(...versions, position, run));
  }

  return mergeValue(...settled, at, run);
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

// The merged scenarios: theirs without those mine removed, then those mine
// added. Names compare ignoring case, as the document holds each once that way.
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

// The layers of a document. A diagram without layers offers the
// placeholder layers, so an absent or empty list merges as those. Else a
// side that changes one placeholder writes all of them, and clashes with a
// side that changes another one. A result that is only the placeholders
// is empty again (see normalizedLayers).
function mergeLayers(base, mine, theirs, run) {
  const [b, m, t] = [base, mine, theirs].map((layers) =>
    layers === undefined || (Array.isArray(layers) && layers.length === 0)
      ? placeholderLayers()
      : layers,
  );
  const value = mergeValue(
    b,
    m,
    t,
    { key: 'layers', root: 'layers', element: 'Diagram', path: ['layers'] },
    run,
  );

  return layersProblem(value) ? value : normalizedLayers(value);
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

  if (key === 'layers') {
    return mergeLayers(base.layers, mine.layers, theirs.layers, run);
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

// The merged document's connections, without those whose node or device
// interface the document does not have because a choice deleted it. Each
// dropped connection is named ({key, label}) in `dropped`.
function dropDangling(doc, run, dropped) {
  if (!Array.isArray(doc.edges) || !Array.isArray(doc.nodes)) {
    return;
  }

  const nodes = new Map(
    doc.nodes.filter(isPlainObject).map((node) => [node.id, node]),
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
      !isPlainObject(edge) ||
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

// The node `node` in the group `parentId`, or in no group without one.
function withParent(node, parentId) {
  const moved = { ...node };

  if (parentId) {
    moved.parentId = parentId;
  } else {
    delete moved.parentId;
  }

  return moved;
}

// The first cycle of groups in `byId` that holds each other, as node ids,
// or null when there is none.
function parentCycle(byId) {
  const done = new Set();

  for (const start of byId.keys()) {
    const path = [];

    for (let id = start; id && byId.has(id) && !done.has(id); ) {
      const at = path.indexOf(id);

      if (at >= 0) {
        return path.slice(at);
      }

      path.push(id);
      id = byId.get(id).parentId;
    }

    path.forEach((id) => done.add(id));
  }

  return null;
}

// Moves each node whose group was deleted (reason 'deleted') or that is in a
// cycle of groups (reason 'nested'), and names it in `moved`.
function settleParents(doc, run, moved) {
  if (!Array.isArray(doc.nodes)) {
    return;
  }

  const byId = nodesById(doc);
  const { base, mine, theirs } = run.versions;
  const former = (id) => theirs.get(id) ?? mine.get(id) ?? base.get(id);
  const isGroup = (id) => byId.get(id)?.kind === 'group';
  const name = (id) => nodeLabel(byId.get(id) ?? former(id)) || 'a group';
  const changes = new Map();
  const move = (node, parentId, reason) => {
    moved.push({
      key: `nodes[${JSON.stringify(node.id)}]`,
      label: `${nodeLabel(node) || 'a node'} out of ${name(node.parentId)}`,
      reason,
    });
    changes.set(node.id, withParent(node, parentId));
    byId.set(node.id, changes.get(node.id));
  };

  byId.forEach((node) => {
    if (!node.parentId || isGroup(node.parentId)) {
      return;
    }

    const seen = new Set();
    let parent = node.parentId;

    while (parent && !isGroup(parent) && !seen.has(parent)) {
      seen.add(parent);
      parent = former(parent)?.parentId;
    }

    move(node, isGroup(parent) ? parent : '', 'deleted');
  });

  for (let cycle = parentCycle(byId); cycle; cycle = parentCycle(byId)) {
    const changed = cycle.filter(
      (id) => byId.get(id).parentId !== base.get(id)?.parentId,
    );
    const [id] = (changed.length ? changed : cycle).sort();

    move(byId.get(id), '', 'nested');
  }

  if (changes.size) {
    doc.nodes = doc.nodes.map((node) =>
      isPlainObject(node) && changes.has(node.id) ? changes.get(node.id) : node,
    );
  }
}

/** What a merge says about the connections it dropped, or '' for none. */
export function droppedText(dropped = []) {
  if (!dropped.length) {
    return '';
  }

  const which = listOf(dropped.map((entry) => `the ${entry.label}`));

  return dropped.length === 1
    ? `Dropped ${which}: someone deleted the device or interface that it connects.`
    : `Dropped ${which}: someone deleted the devices or interfaces that they connect.`;
}

/** What a merge says about the nodes it moved out of a group, or ''. */
export function movedText(moved = []) {
  const why = {
    deleted: (one) =>
      one ? 'someone deleted its group' : 'someone deleted their groups',
    nested: () => 'groups cannot be inside each other',
  };

  return Object.entries(why)
    .map(([reason, because]) => {
      const labels = moved
        .filter((entry) => entry.reason === reason)
        .map((entry) => entry.label);

      return labels.length
        ? `Moved ${listOf(labels)}: ${because(labels.length === 1)}.`
        : '';
    })
    .filter(Boolean)
    .join(' ');
}

/** What a merge says about dropped connections and moved nodes, or ''. */
export function mergeNotes({ dropped = [], moved = [] } = {}) {
  return [droppedText(dropped), movedText(moved)].filter(Boolean).join(' ');
}

// Merges the changes of mine and theirs since base. A clash without a choice
// in `choices` keeps theirs. The result holds the merged doc, the clashes, the
// dropped connections, the moved nodes and the three versions to merge again.
export function mergeDocuments(base, mine, theirs, choices = {}) {
  const [b, m, t] = [base, mine, theirs].map((doc) =>
    isPlainObject(doc) ? doc : {},
  );
  const run = createRun(choices, [b, m, t]);
  const doc = {};
  const dropped = [];
  const moved = [];

  unique([...Object.keys(t), ...Object.keys(m), ...Object.keys(b)]).forEach(
    (key) => {
      const value = mergeRoot(key, b, m, t, run);
      const empty =
        (Array.isArray(value) && value.length === 0) ||
        (isPlainObject(value) && Object.keys(value).length === 0);

      if (value !== undefined && !(empty && OMITTED_EMPTY.has(key))) {
        doc[key] = value;
      }
    },
  );

  settleParents(doc, run, moved);
  dropDangling(doc, run, dropped);

  return {
    doc,
    clashes: run.clashes,
    dropped,
    moved,
    base,
    mine,
    theirs,
  };
}

// Who a merge takes changes from: the user who saved theirs, "another tab"
// for the signed-in user, or "another editor" when the server did not say.
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

// Why the conflict panel asks for a review: the field clashes, or that the
// merged diagram cannot be saved. It counts fields, not changes, because the
// panel counts unsaved changes in another unit (one Apply).
export function clashNote(clashes, from) {
  if (clashes > 0) {
    return `You and ${from} changed ${count(clashes, 'field')} differently. Review and merge to choose which version to keep.`;
  }

  return `Builder cannot merge your changes and ${possessive(from)} as they are. Review and merge shows why.`;
}
