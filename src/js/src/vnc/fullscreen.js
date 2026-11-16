// Fullscreen for a console pane or the whole console page. In a secure
// context with Keyboard Lock (Chromium), Esc, Alt+Tab and Ctrl+W go to the
// guest. Firefox and Safari have no Keyboard Lock and keep normal fullscreen.
// To exit, use the chord Ctrl+] then F, or hold Esc.
import { ConfigProgrammatic, ToastProgrammatic as Toast } from 'buefy';

/** The toast and the fullscreen button tooltip over plain HTTP. */
export const KEYBOARD_CAPTURE_NEEDS_HTTPS = 'Keyboard capture needs HTTPS';

function keyboardLock() {
  if (globalThis.isSecureContext !== true) return null;
  const keyboard = globalThis.navigator?.keyboard;
  return typeof keyboard?.lock === 'function' ? keyboard : null;
}

/**
 * The note for the fullscreen button tooltip: KEYBOARD_CAPTURE_NEEDS_HTTPS
 * over plain HTTP, else null.
 *
 * @returns {string|null}
 */
export function keyboardCaptureNote() {
  return globalThis.isSecureContext === true
    ? null
    : KEYBOARD_CAPTURE_NEEDS_HTTPS;
}

const NOTICES_ATTR = 'data-vnc-notices';
const NOTICES = `[${NOTICES_ATTR}]`;

/**
 * The Buefy container for notices: a selector of the fullscreen element, or
 * null when no element is fullscreen. Buefy mounts notices on the body, and
 * the browser does not show the body over a fullscreen element.
 *
 * @returns {string|null}
 */
function noticeContainer() {
  const el = document.fullscreenElement ?? null;
  for (const old of document.querySelectorAll(NOTICES)) {
    if (old !== el) old.removeAttribute(NOTICES_ATTR);
  }
  if (!el) return null;
  el.setAttribute(NOTICES_ATTR, '');
  return NOTICES;
}

/**
 * While an element is fullscreen, Buefy mounts its toasts, snackbars,
 * notifications and dialogs inside that element.
 *
 * @returns {() => void} stops, and puts notices on the body again
 */
export function keepNoticesInFullscreen() {
  let set = false;
  const follow = () => {
    const container = noticeContainer();
    // Buefy merges a null over a null into an empty object
    if (!container && !set) return;
    set = Boolean(container);
    ConfigProgrammatic.setOptions({ defaultContainerElement: container });
  };
  document.addEventListener('fullscreenchange', follow);
  return () => {
    document.removeEventListener('fullscreenchange', follow);
    if (set) ConfigProgrammatic.setOptions({ defaultContainerElement: null });
    set = false;
  };
}

const showToast = (message) =>
  new Toast().open({
    message,
    type: 'is-warning',
    container: noticeContainer(),
  });

/**
 * Shows the element fullscreen, and locks the keyboard when the browser lets
 * it. Over plain HTTP, it does not call Keyboard Lock, and a toast inside the
 * fullscreen element says KEYBOARD_CAPTURE_NEEDS_HTTPS.
 *
 * @param {Element} el
 * @param {{ toast?: (message: string) => void }} [options]
 * @returns {Promise<{ keyboardLocked: boolean }>} it rejects when the
 *   browser refuses fullscreen
 */
export async function enterFullscreen(el, { toast = showToast } = {}) {
  await el.requestFullscreen();
  const keyboard = keyboardLock();
  if (!keyboard) {
    const note = keyboardCaptureNote();
    if (note) toast(note);
    return { keyboardLocked: false };
  }
  try {
    await keyboard.lock();
    return { keyboardLocked: true };
  } catch {
    return { keyboardLocked: false };
  }
}

/** Unlocks the keyboard and leaves fullscreen. */
export async function exitFullscreen() {
  globalThis.navigator?.keyboard?.unlock?.();
  if (document.fullscreenElement) await document.exitFullscreen();
}
