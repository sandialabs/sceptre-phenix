import { computed, ref } from 'vue';

export const DEFAULT_THEME_STORAGE_KEY = 'phenix.theme';
export const THEME_SYSTEM = 'system';
export const THEME_LIGHT = 'light';
export const THEME_DARK = 'dark';

export function normalizeDefaultTheme(value) {
  return [THEME_SYSTEM, THEME_LIGHT, THEME_DARK].includes(value)
    ? value
    : THEME_SYSTEM;
}

export function normalizeLocalTheme(value) {
  return [THEME_LIGHT, THEME_DARK].includes(value) ? value : null;
}

export function resolveTheme(localTheme, defaultTheme, systemPrefersDark) {
  const requestedTheme =
    normalizeLocalTheme(localTheme) || normalizeDefaultTheme(defaultTheme);

  if (requestedTheme === THEME_SYSTEM) {
    return systemPrefersDark ? THEME_DARK : THEME_LIGHT;
  }

  return requestedTheme;
}

function createThemeManager(browser, root) {
  const mediaQuery = browser.matchMedia('(prefers-color-scheme: dark)');
  const defaultTheme = ref(
    normalizeDefaultTheme(browser.__PHENIX_DEFAULT_THEME__),
  );
  const localTheme = ref(null);
  const systemPrefersDark = ref(mediaQuery.matches);
  const activeTheme = computed(() =>
    resolveTheme(localTheme.value, defaultTheme.value, systemPrefersDark.value),
  );

  function applyTheme() {
    root.dataset.theme = activeTheme.value;
    root.style.colorScheme = activeTheme.value;
  }

  function readLocalTheme() {
    try {
      const storedTheme = browser.localStorage.getItem(
        DEFAULT_THEME_STORAGE_KEY,
      );
      localTheme.value = normalizeLocalTheme(storedTheme);

      if (storedTheme !== null && localTheme.value === null) {
        browser.localStorage.removeItem(DEFAULT_THEME_STORAGE_KEY);
      }
    } catch (error) {
      console.warn('Unable to read the local theme preference', error);
    }
  }

  function setLocalTheme(theme) {
    const normalizedTheme = normalizeLocalTheme(theme);
    if (normalizedTheme === null) {
      throw new Error(`Unsupported local theme: ${theme}`);
    }

    localTheme.value = normalizedTheme;
    applyTheme();

    try {
      browser.localStorage.setItem(DEFAULT_THEME_STORAGE_KEY, normalizedTheme);
    } catch (error) {
      throw new Error('Theme changed, but the preference could not be saved', {
        cause: error,
      });
    }
  }

  function toggleTheme() {
    setLocalTheme(activeTheme.value === THEME_DARK ? THEME_LIGHT : THEME_DARK);
  }

  function setDefaultTheme(theme) {
    defaultTheme.value = normalizeDefaultTheme(theme);
    applyTheme();
  }

  function handleSystemThemeChange(event) {
    systemPrefersDark.value = event.matches;
    if (localTheme.value === null && defaultTheme.value === THEME_SYSTEM) {
      applyTheme();
    }
  }

  function handleStorage(event) {
    if (event.key === DEFAULT_THEME_STORAGE_KEY) {
      localTheme.value = normalizeLocalTheme(event.newValue);
      applyTheme();
    }
  }

  readLocalTheme();
  applyTheme();
  mediaQuery.addEventListener('change', handleSystemThemeChange);
  browser.addEventListener('storage', handleStorage);

  if (browser.__PHENIX_THEME_STORAGE_ERROR__) {
    console.warn(
      'Unable to read the local theme preference',
      browser.__PHENIX_THEME_STORAGE_ERROR__,
    );
  }

  return {
    activeTheme,
    defaultTheme,
    localTheme,
    setDefaultTheme,
    setLocalTheme,
    toggleTheme,
  };
}

let themeManager;

export function initializeTheme() {
  if (!themeManager) {
    themeManager = createThemeManager(window, document.documentElement);
  }

  return themeManager;
}

export function useTheme() {
  return initializeTheme();
}
