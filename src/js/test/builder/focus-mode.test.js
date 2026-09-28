import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderHeaderButtons from '@/components/builder/BuilderHeaderButtons.vue';
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

  // The editor's header and the drafts' have the same button, so focus mode
  // can be left from either, and a view that replaces the other shows it
  // as it is.
  test("both headers' buttons follow it", async () => {
    const tip = { text: 'tip', description: 'keys', aria: 'Control+K' };
    const render = (view) =>
      renderToString(
        createSSRApp({
          render: () =>
            h(BuilderHeaderButtons, {
              view,
              shortcuts: view === 'editor',
              tipFor: () => ({}),
              themeButton: {
                icon: 'system',
                label: 'System',
                name: 'Theme: System. Switch to Dark theme.',
              },
              tips: Object.fromEntries(
                [
                  'commands',
                  'theme',
                  'shortcuts',
                  'settings',
                  'help',
                  'focus',
                ].map((key) => [key, tip]),
              ),
            }),
        }),
      );
    const button = (html, view) =>
      html.match(
        new RegExp(
          `<button[^>]*data-testid="${view}-focus-mode"[^]*?</button>`,
        ),
      )?.[0] || '';

    for (const view of ['drafts', 'editor']) {
      const off = button(await render(view), view);

      expect(off, view).toContain('aria-describedby="header-tip-focus"');
      expect(off, view).toContain('builder-icon--focus"');
      expect(off, view).toMatch(/>\s*Focus mode\s*</);

      enterFocusMode(null);
      const on = button(await render(view), view);

      expect(on, view).toContain('builder-icon--focus-exit');
      expect(on, view).toMatch(/>\s*Exit focus mode\s*</);
      exitFocusMode(null);
    }

    // The drafts have no Shortcuts; the others come in the editor's order.
    const drafts = await render('drafts');
    const editor = await render('editor');
    const ids = (html) =>
      [...html.matchAll(/data-testid="(?:drafts|editor)-([a-z-]+)"/g)].map(
        (match) => match[1],
      );

    expect(ids(editor)).toEqual([
      'commands',
      'theme',
      'shortcuts',
      'settings',
      'help',
      'focus-mode',
    ]);
    expect(ids(drafts)).toEqual(ids(editor).filter((id) => id !== 'shortcuts'));
  });
});
