// Full configs, as the Configs page's viewer and editor load them, so opening
// a config, then editing it, fetches it once. The config list only carries
// metadata; a cached copy is used while the list's revision for it (or, for a
// config not written since revisions were added, its `updated` time, which
// has whole seconds) is unchanged. Cleared on logout with the other page data.
import { getConfig } from '@/api/configs.js';

const cache = new Map(); // "Kind/name" -> { version, promise }

export const configKey = (cfg) => `${cfg.kind}/${cfg.metadata.name}`;

// Resolves to a copy of the full config for a config list row, so callers
// may change it freely.
export async function fullConfig(cfg) {
  const key = configKey(cfg);
  const version = cfg.metadata.revision
    ? `r${cfg.metadata.revision}`
    : cfg.metadata.updated;

  let entry = cache.get(key);
  if (!entry || entry.version !== version) {
    const promise = getConfig(key).then((resp) => resp.data);
    entry = { version, promise };
    cache.set(key, entry);

    // a failed load is not worth keeping
    promise.catch(() => {
      if (cache.get(key) === entry) cache.delete(key);
    });
  }

  return structuredClone(await entry.promise);
}

// Starts loading a config the user is likely to open, such as one under the
// pointer. Failures are reported when the config is actually opened.
export function prefetchConfig(cfg) {
  fullConfig(cfg).catch(() => {});
}

export function forgetConfig(cfg) {
  cache.delete(configKey(cfg));
}

export function clearConfigCache() {
  cache.clear();
}

// The Configs page with the config `kind`/`name` open in its viewer.
export const configViewRoute = (kind, name) => ({
  name: 'configs',
  query: { view: `${kind}/${name}` },
});

// The listed config a viewer link names as Kind/name, or null; a name that
// differs only in case matches when no name matches exactly.
export function findConfig(configs, key) {
  const match = /^([A-Za-z]+)\/(.+)$/s.exec(typeof key === 'string' ? key : '');
  if (!match) return null;
  const [, kind, name] = match;
  const ofKind = configs.filter((cfg) => cfg.kind === kind);
  return (
    ofKind.find((cfg) => cfg.metadata.name === name) ??
    ofKind.find(
      (cfg) => cfg.metadata.name.toLowerCase() === name.toLowerCase(),
    ) ??
    null
  );
}
