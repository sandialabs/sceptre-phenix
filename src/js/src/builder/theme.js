// Builder-only theme handling.
//
// The rest of phenix keeps its own styling; the beta builder scopes its theme
// to its own root element via a data attribute, so switching here never leaks
// into other views. The preference is persisted under phenix.builder.theme.

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

/**
 * Reads the persisted preference, falling back to "system".
 *
 * @param {Storage} [storage]
 * @returns {string}
 */
export function readStoredTheme(storage) {
  try {
    const value = storage?.getItem(THEME_STORAGE_KEY);
    return isValidTheme(value) ? value : DEFAULT_THEME;
  } catch {
    return DEFAULT_THEME;
  }
}

/**
 * Persists the preference. Storage failures (private mode) are non-fatal.
 *
 * @param {string} theme
 * @param {Storage} [storage]
 * @returns {string} the theme that was stored
 */
export function storeTheme(theme, storage) {
  const value = isValidTheme(theme) ? theme : DEFAULT_THEME;

  try {
    storage?.setItem(THEME_STORAGE_KEY, value);
  } catch {
    // ignore: the in-memory preference still applies for this session
  }

  return value;
}

/**
 * Resolves the preference to the concrete theme to render.
 *
 * @param {string} theme preference
 * @param {(query: string) => {matches: boolean}} [matchMedia]
 * @returns {'light'|'dark'}
 */
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

/**
 * Applies the resolved theme to an element as data attributes.
 *
 * @param {HTMLElement} element builder root element
 * @param {string} theme preference
 * @param {(query: string) => {matches: boolean}} [matchMedia]
 * @returns {'light'|'dark'} resolved theme
 */
export function applyTheme(element, theme, matchMedia) {
  const resolved = resolveTheme(theme, matchMedia);

  if (element?.setAttribute) {
    element.setAttribute('data-builder-theme', resolved);
    element.setAttribute('data-builder-theme-preference', theme);
  }

  return resolved;
}

/**
 * Watches the browser's color-scheme preference.
 *
 * @param {(query: string) => MediaQueryList} matchMedia
 * @param {() => void} onChange
 * @returns {() => void} unsubscribe function
 */
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

/**
 * The theme the toolbar toggle moves to. From System it goes to the opposite
 * of what the system shows, so the first press always changes the colors,
 * then to the system's own, then back to System: System, Dark, Light for a
 * light system, and System, Light, Dark for a dark one.
 *
 * @param {string} theme preference
 * @param {'light'|'dark'} [system] what System shows now (see resolveTheme)
 * @returns {string}
 */
export function nextTheme(theme, system = 'light') {
  const own = system === 'dark' ? 'dark' : 'light';
  const opposite = own === 'dark' ? 'light' : 'dark';

  if (theme === 'system') {
    return opposite;
  }

  return theme === opposite ? own : DEFAULT_THEME;
}
