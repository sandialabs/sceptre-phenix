import { reactive } from 'vue';

import { readPref, writePref } from '@/utils/prefs.js';

// Which of a table's columns show, each remembered for the user under
// "<username>.<storageKey>". `toggles` lists { key, label, storageKey };
// every column shows until the user hides it. Call within setup().
export function useColumnVisibility(toggles, username) {
  const storageKey = (toggle) => `${username()}.${toggle.storageKey}`;
  const columnVisibility = reactive(
    Object.fromEntries(toggles.map((toggle) => [toggle.key, true])),
  );
  for (const toggle of toggles) {
    const value = readPref(storageKey(toggle));
    if (value !== null) columnVisibility[toggle.key] = value === 'true';
  }
  return {
    columnToggles: toggles,
    columnVisibility,
    persistColumnVisibility(toggle) {
      writePref(storageKey(toggle), columnVisibility[toggle.key]);
    },
  };
}
