// Builder-only theme handling. The theme applies only to the Builder root
// element through a data attribute, so it never affects other views.

export const THEME_STORAGE_KEY = 'phenix.builder.theme';
const THEMES = ['system', 'light', 'dark'];
export const DEFAULT_THEME = 'system';

/**
 * @param {string} value
 * @returns {boolean}
 */
export function isValidTheme(value) {
  return THEMES.includes(value);
}

/** Reads the stored preference, or "system" when there is none. */
export function readStoredTheme(storage) {
  try {
    const value = storage?.getItem(THEME_STORAGE_KEY);
    return isValidTheme(value) ? value : DEFAULT_THEME;
  } catch {
    return DEFAULT_THEME;
  }
}

/** Stores the preference and returns it. A storage failure (private mode) is not an error. */
export function storeTheme(theme, storage) {
  const value = isValidTheme(theme) ? theme : DEFAULT_THEME;

  try {
    storage?.setItem(THEME_STORAGE_KEY, value);
  } catch {
    // ignore: the in-memory preference still applies for this session
  }

  return value;
}

/** The page's matchMedia bound to its window, or undefined outside a page. */
export function pageMatchMedia() {
  return typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia.bind(window)
    : undefined;
}

/** Resolves the preference to the concrete theme to render. */
export function resolveTheme(theme, matchMedia) {
  if (theme === 'light' || theme === 'dark') {
    return theme;
  }

  return prefersDark(matchMedia) ? 'dark' : 'light';
}

/**
 * @param {(query: string) => {matches: boolean}} [matchMedia]
 * @returns {boolean}
 */
export function prefersDark(matchMedia) {
  try {
    return Boolean(matchMedia?.('(prefers-color-scheme: dark)')?.matches);
  } catch {
    return false;
  }
}

/**
 * @param {(query: string) => {matches: boolean}} [matchMedia]
 * @returns {boolean}
 */
export function prefersReducedMotion(matchMedia) {
  try {
    return Boolean(matchMedia?.('(prefers-reduced-motion: reduce)')?.matches);
  } catch {
    return false;
  }
}

/** Applies the resolved theme to the Builder root element as data attributes. */
export function applyTheme(element, theme, matchMedia) {
  const resolved = resolveTheme(theme, matchMedia);

  if (element?.setAttribute) {
    element.setAttribute('data-builder-theme', resolved);
    element.setAttribute('data-builder-theme-preference', theme);
  }

  return resolved;
}

/** Watches the browser's color-scheme preference. Returns an unsubscribe function. */
export function watchSystemTheme(matchMedia, onChange) {
  try {
    const query = matchMedia?.('(prefers-color-scheme: dark)');

    if (!query?.addEventListener) {
      return () => {};
    }

    query.addEventListener('change', onChange);

    return () => query.removeEventListener('change', onChange);
  } catch {
    return () => {};
  }
}

/** The next theme of the toolbar toggle: the first press from System always changes the colors. */
export function nextTheme(theme, system = 'light') {
  const own = system === 'dark' ? 'dark' : 'light';
  const opposite = own === 'dark' ? 'light' : 'dark';

  if (theme === 'system') {
    return opposite;
  }

  return theme === opposite ? own : DEFAULT_THEME;
}
