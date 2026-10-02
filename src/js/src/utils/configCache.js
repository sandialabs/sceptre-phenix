// Full configs, as the Configs page's viewer and editor load them, so opening
// a config, then editing it, fetches it once. The config list only carries
// metadata; a cached copy is used while the list's `updated` time for it is
// unchanged. Cleared on logout with the other page data.
import axiosInstance from '@/utils/axios.js';

const cache = new Map(); // "Kind/name" -> { updated, promise }

export const configKey = (cfg) => `${cfg.kind}/${cfg.metadata.name}`;

// Resolves to a copy of the full config for a config list row, so callers
// may change it freely.
export async function fullConfig(cfg) {
  const key = configKey(cfg);
  const updated = cfg.metadata.updated;

  let entry = cache.get(key);
  if (!entry || entry.updated !== updated) {
    const promise = axiosInstance
      .get(`configs/${key}`, { headers: { Accept: 'application/json' } })
      .then((resp) => resp.data);
    entry = { updated, promise };
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
