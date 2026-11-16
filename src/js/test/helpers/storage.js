// Stand-ins for localStorage.

// Storage that keeps values as strings, as the browser does. `initial` gives
// its first items, and `entries` holds what it stores.
export function memoryStorage(initial = {}) {
  const entries = new Map(Object.entries(initial));
  return {
    entries,
    get length() {
      return entries.size;
    },
    key: (index) => [...entries.keys()][index] ?? null,
    getItem: (key) => (entries.has(key) ? entries.get(key) : null),
    setItem: (key, value) => {
      entries.set(key, String(value));
    },
    removeItem: (key) => {
      entries.delete(key);
    },
    clear: () => entries.clear(),
  };
}

// Storage the browser refuses, as when site data is blocked.
export function blockedStorage() {
  const blocked = () => {
    throw new Error('blocked');
  };
  return { getItem: blocked, setItem: blocked, removeItem: blocked };
}
