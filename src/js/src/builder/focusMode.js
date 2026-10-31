// Focus mode: the Builder without the phenix navigation bar, in full screen
// when the browser allows it. Leaving full screen from the browser keeps
// focus mode on, so an Escape meant for a dialog does not end it.
// App.vue reads this state, so this module imports nothing else from the
// Builder.

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
 * Turns focus mode on and requests full screen. The press itself must call
 * it, because browsers grant full screen only in answer to a press.
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
 * Keeps focusMode.fullScreen in step with the browser. onLeft runs when the
 * browser leaves full screen.
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
