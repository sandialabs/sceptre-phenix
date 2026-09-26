import { afterEach, describe, expect, test, vi } from 'vitest';

import {
  enterFocusMode,
  exitFocusMode,
  focusMode,
  followFullScreen,
} from '@/builder/focusMode.js';

// The Fullscreen API as focusMode.js reads it. Each change fires
// fullscreenchange; `grant` settles a request, which the browser may refuse.
function fakeDocument({ enabled = true } = {}) {
  const listeners = new Set();
  const doc = {
    fullscreenEnabled: enabled,
    fullscreenElement: null,
    addEventListener: (_, listener) => listeners.add(listener),
    removeEventListener: (_, listener) => listeners.delete(listener),
    change(element) {
      doc.fullscreenElement = element;
      listeners.forEach((listener) => listener());
    },
    exitFullscreen: vi.fn(async () => doc.change(null)),
    grant: null,
  };

  doc.documentElement = {
    requestFullscreen: vi.fn(
      () =>
        new Promise((resolve, reject) => {
          doc.grant = (granted = true) => {
            if (!granted) {
              reject(new Error('Permission denied'));

              return;
            }

            doc.change(doc.documentElement);
            resolve();
          };
        }),
    ),
  };

  return doc;
}

afterEach(() => {
  exitFocusMode(null);
  focusMode.fullScreen = false;
});

describe('focus mode', () => {
  test('goes full screen from the press that turns it on, and leaves it on its own exit only', async () => {
    const doc = fakeDocument();
    const left = vi.fn();
    const stop = followFullScreen(left, doc);

    const entered = enterFocusMode(doc);
    // Asked for at once, while the press still counts as one.
    expect(doc.documentElement.requestFullscreen).toHaveBeenCalledWith({
      navigationUI: 'hide',
    });
    doc.grant();
    expect(await entered).toBe(true);
    expect(focusMode).toEqual({ on: true, fullScreen: true });

    exitFocusMode(doc);
    await vi.waitFor(() => expect(focusMode.fullScreen).toBe(false));
    expect(focusMode.on).toBe(false);
    expect(left).not.toHaveBeenCalled();

    // The browser's Escape leaves full screen only: focus mode stays on,
    // and the view is told.
    const again = enterFocusMode(doc);
    doc.grant();
    await again;
    doc.change(null);
    expect(focusMode).toEqual({ on: true, fullScreen: false });
    expect(left).toHaveBeenCalledOnce();

    // Turned off before the browser granted it: full screen is left again.
    exitFocusMode(doc);
    doc.exitFullscreen.mockClear();
    const late = enterFocusMode(doc);
    exitFocusMode(doc);
    doc.grant();
    expect(await late).toBe(false);
    expect(doc.exitFullscreen).toHaveBeenCalledOnce();
    stop();
  });

  test('stays on, in the window, where full screen is refused or missing', async () => {
    const refused = fakeDocument();
    const entered = enterFocusMode(refused);

    refused.grant(false);
    expect(await entered).toBe(false);
    expect(focusMode.on).toBe(true);
    exitFocusMode(refused);
    expect(refused.exitFullscreen).not.toHaveBeenCalled();

    for (const doc of [fakeDocument({ enabled: false }), null]) {
      expect(await enterFocusMode(doc)).toBe(false);
      expect(focusMode.on).toBe(true);
      exitFocusMode(doc);
      expect(focusMode.on).toBe(false);
    }
  });
});
