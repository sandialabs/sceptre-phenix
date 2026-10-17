// The View commands for the node icon settings (Show or hide node icons,
// Node icon size): the palette lists and runs them, and they take keys.

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

import { memoryStorage, sampleDocument, withTemplates } from './fixtures.js';

// The command context of the open editor, with the view's methods stubbed.
function context(view = {}) {
  const { doc } = sampleDocument();
  const fullView = {
    editing: true,
    showMinimap: true,
    showNodeNotes: true,
    showNodeIcons: true,
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

describe('Show or hide node icons', () => {
  const ID = 'view.nodeIcons';

  test('is in the palette as Hide node icons or Show node icons', () => {
    const shown = paletteItem(context(), 'node icons', ID);
    const hidden = paletteItem(
      context({ showNodeIcons: false }),
      'node icons',
      ID,
    );

    expect(shown).toMatchObject({ title: 'Hide node icons', disabled: '' });
    expect(hidden).toMatchObject({ title: 'Show node icons', disabled: '' });
    // Found by the word it starts with in each state.
    expect(paletteItem(context(), 'hide icons', ID)).toBeTruthy();
    expect(
      paletteItem(context({ showNodeIcons: false }), 'show icons', ID),
    ).toBeTruthy();

    // With nothing typed, it is listed with the View commands.
    const view = paletteResults(context()).groups.find((group) =>
      group.items.some((item) => item.command?.id === ID),
    );

    expect(view?.label).toBe('View');
  });

  test('runs from the palette: the view shows or hides the icons', () => {
    const ctx = context();
    const item = paletteItem(ctx, 'node icons', ID);

    expect(runCommand(item.command, ctx)).toBe(true);
    expect(ctx.view.toggleNodeIcons).toHaveBeenCalledOnce();
  });

  test('has no default key, and takes one in the shortcut sheet', () => {
    const storage = memoryStorage();

    expect(defaultKeys(ID, 'mac')).toEqual([]);
    expect(defaultKeys(ID, 'other')).toEqual([]);
    expect(commandKeys(ID)).toEqual([]);
    expect(isCustomizable(ID)).toBe(true);

    // The sheet lists it only to customize, as it has no key.
    expect(sheetRows().map((row) => row.id)).not.toContain(ID);
    expect(
      sheetRows({ customize: true, query: 'node icons' }).find(
        (row) => row.id === ID,
      ),
    ).toMatchObject({
      title: 'Show or hide node icons',
      group: 'View',
      customizable: true,
      keys: [],
      defaults: [],
    });

    expect(assignShortcut(ID, 'Alt+Shift+I', { storage }).ok).toBe(true);
    expect(commandKeys(ID)).toEqual(['Alt+Shift+I']);
    expect(sheetRows().map((row) => row.id)).toContain(ID);
  });
});

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
