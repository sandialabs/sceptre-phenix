// The running VMs that the Add VM panel of the console offers, as exp/vm
// keys: kept in memory for the page and in localStorage for each user, so
// the panel shows them at once, also after a reload, while it reads them
// again. The `vncvms` entry of utils/freshness.js describes this cache;
// logout clears it.

const STORAGE_PREFIX = 'phenix.vnc.runningVms.';

// the keys of this page, by user
const memory = new Map();

const storageKey = (username) => STORAGE_PREFIX + (username ?? '');

const isKeyList = (value) =>
  Array.isArray(value) && value.every((key) => typeof key === 'string');

/**
 * The running VMs that were last read for the user, or null when none are
 * known.
 *
 * @param {string} username
 * @returns {string[]|null}
 */
export function cachedRunningVms(username) {
  if (memory.has(username)) return memory.get(username);
  let stored = null;
  try {
    stored = JSON.parse(localStorage.getItem(storageKey(username)) ?? 'null');
  } catch {
    // a browser with no storage, or a value that is not JSON, keeps none
  }
  if (!isKeyList(stored)) return null;
  memory.set(username, stored);
  return stored;
}

/**
 * Keeps the running VMs that were read for the user.
 *
 * @param {string} username
 * @param {string[]} keys
 */
export function cacheRunningVms(username, keys) {
  memory.set(username, keys);
  try {
    localStorage.setItem(storageKey(username), JSON.stringify(keys));
  } catch {
    // the page keeps them in memory
  }
}

/**
 * The exp/vm keys of the running VMs in a GET /vms answer, sorted.
 *
 * @param {Array<{ experiment: string, name: string, running?: boolean }>} vms
 * @returns {string[]}
 */
export function runningVmKeys(vms) {
  return (vms ?? [])
    .filter((vm) => vm.running)
    .map((vm) => `${vm.experiment}/${vm.name}`)
    .sort((a, b) => a.localeCompare(b));
}

/** Forgets the running VMs of every user, in memory and in storage. */
export function clearRunningVmsCache() {
  memory.clear();
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (key?.startsWith(STORAGE_PREFIX)) localStorage.removeItem(key);
    }
  } catch {
    // nothing is stored
  }
}
