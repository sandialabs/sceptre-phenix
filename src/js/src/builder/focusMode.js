// Focus mode: the Builder alone, without the phenix navigation bar. It fills
// the window, and the screen where the browser allows it. App.vue hides its
// header while focus mode is on. The Builder's view (Builder.vue) turns focus
// mode on and off through the Focus mode button in the editor and drafts
// headers and the view.focusMode command. It also turns focus mode off when
// the user leaves the Builder. Focus mode stays on when the editor and the
// drafts replace each other.
//
// The press that turns focus mode on also requests full screen through the
// Fullscreen API. The browser can refuse it (a frame without permission, a
// browser setting). Focus mode then fills the window only. When the user
// leaves full screen from the browser (Escape, the browser's own controls),
// focus mode stays on, in the window. Browsers keep the first Escape for
// themselves. Without this rule, an Escape meant to close a dialog or clear
// the selection would also end focus mode. Focus mode ends only through its
// button, its keys or leaving the Builder, and each of these also leaves full
// screen.
//
// The app loads this module (App.vue reads the state), so the module stays
// small and imports nothing else from the Builder.

import { reactive } from 'vue';

// on: focus mode is on. fullScreen: the page is full screen, as focus mode
// asked.
export const focusMode = reactive({ on: false, fullScreen: false });

function isFullScreen(doc) {
  return (
    Boolean(doc?.documentElement) &&
    doc.fullscreenElement === doc.documentElement
  );
}

// Whether focus mode asked for full screen, and has not seen it end: it
// leaves only the full screen it asked for.
let asked = false;

function leaveFullScreen(doc, ours = asked) {
  asked = false;

  if (!ours || !isFullScreen(doc)) {
    return;
  }

  try {
    Promise.resolve(doc.exitFullscreen?.()).catch(() => {});
  } catch {
    // A browser that cannot leave full screen here leaves it on Escape.
  }
}

/**
 * Turns focus mode on and requests full screen. The press that turns focus
 * mode on calls it, because browsers grant full screen only in answer to a
 * press.
 *
 * @param {Document} [doc]
 * @returns {Promise<boolean>} whether the page went full screen
 */
export async function enterFocusMode(doc = globalThis.document) {
  if (focusMode.on) {
    return isFullScreen(doc);
  }

  focusMode.on = true;

  const element = doc?.documentElement;

  if (
    !doc?.fullscreenEnabled ||
    doc.fullscreenElement ||
    !element?.requestFullscreen
  ) {
    return false;
  }

  asked = true;

  try {
    await element.requestFullscreen({ navigationUI: 'hide' });
  } catch {
    asked = false;

    return false;
  }

  // Focus mode ended while the browser was still deciding: the full screen
  // it granted is left at once.
  if (!focusMode.on) {
    leaveFullScreen(doc, true);

    return false;
  }

  return true;
}

/**
 * Turns focus mode off, and leaves the full screen it asked for.
 *
 * @param {Document} [doc]
 */
export function exitFocusMode(doc = globalThis.document) {
  if (!focusMode.on) {
    return;
  }

  focusMode.on = false;
  leaveFullScreen(doc);
}

/**
 * Keeps focusMode.fullScreen in step with the browser.
 *
 * @param {() => void} onLeft runs when the browser leaves full screen and
 *   focus mode stays on (Escape, the browser's controls)
 * @param {Document} [doc]
 * @returns {() => void} stops following
 */
export function followFullScreen(onLeft, doc = globalThis.document) {
  if (!doc?.addEventListener) {
    return () => {};
  }

  const changed = () => {
    const was = focusMode.fullScreen;

    asked &&= isFullScreen(doc);
    focusMode.fullScreen = asked;

    if (was && !focusMode.fullScreen && focusMode.on) {
      onLeft?.();
    }
  };

  doc.addEventListener('fullscreenchange', changed);

  return () => doc.removeEventListener('fullscreenchange', changed);
}
