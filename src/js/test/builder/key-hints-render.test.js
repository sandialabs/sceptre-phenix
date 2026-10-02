// Where the buttons show their keys, rendered on the server: the key cap on
// the header's Shortcuts button, and the tooltips of the toolbar's layout
// and Auto-group menus. Showing the tooltips needs a browser, so that is
// left to the Playwright specs.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderHeaderButtons from '@/components/builder/BuilderHeaderButtons.vue';
import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import {
  loadShortcutSettings,
  setPlatform,
  setShortcut,
  setSingleKeyShortcuts,
} from '@/builder/keymap.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

function noStorage() {
  return { getItem: () => null, setItem: () => {} };
}

beforeEach(() => {
  setPlatform('mac');
  loadShortcutSettings(noStorage());
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(noStorage());
});

describe('the header’s Shortcuts button', () => {
  const tip = { text: 'tip', description: 'keys', aria: '?' };

  function render(view = 'editor') {
    return renderToString(
      createSSRApp({
        render: () =>
          h(BuilderHeaderButtons, {
            view,
            shortcuts: view === 'editor',
            tipFor: () => ({}),
            themeButton: { icon: 'system', label: 'System', name: 'Theme' },
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
  }

  // The inside of a header button.
  function button(html, id) {
    return (
      html.match(
        new RegExp(`<button[^>]*data-testid="${id}"[^>]*>([^]*?)</button>`),
      )?.[1] ?? null
    );
  }

  // The key caps in a piece of markup, as text.
  function caps(html) {
    return [...html.matchAll(/<kbd[^>]*>([^<]*)<\/kbd>/g)].map(
      (match) => match[1],
    );
  }

  test('shows the key that opens the sheet, beside its label', async () => {
    const shortcuts = button(await render(), 'editor-shortcuts');

    expect(caps(shortcuts)).toEqual(['?']);
    // After the label and outside it, so it stays when a narrow header
    // hides its labels; only a picture, as the description says the key.
    const [label, after] = shortcuts.split('</span>').slice(0, 2);

    expect(label).toMatch(/builder-header__label[^>]*>\s*Shortcuts\s*$/);
    expect(after).toMatch(/class="builder-keycaps"[^>]*aria-hidden="true"/);
    expect(caps(label)).toEqual([]);
  });

  test('shows the user’s key, and none while the sheet has no key', async () => {
    setShortcut('shortcuts.open', ['Mod+/'], noStorage());
    expect(caps(button(await render(), 'editor-shortcuts'))).toEqual([
      '⌘',
      '/',
    ]);

    // The default key is one character, which the single-key switch turns
    // off.
    loadShortcutSettings(noStorage());
    setSingleKeyShortcuts(false, noStorage());

    const off = button(await render(), 'editor-shortcuts');

    expect(caps(off)).toEqual([]);
    expect(off).not.toContain('builder-keycaps');
    expect(off).toMatch(/>\s*Shortcuts\s*</);
  });

  test('is the only other button with key caps, and the drafts page has none', async () => {
    const editor = await render();

    expect(caps(button(editor, 'editor-commands'))).toEqual(['⌘', 'K']);
    expect(caps(button(editor, 'editor-settings'))).toEqual([]);
    expect(caps(button(editor, 'editor-theme'))).toEqual([]);
    expect(button(await render('drafts'), 'drafts-shortcuts')).toBeNull();
  });
});

describe('the toolbar’s menus', () => {
  async function tips(prepare = () => {}) {
    const pinia = createPinia();
    const app = createSSRApp({ render: () => h(BuilderToolbar) });

    app.use(pinia);

    const store = useBuilderStore(pinia);

    store.doc = sampleDocument().doc;
    prepare(store);

    const html = await renderToString(app);
    const tip = (key) =>
      html
        .match(
          new RegExp(`<span id="toolbar-tip-${key}"[^>]*>([^<]*)</span>`),
        )[1]
        .trim();

    return { layout: tip('layout'), autoGroup: tip('autoGroup') };
  }

  test('say what their keys run, as this platform writes them', async () => {
    expect(await tips()).toEqual({
      layout: 'Choose a layout. ⌥⇧L runs ELK layered, the Settings default',
      autoGroup:
        'Group the ungrouped nodes automatically. ⌥⇧G groups by network',
    });

    setPlatform('other');
    expect(await tips()).toEqual({
      layout:
        'Choose a layout. Alt+Shift+L runs ELK layered, the Settings default',
      autoGroup:
        'Group the ungrouped nodes automatically. Alt+Shift+G groups by network',
    });
  });

  test('follow the selection and the user’s keys', async () => {
    const { doc, alpha } = sampleDocument();
    const selected = await tips((store) => {
      store.doc = doc;
      store.selection = { nodes: [alpha.id], edges: [] };
    });

    expect(selected.autoGroup).toBe(
      'Group the selected nodes automatically. ⌥⇧G groups by network',
    );

    // Without keys, the tooltips say only what the menus do.
    setShortcut('structure.layout', [], noStorage());
    setShortcut('structure.autoGroup.network', [], noStorage());
    expect(await tips()).toEqual({
      layout: 'Choose a layout',
      autoGroup: 'Group the ungrouped nodes automatically',
    });

    setShortcut('structure.autoGroup.network', ['Mod+Shift+U'], noStorage());
    expect((await tips()).autoGroup).toBe(
      'Group the ungrouped nodes automatically. ⇧⌘U groups by network',
    );
  });
});
