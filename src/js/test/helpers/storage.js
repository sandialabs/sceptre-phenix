// Stand-ins for localStorage.

// Storage that keeps values as strings, as the browser does.
export function memoryStorage() {
  const items = new Map();
  return {
    getItem: (key) => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => items.set(key, String(value)),
    removeItem: (key) => items.delete(key),
  };
}

// Storage the browser refuses, as when site data is blocked.
export function blockedStorage() {
  const blocked = () => {
    throw new Error('blocked');
  };
  return { getItem: blocked, setItem: blocked, removeItem: blocked };
}
