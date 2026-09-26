// Focus mode: the Builder's editor on its own, without the phenix navigation
// bar, filling the window, and the screen where the browser allows it.
// App.vue hides its header while focus mode is on; the editor
// (BuilderBeta.vue) turns it on and off, with its header button and the
// view.focusMode command, and off when it closes.
//
// Full screen is asked for with the Fullscreen API, from the press that
// turns focus mode on, and may be refused (a frame without permission, a
// browser setting): focus mode then fills the window only. Leaving full
// screen from the browser (Escape, the browser's own controls) leaves focus
// mode on, in the window: browsers keep the first Escape for themselves, so
// an Escape meant to close a dialog or clear the selection would otherwise
// end focus mode as well. Focus mode ends only by its button, its keys or
// the editor closing, and those leave full screen too.
//
// This module is loaded with the app (App.vue reads the state), so it stays
// small and imports nothing else of the Builder's.

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
 * Turns focus mode on and asks for full screen. Called from the press that
 * turns it on: browsers grant full screen only in answer to one.
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
 * @param {() => void} onLeft runs when the browser leaves full screen while
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
