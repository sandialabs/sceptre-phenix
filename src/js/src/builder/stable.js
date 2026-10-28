// Derived lists that keep their unchanged items.
//
// The canvas rebuilds its items from the document after every edit. An item
// equal to the one it replaces stays that very object, so Vue redraws only
// the items the edit changed.

/** Whether two values are deeply equal. Only arrays and plain objects compare by content. */
export function sameValue(a, b) {
  if (Object.is(a, b)) {
    return true;
  }

  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((value, index) => sameValue(value, b[index]))
    );
  }

  if (!isPlain(a) || !isPlain(b)) {
    return false;
  }

  const keys = Object.keys(a);

  return (
    keys.length === Object.keys(b).length &&
    keys.every((key) => Object.hasOwn(b, key) && sameValue(a[key], b[key]))
  );
}

function isPlain(value) {
  if (!value || typeof value !== 'object') {
    return false;
  }

  const proto = Object.getPrototypeOf(value);

  return proto === Object.prototype || proto === null;
}

// The next items, each equal item kept as the previous object of its id.
// Returns the previous list itself when no item changed.
export function keepUnchanged(next, previous) {
  if (!Array.isArray(previous)) {
    return next;
  }

  const before = new Map(previous.map((item) => [item?.id, item]));
  let same = next.length === previous.length;

  const kept = next.map((item, index) => {
    const old = before.get(item?.id);
    const result = old && sameValue(item, old) ? old : item;

    same = same && result === previous[index];

    return result;
  });

  return same ? previous : kept;
}
