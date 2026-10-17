// The outline of an Inspector form: what names its sections and list
// entries, which of them the user collapsed, and the table of contents the
// Node Editor builds from them (see dialogs/NodeEditorDialog.vue).
//
// A section, list or entry is known by its JSON Forms data path, such as
// "spec.network.interfaces.0". The functions here are pure, so the
// renderers (components/builder/inspector/) and the tests share them.

// Fields whose value best names a list entry in its header.
const SUMMARY_KEYS = [
  'name',
  'hostname',
  'image',
  'destination',
  'network',
  'src',
  'description',
];

// The longest summary an entry header shows, in characters.
const SUMMARY_LENGTH = 40;

/**
 * The text that names a list entry after its number: the value of the
 * first field of SUMMARY_KEYS that it sets, or the entry itself for a
 * list of text or numbers. Long text is cut short with an ellipsis.
 *
 * @param {unknown} item
 * @returns {string} '' when nothing names the entry
 */
export function itemSummary(item) {
  const summary =
    item && typeof item === 'object'
      ? SUMMARY_KEYS.map((key) => item[key]).find(
          (value) => typeof value === 'string' && value.trim(),
        )
      : item;
  const text = summary === undefined || summary === null ? '' : `${summary}`;

  return text.length > SUMMARY_LENGTH
    ? `${text.slice(0, SUMMARY_LENGTH - 1)}…`
    : text;
}

/**
 * The header of a list entry: "Drive 1: ubuntu.qc2", or "Drive 1" when
 * nothing names it.
 *
 * @param {string} name the entry's name, with its first letter a capital
 * @param {unknown} item the entry's data
 * @returns {string}
 */
export function entryTitle(name, item) {
  const summary = itemSummary(item);

  return summary ? `${name}: ${summary}` : name;
}

/**
 * The field's path in the phenix node spec, as a topology's YAML has it:
 * the data path without its leading "spec.", with list indexes as numbers
 * between dots ("network.interfaces.0.address"). Fields that are not part
 * of the spec (the Builder's own fields, such as colors and the layer)
 * have none.
 *
 * @param {string} path the field's JSON Forms data path
 * @returns {string} '' for a field outside the spec, or the spec itself
 */
export function specFieldPath(path) {
  const text = String(path ?? '');

  return text.startsWith('spec.') ? text.slice('spec.'.length) : '';
}

/**
 * @param {string} path a section, list or entry
 * @param {string} other a field's path
 * @returns {boolean} whether the field is the section or inside it
 */
export function pathContains(path, other) {
  return other === path || String(other).startsWith(`${path}.`);
}

/**
 * @param {string|string[]} paths a section, list or entry, or several
 *   (the fields of a section that holds them)
 * @param {Iterable<string>} errorPaths the paths of the fields with errors
 * @returns {boolean} whether a field with an error is inside it
 */
export function hasErrorUnder(paths, errorPaths) {
  const scopes = [paths].flat();

  return [...(errorPaths || [])].some((error) =>
    scopes.some((scope) => pathContains(scope, error)),
  );
}

// The paths that are an entry of the list at `listPath`, or inside one,
// split into the entry's index and the rest of the path. Others give null.
function entryOf(listPath, path) {
  const prefix = `${listPath}.`;

  if (!path.startsWith(prefix)) {
    return null;
  }

  const [index, ...rest] = path.slice(prefix.length).split('.');

  return /^\d+$/.test(index) ? { index: Number(index), rest } : null;
}

function renumbered(paths, listPath, move) {
  const next = new Set();

  for (const path of paths) {
    const entry = entryOf(listPath, path);

    if (!entry) {
      next.add(path);
      continue;
    }

    const index = move(entry.index);

    if (index !== null) {
      next.add([listPath, index, ...entry.rest].join('.'));
    }
  }

  return next;
}

/**
 * The collapsed paths once an entry is added to a list at `index`: the
 * entries after it move down one. The new entry is expanded.
 *
 * @param {Iterable<string>} paths the collapsed paths
 * @param {string} listPath
 * @param {number} index
 * @returns {Set<string>}
 */
export function afterInsert(paths, listPath, index) {
  return renumbered(paths, listPath, (at) => (at >= index ? at + 1 : at));
}

/**
 * The collapsed paths once the entry at `index` is removed from a list:
 * its own go, and the entries after it move up one.
 *
 * @param {Iterable<string>} paths
 * @param {string} listPath
 * @param {number} index
 * @returns {Set<string>}
 */
export function afterRemove(paths, listPath, index) {
  return renumbered(paths, listPath, (at) => {
    if (at === index) {
      return null;
    }

    return at > index ? at - 1 : at;
  });
}

/**
 * The collapsed paths once two entries of a list change places, as Move up
 * and Move down do: each entry keeps its own state.
 *
 * @param {Iterable<string>} paths
 * @param {string} listPath
 * @param {number} from
 * @param {number} to
 * @returns {Set<string>}
 */
export function afterMove(paths, listPath, from, to) {
  return renumbered(paths, listPath, (at) => {
    if (at === from) {
      return to;
    }

    return at === to ? from : at;
  });
}

/**
 * The table of contents from the outline entries of a form, in document
 * order: each entry under the nearest entry before it that is one level
 * up. Entries deeper than `maxDepth` are left out.
 *
 * @param {{key: string, depth: number}[]} entries `depth` is 0 for a
 *   section, 1 for a list or a group in it, 2 for an entry of that list
 * @param {number} [maxDepth]
 * @returns {object[]} the entries of depth 0, each with `children`
 */
export function outlineTree(entries, maxDepth = 2) {
  const roots = [];
  const stack = [];

  for (const entry of entries) {
    if (entry.depth > maxDepth) {
      continue;
    }

    const node = { ...entry, children: [] };

    while (stack.length && stack[stack.length - 1].depth >= entry.depth) {
      stack.pop();
    }

    (stack.length ? stack[stack.length - 1].children : roots).push(node);
    stack.push(node);
  }

  return roots;
}

/**
 * The entry the table of contents marks as in view: of the entries whose
 * box is in the band at the top of the scrolling body, the last in
 * document order, which is the innermost.
 *
 * @param {string[]} keys the entries in document order
 * @param {Set<string>} visible the keys of the entries in the band
 * @returns {string} '' when none is
 */
export function activeEntry(keys, visible) {
  return keys.findLast((key) => visible.has(key)) ?? '';
}
