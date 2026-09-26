import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import {
  COMMANDS,
  GROUPS,
  READ_ONLY,
  SCOPES,
  TYPING,
  VIEW_API,
  ariaShortcuts,
  availability,
  canvasHelp,
  canvasHints,
  commandKeys,
  commandTitle,
  createCommandContext,
  defaultKeys,
  dispatchKeydown,
  focusScope,
  getCommand,
  isCustomizable,
  nodeChoices,
  outlineHint,
  paletteCommands,
  runCommand,
  saveDraft,
  shortcutConflicts,
  shortcutLabel,
  textFieldCommand,
  withShortcut,
  worksInTextFields,
} from '@/builder/commands.js';
import {
  keyRefusal,
  loadShortcutSettings,
  parseKey,
  reservedReason,
  setPlatform,
  setShortcut,
  setSingleKeyShortcuts,
  typesCharacter,
} from '@/builder/keymap.js';

import { nextPosition } from '@/components/builder/paletteDnd.js';

import { sampleDocument } from './fixtures.js';

const PLATFORMS = ['mac', 'other'];

function noStorage() {
  return { getItem: () => null, setItem: () => {} };
}

function fakeStore(overrides = {}) {
  const { doc } = sampleDocument();
  const store = {
    doc,
    readOnly: false,
    selection: { nodes: [], edges: [] },
    clipboard: null,
    canUndo: false,
    canRedo: false,
    canRestoreLayout: false,
    theme: 'system',
    history: { undoLabel: () => 'Added device' },
    drafts: { mine: [], shared: [] },
    documents: [],
    ...overrides,
  };

  for (const action of [
    'announce',
    'undo',
    'redo',
    'copy',
    'paste',
    'duplicate',
    'group',
    'ungroup',
    'selectAll',
    'clearSelection',
    'layout',
    'restoreLayout',
    'saveNow',
    'addNode',
    'connect',
    'removeSelection',
  ]) {
    store[action] = vi.fn();
  }
  store.select = vi.fn((selection) => {
    store.selection = { nodes: [], edges: [], ...selection };
  });

  return store;
}

function fakeView(overrides = {}) {
  const view = {
    editing: true,
    dialog: '',
    showMinimap: true,
    canZoomIn: true,
    canZoomOut: true,
    focusMode: false,
    ...overrides,
  };

  for (const name of VIEW_API) {
    if (!(name in view)) {
      view[name] = vi.fn();
    }
  }

  return view;
}

function context({ store = {}, view = {} } = {}) {
  return createCommandContext({
    store: fakeStore(store),
    view: fakeView(view),
  });
}

// --- stand-ins for the DOM focusScope reads --------------------------------

const SELECTS = {
  dialog: (selector) => selector === 'dialog',
  appDialog: (selector) => selector.includes('[role="dialog"]'),
  field: (selector) => selector === TYPING,
  canvas: (selector) => selector === '.builder-canvas',
  item: (selector) => selector.includes('.vue-flow__node'),
  row: (selector) => selector.includes('.builder-outline__item'),
};

function fakeDocument({ dialogOpen = false } = {}) {
  const doc = {
    querySelector: (selector) =>
      selector === 'dialog[open]' && dialogOpen ? {} : null,
  };

  doc.body = element(doc);

  return doc;
}

function element(doc, is = [], within = [], extra = {}) {
  return {
    ownerDocument: doc,
    matches: (selector) => is.some((kind) => SELECTS[kind](selector)),
    closest: (selector) =>
      [...is, ...within].some((kind) => SELECTS[kind](selector)) ? {} : null,
    classList: { contains: (name) => (extra.classes || []).includes(name) },
    getAttribute: (name) => extra.attrs?.[name] ?? null,
    dataset: extra.dataset || {},
  };
}

function targets(doc = fakeDocument()) {
  return {
    doc,
    body: doc.body,
    button: element(doc),
    field: element(doc, ['field']),
    canvas: element(doc, ['canvas']),
    node: element(doc, ['item'], ['canvas'], {
      classes: ['vue-flow__node'],
      attrs: { 'data-id': 'n1' },
    }),
    zoomButton: element(doc, [], ['canvas']),
    row: element(doc, ['row'], [], { dataset: { testid: 'outline-item-n2' } }),
    inDialog: element(doc, [], ['dialog']),
    // Outside the Builder: the app header's link, a field and a modal.
    outside: element(doc),
    outsideField: element(doc, ['field']),
    outsideModal: element(doc, [], ['appDialog']),
  };
}

function rootFor(t) {
  const outside = [t.outside, t.outsideField, t.outsideModal];

  return { contains: (target) => !outside.includes(target) };
}

function keydown(target, key, code, mods = {}) {
  return {
    key,
    code,
    target,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    isComposing: false,
    defaultPrevented: false,
    getModifierState: () => false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    ...mods,
  };
}

// The platform's command key.
function mod(platform) {
  return platform === 'mac' ? { metaKey: true } : { ctrlKey: true };
}

beforeEach(() => {
  loadShortcutSettings(noStorage());
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(noStorage());
  vi.unstubAllGlobals();
});

describe('the registry', () => {
  test('ids are unique, and every command is titled and grouped', () => {
    const ids = COMMANDS.map((command) => command.id);

    expect(new Set(ids).size).toBe(ids.length);
    for (const command of COMMANDS) {
      expect(command.title, command.id).toBeTruthy();
      expect(GROUPS, command.id).toContain(command.group);
      for (const scope of [].concat(command.scope || [])) {
        expect(Object.keys(SCOPES), command.id).toContain(scope);
      }
      if (command.palette !== false && !command.local) {
        expect(typeof command.run, command.id).toBe('function');
      }
    }
  });

  test('every default key is valid, free of the browser and the OS, and choosable', () => {
    for (const platform of PLATFORMS) {
      for (const command of COMMANDS) {
        for (const key of defaultKeys(command, platform)) {
          const where = `${command.id} ${key} (${platform})`;

          expect(parseKey(key), where).not.toBeNull();
          expect(reservedReason(key, platform), where).toBe('');
          if (isCustomizable(command)) {
            expect(keyRefusal(key, platform), where).toBe('');
          }
          // A text field would keep it (see dispatchKeydown).
          if (worksInTextFields(command)) {
            expect(typesCharacter(key, platform), where).toBe(false);
          }
        }
      }
    }
  });

  test('no two commands share a key where both would answer to it', () => {
    for (const platform of PLATFORMS) {
      for (const command of COMMANDS) {
        for (const key of defaultKeys(command, platform)) {
          expect(
            shortcutConflicts(command, key, { platform }).map(
              (other) => other.id,
            ),
            `${command.id} ${key} (${platform})`,
          ).toEqual([]);
        }
      }
    }
  });

  test('covers the design’s shortcuts, with platform-native redo', () => {
    const keys = (id, platform) => defaultKeys(id, platform);

    expect(keys('palette.open', 'mac')).toEqual(['Mod+K']);
    expect(keys('goto.node', 'other')).toEqual(['Mod+Shift+O']);
    expect(keys('shortcuts.open', 'mac')).toEqual(['?']);
    expect(keys('draft.save', 'other')).toEqual(['Mod+S']);
    // Not F11, which browsers keep; it works in text fields too.
    expect(keys('view.focusMode', 'mac')).toEqual(['Mod+Shift+F']);
    expect(worksInTextFields('view.focusMode')).toBe(true);
    expect(keys('structure.group', 'mac')).toEqual(['Mod+G']);
    expect(keys('structure.ungroup', 'mac')).toEqual(['Mod+Shift+G']);
    expect(keys('selection.all', 'mac')).toEqual(['Mod+A']);
    expect(keys('view.zoomIn', 'mac')).toEqual(['=', '+']);
    expect(keys('view.zoomOut', 'mac')).toEqual(['-']);
    expect(keys('view.fit', 'mac')).toEqual(['Shift+1']);
    expect(keys('edit.redo', 'mac')).toEqual(['Mod+Shift+Z']);
    expect(keys('edit.redo', 'other')).toEqual(['Mod+Shift+Z', 'Mod+Y']);
    // Dropped: the palette has one key, and networks come from switches.
    expect(
      COMMANDS.some((command) =>
        /network…|add\.network/i.test(command.id + command.title),
      ),
    ).toBe(false);
  });

  test('the palette lists each view’s own commands', () => {
    const editor = paletteCommands(context()).map((command) => command.id);
    const landing = paletteCommands(context({ view: { editing: false } })).map(
      (command) => command.id,
    );

    expect(editor).toContain('structure.group');
    expect(editor).toContain('draft.upload');
    expect(editor).not.toContain('drafts.upload');
    expect(editor).not.toContain('drafts.blank');
    expect(editor).not.toContain('palette.open');
    expect(editor).not.toContain('selection.press');
    expect(landing).toEqual(
      expect.arrayContaining([
        'drafts.blank',
        'drafts.import',
        'drafts.upload',
        'drafts.open',
        'drafts.tab.shared',
        'help.open',
        'shortcuts.open',
        'settings.open',
        'view.theme.dark',
      ]),
    );
    expect(editor).toContain('settings.open');
    expect(landing).not.toContain('edit.undo');
    // The landing's Upload is listed with the other ways to start a draft.
    expect(landing).not.toContain('draft.upload');
    expect(getCommand('drafts.upload').group).toBe('Drafts');
  });
});

describe('availability', () => {
  test('a read-only draft keeps only what does not edit', () => {
    const { doc } = sampleDocument();
    const ctx = context({
      store: {
        readOnly: true,
        canUndo: true,
        canRedo: true,
        canRestoreLayout: true,
        clipboard: { nodes: [{}], edges: [] },
        selection: { nodes: [doc.nodes[0].id], edges: [] },
      },
    });
    const editing = [
      'edit.undo',
      'edit.redo',
      'edit.paste',
      'edit.duplicate',
      'edit.delete',
      'edit.rename',
      'structure.group',
      'structure.ungroup',
      'structure.layout',
      'structure.restoreLayout',
      'structure.connect',
      'structure.disconnect',
      'add.device',
      'add.switch',
      'draft.save',
      'draft.scenario',
      'draft.publish',
    ];

    for (const id of editing) {
      expect(availability(id, ctx), id).toBe(READ_ONLY);
    }
    for (const id of [
      'edit.copy',
      'selection.all',
      'goto.node',
      'draft.export',
      'draft.history',
      'palette.open',
      'view.fit',
    ]) {
      expect(availability(id, ctx), id).toBe(true);
    }

    // A role that may not create drafts or publish gets neither (R29).
    const viewer = { canCreateDrafts: false, canPublish: false };
    const editor = context({ store: viewer });
    const landing = context({ store: viewer, view: { editing: false } });

    expect(availability('draft.upload', editor)).toBe(
      'Your role cannot create drafts.',
    );
    expect(availability('draft.publish', editor)).toBe(
      'Your role cannot publish diagrams.',
    );
    expect(availability('draft.export', editor)).toBe(true);
    for (const id of ['drafts.blank', 'drafts.import', 'drafts.upload']) {
      expect(availability(id, landing), id).toBe(
        'Your role cannot create drafts.',
      );
    }
  });

  test('says why, in the store’s own words', () => {
    const ctx = context();

    expect(availability('structure.group', ctx)).toBe(
      'Select at least one node to group.',
    );
    expect(availability('structure.ungroup', ctx)).toBe(
      'Select a group first.',
    );
    expect(availability('edit.paste', ctx)).toBe('Clipboard is empty.');
    expect(availability('edit.undo', ctx)).toBe('Nothing to undo.');
    expect(availability('edit.redo', ctx)).toBe('Nothing to redo.');
    expect(availability('edit.copy', ctx)).toBe('Nothing is selected to copy.');
    expect(availability('view.theme.system', ctx)).toBe(
      'This is the current theme.',
    );
    expect(availability('drafts.blank', ctx)).toBe(
      'Go back to the drafts first.',
    );
    expect(
      availability('edit.undo', context({ view: { editing: false } })),
    ).toBe('Open a draft first.');
    expect(
      availability('view.zoomIn', context({ view: { canZoomIn: false } })),
    ).toMatch(/largest zoom/);

    // The landing's tab commands, for the tab already shown.
    const landing = context({ view: { editing: false, draftsTab: 'mine' } });
    expect(availability('drafts.tab.mine', landing)).toBe(
      'This tab is already shown.',
    );
    expect(availability('drafts.tab.shared', landing)).toBe(true);
  });

  test('titles follow the state', () => {
    expect(commandTitle('view.minimap', context())).toBe('Hide minimap');
    expect(
      commandTitle('view.minimap', context({ view: { showMinimap: false } })),
    ).toBe('Show minimap');
    expect(commandTitle('view.focusMode', context())).toBe('Focus mode');
    expect(
      commandTitle('view.focusMode', context({ view: { focusMode: true } })),
    ).toBe('Exit focus mode');

    const { doc, alpha } = sampleDocument();
    expect(
      commandTitle(
        'edit.rename',
        context({
          store: { doc, selection: { nodes: [alpha.id], edges: [] } },
        }),
      ),
    ).toBe('Rename alpha');
  });
});

describe('running', () => {
  test('runs a command, or announces why it cannot', () => {
    const ctx = context({ store: { canUndo: true } });

    expect(runCommand('edit.undo', ctx)).toBe(true);
    expect(ctx.store.undo).toHaveBeenCalledOnce();

    expect(runCommand('edit.redo', ctx)).toBe(false);
    expect(ctx.store.redo).not.toHaveBeenCalled();
    expect(ctx.store.announce).toHaveBeenCalledWith('Nothing to redo.');

    // The header's Reset view is in the palette too, read only or not.
    const viewer = context({ store: { readOnly: true } });
    expect(paletteCommands(viewer).map((command) => command.id)).toContain(
      'view.reset',
    );
    expect(runCommand('view.reset', viewer)).toBe(true);
    expect(viewer.view.resetView).toHaveBeenCalledOnce();
    // The header's Settings too; it has no keys until the user gives it some.
    expect(runCommand('settings.open', viewer)).toBe(true);
    expect(viewer.view.openSettings).toHaveBeenCalledOnce();
    expect(commandKeys('settings.open')).toEqual([]);
    // So is Focus mode, whose keys turn it off again.
    expect(runCommand('view.focusMode', viewer)).toBe(true);
    expect(viewer.view.toggleFocusMode).toHaveBeenCalledOnce();
  });

  test('a command that asks for a choice opens the palette on it', () => {
    const ctx = context();

    runCommand('add.device', ctx);
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({
      command: 'add.device',
    });
    runCommand('goto.node', ctx);
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({ query: '@' });
  });

  test('choices run with what was chosen', () => {
    const { doc, alpha, sw } = sampleDocument();
    const ctx = context({ store: { doc } });
    const connect = getCommand('structure.connect');
    const [device] = connect.choices(ctx, []);
    const [target] = connect.choices(ctx, [device]);

    runCommand(connect, ctx, target, [device, target]);
    expect(ctx.store.connect).toHaveBeenCalledWith({
      sourceNodeId: alpha.id,
      sourceHandleId: null,
      targetNodeId: sw.id,
    });

    // Disconnect removes only the connection, which it names from the
    // device's end; there is nothing to disconnect without one.
    const disconnect = getCommand('structure.disconnect');
    const [link] = disconnect.choices(ctx);

    expect(link).toMatchObject({
      title: 'alpha (eth0) to EXP',
      detail: 'network EXP',
      disabled: '',
      value: doc.edges[0].id,
    });
    ctx.store.remove = vi.fn();
    runCommand(disconnect, ctx, link, [link]);
    expect(ctx.store.remove).toHaveBeenCalledWith({
      nodes: [],
      edges: [doc.edges[0].id],
    });
    expect(
      availability(
        disconnect,
        context({ store: { doc: { ...doc, edges: [] } } }),
      ),
    ).toBe('The diagram has no connections.');

    const router = getCommand('add.device')
      .choices(ctx)
      .find((choice) => choice.id === 'router');
    runCommand('add.device', ctx, router);
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'router',
        position: nextPosition(doc, { kind: 'device' }),
      }),
    );

    // Added in view, in a free spot, and shown.
    const area = { x: 2000, y: 1000, width: 700, height: 500 };
    ctx.view.visibleArea = () => area;
    ctx.store.addNode = vi.fn(() => ({ id: 'added' }));
    runCommand('add.device', ctx, router);
    const { position } = ctx.store.addNode.mock.calls[0][0];
    expect(position).toEqual(nextPosition(doc, { kind: 'device', area }));
    expect(position.x).toBeGreaterThanOrEqual(area.x);
    expect(position.y).toBeGreaterThanOrEqual(area.y);
    expect(ctx.view.revealNode).toHaveBeenCalledWith('added');
  });

  test('Save now commits the field and settles the Inspector first', async () => {
    const saved = context();

    saved.view.settleEdits = vi.fn(async () => '');
    await saveDraft(saved);
    expect(saved.view.settleEdits).toHaveBeenCalledOnce();
    expect(saved.store.saveNow).toHaveBeenCalledWith({
      announce: true,
      note: '',
    });

    // Edits the Inspector cannot apply stay, and the outcome says so.
    const left = context();
    const order = [];

    left.view.settleEdits = vi.fn(async () => {
      order.push('settle');

      return '1 field needs attention';
    });
    left.store.saveNow = vi.fn(async () => order.push('save'));
    runCommand('draft.save', left);
    await vi.waitFor(() => expect(left.store.saveNow).toHaveBeenCalled());
    expect(order).toEqual(['settle', 'save']);
    expect(left.store.saveNow).toHaveBeenCalledWith({
      announce: true,
      note: "The Inspector's changes are not applied yet: 1 field needs attention.",
    });

    // With nothing waiting, nothing is sent and no snapshot is made: the
    // key says so, with what the Inspector left out.
    const saveState = { status: 'saved', pending: 0 };
    const idle = context({
      store: { autosave: {}, queueWork: Promise.resolve(), saveState },
    });

    await saveDraft(idle);
    expect(idle.store.announce).toHaveBeenLastCalledWith(
      'No changes to save.',
      { slot: 'save' },
    );
    idle.view.settleEdits = vi.fn(async () => '1 field needs attention');
    await saveDraft(idle);
    expect(idle.store.announce).toHaveBeenLastCalledWith(
      "No changes to save. The Inspector's changes are not applied yet: 1 field needs attention.",
      { slot: 'save' },
    );
    expect(idle.store.saveNow).not.toHaveBeenCalled();

    // An edit the Inspector applied, or one still on its way to the queue,
    // is saved.
    idle.view.settleEdits = vi.fn(async () => {
      idle.store.queueWork = Promise.resolve();

      return '';
    });
    await saveDraft(idle);
    expect(idle.store.saveNow).toHaveBeenCalledOnce();

    const queued = context({
      store: { autosave: {}, queueWork: new Promise(() => {}), saveState },
    });

    await saveDraft(queued);
    expect(queued.store.saveNow).toHaveBeenCalledOnce();
  });

  test('Go to node selects and shows a node, or with Shift adds it', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const ctx = context({ store: { doc } });

    runCommand('goto.node', ctx, { value: alpha.id });
    expect(ctx.store.selection).toEqual({ nodes: [alpha.id], edges: [] });
    expect(ctx.view.showNode).toHaveBeenCalledWith(alpha.id);

    runCommand('goto.node', { ...ctx, additive: true }, { value: bravo.id });
    expect(ctx.store.selection.nodes).toEqual([alpha.id, bravo.id]);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Added bravo to the selection, 2 items selected',
    );

    // The palette stays open and says it on its own status line; the live
    // region would say it again, late, once the palette closed.
    ctx.store.announce.mockClear();
    runCommand(
      'goto.node',
      { ...ctx, additive: true, source: 'palette' },
      { value: alpha.id },
    );
    expect(ctx.store.selection.nodes).toEqual([bravo.id]);
    expect(ctx.store.announce).not.toHaveBeenCalled();
  });

  test('nodes are found by hostname, image, VLAN and addresses', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    node.device.spec.network.interfaces = [
      {
        name: 'eth0',
        vlan: 'EXP',
        address: '10.1.2.3',
        mac: '00:11:22:33:44:55',
      },
    ];

    const choice = nodeChoices(doc).find((entry) => entry.id === alpha.id);

    expect(choice.title).toBe('alpha');
    expect(choice.keywords).toEqual(
      expect.arrayContaining([
        'alpha',
        'ubuntu.qc2',
        'EXP',
        'VLAN 100',
        '100',
        '10.1.2.3',
        '00:11:22:33:44:55',
      ]),
    );
    expect(choice.detail).toBe('Device · ubuntu.qc2 · EXP');
    // Named, so the command palette can say which one matched.
    expect(choice.fields).toEqual([
      { label: 'Hostname', value: 'alpha' },
      { label: 'Image', value: 'ubuntu.qc2' },
      { label: 'Network', value: 'EXP' },
      { label: 'VLAN', value: 'VLAN 100' },
      { label: 'IP address', value: '10.1.2.3' },
      { label: 'MAC address', value: '00:11:22:33:44:55' },
    ]);
  });
});

describe('keys and hints', () => {
  test('follow the platform', () => {
    expect(shortcutLabel('edit.undo', { platform: 'mac' })).toBe('⌘Z');
    expect(shortcutLabel('edit.redo', { platform: 'mac' })).toBe('⇧⌘Z');
    expect(shortcutLabel('edit.redo', { platform: 'other' })).toBe(
      'Ctrl+Shift+Z or Ctrl+Y',
    );
    expect(ariaShortcuts('edit.redo', { platform: 'other' })).toBe(
      'Control+Shift+Z Control+Y',
    );
    expect(ariaShortcuts('edit.undo', { platform: 'mac' })).toBe('Meta+Z');
    expect(ariaShortcuts('structure.layout', { platform: 'mac' })).toBe(
      undefined,
    );
    expect(
      withShortcut('Group selection', 'structure.group', { platform: 'mac' }),
    ).toBe('Group selection (⌘G)');
    expect(withShortcut('Arrange', 'structure.layout')).toBe('Arrange');
  });

  test('follow the user’s keys and the single-key switch', () => {
    setShortcut('structure.layout', ['Mod+Shift+L'], noStorage());
    setShortcut('edit.undo', [], noStorage());
    // Local keys are the controls' own, and cannot change.
    setShortcut('edit.delete', ['Mod+Shift+D'], noStorage());

    expect(shortcutLabel('structure.layout', { platform: 'mac' })).toBe('⇧⌘L');
    expect(shortcutLabel('edit.undo', { platform: 'mac' })).toBe('');
    expect(commandKeys('edit.delete', { platform: 'other' })).toEqual([
      'Delete',
      'Backspace',
    ]);

    setSingleKeyShortcuts(false, noStorage());
    expect(commandKeys('shortcuts.open')).toEqual([]);
    expect(commandKeys('view.fit')).toEqual([]);
    expect(commandKeys('view.fit', { all: true })).toEqual(['Shift+1']);
    expect(commandKeys('palette.open', { platform: 'mac' })).toEqual(['Mod+K']);
  });

  test('a clash is found where both commands would answer', () => {
    expect(
      shortcutConflicts('structure.layout', 'Mod+D', { platform: 'mac' }).map(
        (command) => command.id,
      ),
    ).toEqual(['edit.duplicate']);
    // Canvas keys and outline keys never meet.
    expect(
      shortcutConflicts('outline.move', 'ArrowLeft', { platform: 'mac' }),
    ).toEqual([]);
    // Landing and editor commands never meet either.
    expect(
      shortcutConflicts('drafts.blank', 'Mod+Z', { platform: 'mac' }),
    ).toEqual([]);
  });

  test('the Keyboard help and the outline hint name this platform’s keys', () => {
    const mac = canvasHelp({ readOnly: false, platform: 'mac' }).join('\n');
    const other = canvasHelp({ readOnly: false, platform: 'other' }).join('\n');

    expect(mac).toContain('⌘A selects everything');
    expect(mac).toContain('⇧⌘Z redoes');
    expect(mac).toContain('Delete or Forward Delete deletes the focused item');
    expect(mac).toContain('= or + zooms in, − zooms out and ⇧1 fits');
    expect(mac).not.toMatch(/Ctrl|On a Mac/);
    expect(other).toContain('Ctrl+Shift+Z or Ctrl+Y redoes');
    expect(other).toContain('Ctrl+K opens the command palette');
    expect(other).toContain('Ctrl+Shift+F turns focus mode on or off');

    const readOnly = canvasHelp({ readOnly: true, platform: 'other' }).join(
      '\n',
    );
    expect(readOnly).toContain('This draft is read-only');
    expect(readOnly).toContain('Ctrl+C copies');
    expect(readOnly).not.toMatch(/pastes|Delete or/);

    expect(outlineHint({ readOnly: false, platform: 'mac' })).toContain(
      'F2 renames. Delete or Forward Delete removes the row',
    );
    expect(outlineHint({ readOnly: false, platform: 'other' })).toContain(
      'Enter or Space selects a row',
    );
    expect(outlineHint({ readOnly: true, platform: 'other' })).not.toMatch(
      /renames|removes/,
    );
    expect(canvasHints({ readOnly: false, platform: 'mac' }).node).toBe(
      'Return selects or deselects, ⇧Return adds to the selection, Escape ' +
        'clears the selection. Arrow keys move the selected nodes, Delete removes.',
    );
  });

  test('switched-off keys leave the help', () => {
    setSingleKeyShortcuts(false, noStorage());

    const help = canvasHelp({ readOnly: false, platform: 'other' }).join('\n');

    expect(help).not.toMatch(/zooms|lists every shortcut/);
  });
});

describe('focus scopes', () => {
  test('name where a key was pressed', () => {
    const t = targets();
    const root = rootFor(t);
    const scope = (target, editing = true) =>
      focusScope(target, { root, editing });

    // The rest of the page, such as the header link that led here, but not
    // its text fields or dialogs.
    expect(scope(t.outside)).toBe('page');
    expect(scope(t.outsideField)).toBe('outside');
    expect(scope(t.outsideModal)).toBe('outside');
    expect(scope(null)).toBe('outside');
    expect(scope(t.inDialog)).toBe('dialog');
    expect(scope(t.field)).toBe('field');
    expect(scope(t.canvas)).toBe('canvas');
    expect(scope(t.node)).toBe('canvas');
    // The canvas's own controls are not the canvas.
    expect(scope(t.zoomButton)).toBe('editor');
    expect(scope(t.row)).toBe('outline');
    expect(scope(t.button)).toBe('editor');
    // Focus that fell to the page.
    expect(scope(t.body)).toBe('editor');
    expect(scope(t.button, false)).toBe('landing');
    expect(scope(t.field, false)).toBe('field');
  });

  test('an open dialog keeps every key', () => {
    const t = targets(fakeDocument({ dialogOpen: true }));

    expect(focusScope(t.body, { root: rootFor(t), editing: true })).toBe(
      'dialog',
    );
  });
});

describe('the dispatcher', () => {
  function dispatch(event, ctx, t) {
    return dispatchKeydown(event, ctx, { root: rootFor(t) })?.id || null;
  }

  test.each(PLATFORMS)(
    'runs the platform’s keys where their scope reaches (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const ctx = context({ store: { canUndo: true } });
      const run = (target, key, code, mods = {}) => {
        const event = keydown(target, key, code, mods);

        return [dispatch(event, ctx, t), event.defaultPrevented];
      };

      // Mod+K and Mod+S work in text fields; the editing keys do not.
      expect(run(t.field, 'k', 'KeyK', mod(platform))).toEqual([
        'palette.open',
        true,
      ]);
      expect(ctx.view.openPalette).toHaveBeenLastCalledWith({});
      expect(run(t.field, 's', 'KeyS', mod(platform))).toEqual([
        'draft.save',
        true,
      ]);
      expect(run(t.field, 'z', 'KeyZ', mod(platform))).toEqual([null, false]);
      expect(run(t.field, '?', 'Slash', { shiftKey: true })).toEqual([
        null,
        false,
      ]);

      // The editing keys work anywhere else in the editor.
      expect(run(t.row, 'z', 'KeyZ', mod(platform))).toEqual([
        'edit.undo',
        true,
      ]);
      expect(run(t.node, 'z', 'KeyZ', mod(platform))).toEqual([
        'edit.undo',
        true,
      ]);
      expect(ctx.store.undo).toHaveBeenCalledTimes(2);
      expect(run(t.button, '?', 'Slash', { shiftKey: true })).toEqual([
        'shortcuts.open',
        true,
      ]);

      // Zoom keys only on the canvas, not its zoom buttons.
      expect(run(t.canvas, '=', 'Equal')).toEqual(['view.zoomIn', true]);
      expect(run(t.node, '-', 'Minus')).toEqual(['view.zoomOut', true]);
      expect(run(t.node, '!', 'Digit1', { shiftKey: true })).toEqual([
        'view.fit',
        true,
      ]);
      expect(run(t.zoomButton, '=', 'Equal')).toEqual([null, false]);
      expect(ctx.view.zoomIn).toHaveBeenCalledOnce();
      expect(ctx.view.fitView).toHaveBeenCalledOnce();

      // Go to node opens the palette at its prefix.
      expect(
        run(t.button, 'O', 'KeyO', { ...mod(platform), shiftKey: true }),
      ).toEqual(['goto.node', true]);
      expect(ctx.view.openPalette).toHaveBeenLastCalledWith({ query: '@' });

      // The other platform's modifier does nothing.
      const other = platform === 'mac' ? { ctrlKey: true } : { metaKey: true };
      expect(run(t.button, 'z', 'KeyZ', other)).toEqual([null, false]);
    },
  );

  test('redo is ⇧⌘Z on macOS, and also Ctrl+Y elsewhere', () => {
    const t = targets();
    const ctx = context({ store: { canRedo: true } });

    setPlatform('mac');
    expect(
      dispatch(keydown(t.button, 'y', 'KeyY', { metaKey: true }), ctx, t),
    ).toBe(null);
    expect(
      dispatch(
        keydown(t.button, 'z', 'KeyZ', { metaKey: true, shiftKey: true }),
        ctx,
        t,
      ),
    ).toBe('edit.redo');

    setPlatform('other');
    expect(
      dispatch(keydown(t.button, 'y', 'KeyY', { ctrlKey: true }), ctx, t),
    ).toBe('edit.redo');
    expect(ctx.store.redo).toHaveBeenCalledTimes(2);
  });

  test('leaves alone what others handle', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context({ store: { canUndo: true } });
    const handled = keydown(t.node, 'z', 'KeyZ', { ctrlKey: true });

    handled.preventDefault();
    expect(dispatch(handled, ctx, t)).toBe(null);
    expect(
      dispatch(keydown(t.inDialog, 'k', 'KeyK', { ctrlKey: true }), ctx, t),
    ).toBe(null);
    expect(
      dispatch(keydown(t.outsideField, 'k', 'KeyK', { ctrlKey: true }), ctx, t),
    ).toBe(null);
    expect(
      dispatch(keydown(t.outsideModal, 'k', 'KeyK', { ctrlKey: true }), ctx, t),
    ).toBe(null);
    expect(
      dispatch(
        keydown(t.field, 'k', 'KeyK', { ctrlKey: true, isComposing: true }),
        ctx,
        t,
      ),
    ).toBe(null);
    // Delete and Enter are the canvas's and the outline's own.
    expect(dispatch(keydown(t.node, 'Delete', 'Delete'), ctx, t)).toBe(null);
    expect(dispatch(keydown(t.row, 'Enter', 'Enter'), ctx, t)).toBe(null);
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });

  test.each(PLATFORMS)(
    'on the rest of the page, only the palette’s and the sheet’s keys answer (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const run = (ctx, key, code, mods = {}) => {
        const event = keydown(t.outside, key, code, mods);

        return [dispatch(event, ctx, t), event.defaultPrevented];
      };

      for (const editing of [true, false]) {
        const ctx = context({ store: { canUndo: true }, view: { editing } });

        expect(run(ctx, 'k', 'KeyK', mod(platform))).toEqual([
          'palette.open',
          true,
        ]);
        expect(run(ctx, '?', 'Slash', { shiftKey: true })).toEqual([
          'shortcuts.open',
          true,
        ]);
        expect(run(ctx, 'z', 'KeyZ', mod(platform))).toEqual([null, false]);
        expect(run(ctx, 's', 'KeyS', mod(platform))).toEqual([null, false]);
        expect(ctx.store.undo).not.toHaveBeenCalled();
      }
    },
  );

  test('a text field keeps every key that types a character', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    // As if the key had been given before the recorder refused it.
    setShortcut('palette.open', ['/'], noStorage());
    setShortcut('draft.save', ['Shift+1'], noStorage());

    const slash = keydown(t.field, '/', 'Slash');
    expect(dispatch(slash, ctx, t)).toBe(null);
    expect(slash.defaultPrevented).toBe(false);
    expect(
      dispatch(keydown(t.field, '!', 'Digit1', { shiftKey: true }), ctx, t),
    ).toBe(null);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.saveNow).not.toHaveBeenCalled();

    // Elsewhere, the key is the palette's.
    expect(dispatch(keydown(t.button, '/', 'Slash'), ctx, t)).toBe(
      'palette.open',
    );
    expect(worksInTextFields('palette.open')).toBe(true);
    expect(worksInTextFields('draft.save')).toBe(true);
    expect(worksInTextFields('edit.undo')).toBe(false);
    expect(worksInTextFields('view.zoomIn')).toBe(false);
  });

  test('on macOS, a text field keeps ⌥ keys without ⌘ or ⌃: they type', () => {
    setPlatform('mac');

    const t = targets();
    const ctx = context();

    // As if the keys had been given before the recorder refused them: ⌥E
    // is the acute accent's dead key, and ⌥⇧/ types ¿.
    setShortcut('palette.open', ['Alt+E'], noStorage());
    setShortcut('draft.save', ['Alt+Shift+/'], noStorage());

    const acute = keydown(t.field, 'Dead', 'KeyE', { altKey: true });
    expect(dispatch(acute, ctx, t)).toBe(null);
    expect(acute.defaultPrevented).toBe(false);
    expect(
      dispatch(
        keydown(t.field, '¿', 'Slash', { altKey: true, shiftKey: true }),
        ctx,
        t,
      ),
    ).toBe(null);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.saveNow).not.toHaveBeenCalled();
    expect(textFieldCommand(acute)).toBe(null);

    // Elsewhere, the key is the palette's; with ⌘ or ⌃ too, it types
    // nothing in the field either.
    expect(
      dispatch(keydown(t.button, 'Dead', 'KeyE', { altKey: true }), ctx, t),
    ).toBe('palette.open');
    setShortcut('palette.open', ['Ctrl+Alt+E'], noStorage());
    const ctrl = keydown(t.field, 'Dead', 'KeyE', {
      altKey: true,
      ctrlKey: true,
    });
    expect(dispatch(ctrl, ctx, t)).toBe('palette.open');
    expect(ctrl.defaultPrevented).toBe(true);
  });

  test('on Windows and Linux, Alt keys work in a text field: Alt types nothing', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    setShortcut('palette.open', ['Alt+E'], noStorage());

    const alt = keydown(t.field, 'e', 'KeyE', { altKey: true });
    expect(dispatch(alt, ctx, t)).toBe('palette.open');
    expect(alt.defaultPrevented).toBe(true);
  });

  test('names the command a key in a text field is for, for a field that keeps its other keys', () => {
    for (const platform of PLATFORMS) {
      setPlatform(platform);

      const t = targets();

      expect(
        textFieldCommand(keydown(t.field, 'k', 'KeyK', mod(platform)))?.id,
      ).toBe('palette.open');
      expect(
        textFieldCommand(keydown(t.field, 's', 'KeyS', mod(platform)))?.id,
      ).toBe('draft.save');
      // Keys that work outside text fields only, and the field's own.
      expect(
        textFieldCommand(keydown(t.field, 'z', 'KeyZ', mod(platform))),
      ).toBe(null);
      expect(textFieldCommand(keydown(t.field, 'Enter', 'Enter'))).toBe(null);
      expect(textFieldCommand(keydown(t.field, 'Escape', 'Escape'))).toBe(null);
    }

    // A customized key, as it is now.
    setPlatform('other');
    setShortcut('draft.save', ['Mod+Shift+S'], noStorage());
    expect(
      textFieldCommand(
        keydown(null, 'S', 'KeyS', { ctrlKey: true, shiftKey: true }),
      )?.id,
    ).toBe('draft.save');
    expect(
      textFieldCommand(keydown(null, 's', 'KeyS', { ctrlKey: true })),
    ).toBe(null);
  });

  test('takes a matched key even when its command cannot run, and says why', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context({ store: { readOnly: true } });
    const paste = keydown(t.node, 'v', 'KeyV', { ctrlKey: true });

    expect(dispatch(paste, ctx, t)).toBe('edit.paste');
    expect(paste.defaultPrevented).toBe(true);
    expect(ctx.store.paste).not.toHaveBeenCalled();
    expect(ctx.store.announce).toHaveBeenCalledWith(READ_ONLY);

    // F2 on a read-only outline row, which renames nothing there.
    dispatch(keydown(t.row, 'F2', 'F2'), ctx, t);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(READ_ONLY);
  });

  test('leaves selected text to the browser’s copy', () => {
    setPlatform('other');
    vi.stubGlobal('window', { getSelection: () => 'selected words' });

    const t = targets();
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id], edges: [] } },
    });
    const copy = keydown(t.button, 'c', 'KeyC', { ctrlKey: true });

    expect(dispatch(copy, ctx, t)).toBe(null);
    expect(copy.defaultPrevented).toBe(false);
    expect(ctx.store.copy).not.toHaveBeenCalled();
  });

  test('F2 on a canvas node renames it in the Inspector', () => {
    setPlatform('mac');

    const { doc, alpha } = sampleDocument();
    const t = targets();
    const node = element(t.doc, ['item'], ['canvas'], {
      classes: ['vue-flow__node'],
      attrs: { 'data-id': alpha.id },
    });
    const ctx = context({ store: { doc } });

    expect(dispatch(keydown(node, 'F2', 'F2'), ctx, t)).toBe('edit.rename');
    expect(ctx.store.selection).toEqual({ nodes: [alpha.id], edges: [] });
    expect(ctx.view.focusInspector).toHaveBeenCalledWith({ field: true });
  });

  test('on the landing, only the landing’s commands answer', () => {
    setPlatform('mac');

    const t = targets();
    const ctx = context({ store: { canUndo: true }, view: { editing: false } });

    expect(
      dispatch(keydown(t.button, 'k', 'KeyK', { metaKey: true }), ctx, t),
    ).toBe('palette.open');
    expect(dispatch(keydown(t.button, '?', 'Slash'), ctx, t)).toBe(
      'shortcuts.open',
    );
    expect(
      dispatch(keydown(t.button, 'z', 'KeyZ', { metaKey: true }), ctx, t),
    ).toBe(null);
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });

  test('follows the user’s keys and the single-key switch', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    setShortcut('structure.layout', ['Mod+Shift+L'], noStorage());
    expect(
      dispatch(
        keydown(t.button, 'L', 'KeyL', { ctrlKey: true, shiftKey: true }),
        ctx,
        t,
      ),
    ).toBe('structure.layout');
    expect(ctx.store.layout).toHaveBeenCalledOnce();

    setSingleKeyShortcuts(false, noStorage());
    const question = keydown(t.button, '?', 'Slash', { shiftKey: true });

    expect(dispatch(question, ctx, t)).toBe(null);
    expect(question.defaultPrevented).toBe(false);
  });
});
