// The outline of an Inspector form: the names of its sections and list
// entries, which ones are collapsed, and the Node Editor's table of contents.
// Each part is known by its JSON Forms data path ("spec.network.interfaces.0").

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
 * The text that names a list entry after its number: its first SUMMARY_KEYS
 * value, or the entry itself for text or numbers, cut with an ellipsis.
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
 * The header of a list entry: "Drive 1: ubuntu.qc2", or "Drive 1" when nothing
 * names it.
 */
export function entryTitle(name, item) {
  const summary = itemSummary(item);

  return summary ? `${name}: ${summary}` : name;
}

/**
 * The field's path in the node spec, as a topology's YAML has it
 * ("network.interfaces.0.address"), or '' for a field outside the spec.
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
 * Whether a field with an error is inside the section, list or entry (or any of
 * several).
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
 * The collapsed paths once an entry is added at `index`. The new entry is
 * expanded.
 */
export function afterInsert(paths, listPath, index) {
  return renumbered(paths, listPath, (at) => (at >= index ? at + 1 : at));
}

/** The collapsed paths once the entry at `index` is removed from a list. */
export function afterRemove(paths, listPath, index) {
  return renumbered(paths, listPath, (at) => {
    if (at === index) {
      return null;
    }

    return at > index ? at - 1 : at;
  });
}

/**
 * The collapsed paths once two entries change places. Each entry keeps its own
 * state.
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
 * The table of contents from a form's outline entries, each under the
 * nearest entry before it one level up. Entries deeper than `maxDepth` are
 * left out.
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
 * The entry that the table of contents marks as in view: the last one in the
 * band at the top.
 */
export function activeEntry(keys, visible) {
  return keys.findLast((key) => visible.has(key)) ?? '';
}
