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
// - Within an element, objects merge key by key. Lists whose items all carry
//   a unique id, or else a unique name (a device's interface handles and
//   its spec's interfaces), merge item by item the same way. Any other list
//   (a line's points, an edge's route, the diagram's notes), a node's
//   position and its size, and every scalar are one value each: they clash
//   when both sides changed them to different values.
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
//   so it never clashes.
//
// The functions are pure: they never change their arguments.

import { count } from './announce.js';
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

// The field the items of the lists are matched by: 'id', else 'name', else
// '' when the lists are one value each.
function matchField(lists) {
  return (
    ['id', 'name'].find((field) =>
      lists.every((list) => uniqueBy(list, field)),
    ) || ''
  );
}

function words(segment) {
  return String(segment)
    .replace(/_/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .toLowerCase();
}

// What a field is called, from its path within its element. A path segment
// that is an item's id or name ({item}) is shown as it is.
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

  return path
    .filter(
      (segment, index) =>
        !(index === 0 && PAYLOADS.has(segment)) && segment !== 'spec',
    )
    .map((segment) => (isObject(segment) ? segment.item : words(segment)))
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

// One merge: the choices it applies, and what it found.
function createRun(choices, docs) {
  const nodes = new Map();

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

    note(side, at) {
      this[side].push({ key: at.key, label: nameOf(at) });
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

  if (mine === undefined && base !== undefined) {
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

// The position of the item `id` of a list at `at`: an element of its own
// in a document list, else a part of the element the list is in.
function itemAt(at, id, item, run) {
  const key = `${at.key}[${JSON.stringify(id)}]`;

  if (at.list) {
    return {
      key,
      root: key,
      element: elementName(at.list, item, run),
      kind: at.list === 'nodes' ? item?.kind : '',
      path: [],
      atomic: false,
      derived: false,
      scope: at.list === 'nodes' ? 'node' : '',
      isElement: true,
    };
  }

  return {
    ...at,
    key,
    path: [...at.path, { item: id }],
    atomic: false,
    derived: false,
    scope: '',
    isElement: false,
  };
}

function mergeValue(base, mine, theirs, at, run) {
  if (sameValue(mine, theirs)) {
    return mine;
  }

  if (sameValue(base, mine)) {
    run.note('theirs', at);

    return theirs;
  }

  if (sameValue(base, theirs)) {
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
      const field = matchField([base || [], mine, theirs]);

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
    const kept = mine === undefined ? theirs : mine;

    if (sameValue(base, kept)) {
      run.note(mine === undefined ? 'mine' : 'theirs', at);

      return undefined;
    }

    return run.clash(base, mine, theirs, at);
  }

  if (at.isElement && at.kind === 'device') {
    return mergeValue(
      ...settleDeviceName(base, mine, theirs, at, run),
      at,
      run,
    );
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
      itemAt(at, id, item, run),
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
 *   theirChanges: object[], base: object, mine: object, theirs: object}}
 *   the merged document; each clash, with a stable key, its element and
 *   field, a label (element and field), the base, mine and theirs values,
 *   how each choice shows its value (mineText, theirsText) and a summary;
 *   the changes taken from each side ({key, label}); and the three versions,
 *   which applyChoices merges again
 */
export function mergeDocuments(base, mine, theirs, choices = {}) {
  const [b, m, t] = [base, mine, theirs].map((doc) =>
    isObject(doc) ? doc : {},
  );
  const run = createRun(choices, [b, m, t]);
  const doc = {};

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

  return {
    doc,
    clashes: run.clashes,
    mineChanges: run.mine,
    theirChanges: run.theirs,
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
