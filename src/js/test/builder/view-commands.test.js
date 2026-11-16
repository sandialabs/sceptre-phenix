// The View commands for the node settings (node icons, node descriptions, Node
// icon size): the palette lists and runs them, and they take keys.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { paletteResults } from '@/builder/commandSearch.js';
import {
  VIEW_API,
  commandKeys,
  createCommandContext,
  defaultKeys,
  getCommand,
  isCustomizable,
  runCommand,
} from '@/builder/commands.js';
import { loadShortcutSettings, setPlatform } from '@/builder/keymap.js';
import { assignShortcut, shortcutGroups } from '@/builder/shortcuts.js';

import { memoryStorage } from '../helpers/storage.js';
import { sampleDocument, withTemplates } from './fixtures.js';

// The command context of the open editor, with the view's methods stubbed.
function context(view = {}) {
  const { doc } = sampleDocument();
  const fullView = {
    editing: true,
    showMinimap: true,
    showNodeIcons: true,
    showNodeDescriptions: true,
    showConnections: true,
    nodeIconSize: 'off',
    canZoomIn: true,
    canZoomOut: true,
    icons: [],
    ...view,
  };

  for (const name of VIEW_API) {
    fullView[name] ??= vi.fn();
  }

  const ctx = createCommandContext({
    store: withTemplates({
      doc,
      readOnly: false,
      selection: { nodes: [], edges: [] },
      clipboard: null,
      canUndo: false,
      canRedo: false,
      canRestoreLayout: false,
      theme: 'system',
      history: { undoLabel: () => '' },
      drafts: { all: [] },
      documents: [],
      announce: vi.fn(),
    }),
    view: fullView,
  });

  return { ...ctx, source: 'palette' };
}

// The palette's command items for a query, by command id.
function paletteItems(ctx, query) {
  return paletteResults(ctx, { query })
    .groups.flatMap((group) => group.items)
    .filter((item) => item.kind === 'command');
}

function paletteItem(ctx, query, id) {
  return paletteItems(ctx, query).find((item) => item.command.id === id);
}

// The rows of the shortcut sheet, in its customization or not.
function sheetRows(options) {
  return shortcutGroups(options).flatMap((group) => group.rows);
}

beforeEach(() => {
  setPlatform('mac');
  loadShortcutSettings(null);
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(null);
});

describe.each([
  {
    id: 'view.nodeIcons',
    flag: 'showNodeIcons',
    noun: 'node icons',
    // Found by the word it starts with in each state.
    words: ['hide icons'],
    hiddenWords: ['show icons'],
    toggle: 'toggleNodeIcons',
    keys: [],
    key: 'Alt+Shift+I',
  },
  {
    id: 'view.nodeDescriptions',
    flag: 'showNodeDescriptions',
    noun: 'node descriptions',
    words: ['hide descriptions', 'comments'],
    hiddenWords: [],
    toggle: 'toggleNodeDescriptions',
    keys: [],
    key: 'Alt+Shift+D',
  },
  {
    id: 'view.connections',
    flag: 'showConnections',
    noun: 'connections',
    // Found by the other words for a connection.
    words: ['hide links', 'edges'],
    hiddenWords: [],
    toggle: 'toggleConnections',
    keys: ['Alt+Shift+C'],
    key: 'Alt+Shift+K',
  },
])(
  'Show or hide $noun',
  ({ id, flag, noun, words, hiddenWords, toggle, keys, key }) => {
    test(`is in the palette as Hide ${noun} or Show ${noun}`, () => {
      const hiddenContext = context({ [flag]: false });

      expect(paletteItem(context(), noun, id)).toMatchObject({
        title: `Hide ${noun}`,
        disabled: '',
      });
      expect(paletteItem(hiddenContext, noun, id)).toMatchObject({
        title: `Show ${noun}`,
        disabled: '',
      });
      for (const word of words) {
        expect(paletteItem(context(), word, id), word).toBeTruthy();
      }
      for (const word of hiddenWords) {
        expect(paletteItem(hiddenContext, word, id), word).toBeTruthy();
      }

      // With nothing typed, it is listed with the View commands.
      const view = paletteResults(context()).groups.find((group) =>
        group.items.some((item) => item.command?.id === id),
      );

      expect(view?.label).toBe('View');
    });

    test('runs from the palette, also in a read-only draft', () => {
      const ctx = context();
      const item = paletteItem(ctx, noun, id);

      expect(runCommand(item.command, ctx)).toBe(true);
      expect(ctx.view[toggle]).toHaveBeenCalledOnce();

      const readOnly = context();

      readOnly.store.readOnly = true;
      expect(runCommand(id, readOnly)).toBe(true);
      expect(readOnly.view[toggle]).toHaveBeenCalledOnce();
    });

    test(`has ${keys.length ? keys[0] : 'no key'} by default, and the shortcut sheet changes it`, () => {
      const storage = memoryStorage();

      expect(defaultKeys(id, 'mac')).toEqual(keys);
      expect(defaultKeys(id, 'other')).toEqual(keys);
      expect(commandKeys(id)).toEqual(keys);
      expect(isCustomizable(id)).toBe(true);

      // The sheet lists a command with no key only to customize.
      expect(sheetRows().some((row) => row.id === id)).toBe(keys.length > 0);
      expect(
        sheetRows({ customize: true, query: noun }).find(
          (row) => row.id === id,
        ),
      ).toMatchObject({
        title: `Show or hide ${noun}`,
        group: 'View',
        customizable: true,
        keys: keys.map((spec) => expect.objectContaining({ spec })),
        defaults: keys.map((spec) => expect.objectContaining({ spec })),
      });

      expect(assignShortcut(id, key, { storage }).ok).toBe(true);
      expect(commandKeys(id)).toEqual([key]);
      expect(sheetRows().map((row) => row.id)).toContain(id);
    });
  },
);

describe('Node icon size', () => {
  const CHOICES = {
    off: 'Off',
    small: 'Small',
    medium: 'Medium',
    large: 'Large',
  };

  test.each(Object.entries(CHOICES))(
    'Node icon size: %s is a View command with no default key',
    (value, title) => {
      const id = `view.nodeIconSize.${value}`;
      const command = getCommand(id);

      expect(command).toMatchObject({
        title: `Node icon size: ${title}`,
        group: 'View',
      });
      expect(defaultKeys(id, 'mac')).toEqual([]);
      expect(defaultKeys(id, 'other')).toEqual([]);
      expect(isCustomizable(id)).toBe(true);
    },
  );

  test('the palette lists each size, and runs any but the current one', () => {
    const ctx = context({ nodeIconSize: 'large' });
    const byId = (value) =>
      paletteItem(ctx, 'node icon size', `view.nodeIconSize.${value}`);

    expect(byId('off')).toMatchObject({
      title: 'Node icon size: Off',
      disabled: '',
      detail: 'Use the icon sizes of the diagram',
    });
    expect(byId('medium')).toMatchObject({
      disabled: '',
      detail: 'Every icon on your canvas. The diagram does not change.',
    });
    // A search lists the current size too, as one that cannot run.
    expect(byId('large')).toMatchObject({
      disabled: 'This is the current node icon size.',
    });

    expect(runCommand('view.nodeIconSize.medium', ctx)).toBe(true);
    expect(ctx.view.setNodeIconSize).toHaveBeenCalledWith('medium');

    // The current size does not run again.
    expect(runCommand('view.nodeIconSize.large', ctx)).toBe(false);
    expect(ctx.store.announce).toHaveBeenCalledWith(
      'This is the current node icon size.',
    );
    expect(ctx.view.setNodeIconSize).toHaveBeenCalledOnce();
  });
  test('takes a key in the shortcut sheet', () => {
    const storage = memoryStorage();
    const ids = Object.keys(CHOICES).map(
      (value) => `view.nodeIconSize.${value}`,
    );

    expect(
      sheetRows({ customize: true, query: 'node icon size' }).map(
        (row) => row.id,
      ),
    ).toEqual(ids);
    expect(sheetRows().map((row) => row.id)).not.toContain(ids[3]);

    expect(assignShortcut(ids[3], 'Alt+Shift+9', { storage }).ok).toBe(true);
    expect(commandKeys(ids[3])).toEqual(['Alt+Shift+9']);
  });
});
