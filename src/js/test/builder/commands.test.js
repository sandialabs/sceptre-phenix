import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { DUPLICATE_NEEDS_NODES } from '@/builder/clipboard.js';
import {
  COMMANDS,
  GROUPS,
  READ_ONLY,
  SCOPES,
  TYPING,
  VIEW_API,
  ariaShortcuts,
  availability,
  canvasHints,
  commandKeys,
  commandTitle,
  createCommandContext,
  defaultKeys,
  dispatchKeydown,
  focusScope,
  getCommand,
  isCustomizable,
  isTextEntry,
  nodeChoices,
  outlineHint,
  paletteCommands,
  runCommand,
  saveDraft,
  shortcutConflicts,
  shortcutLabel,
  takesLetters,
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

import { nextPosition, paletteNode } from '@/components/builder/paletteDnd.js';

import { sampleDocument, withTemplates } from './fixtures.js';

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
    'autoGroup',
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

  return withTemplates(store);
}

function fakeView(overrides = {}) {
  const view = {
    editing: true,
    dialog: '',
    showMinimap: true,
    showNodeNotes: true,
    minimapSize: { width: 200, min: 120, max: 400 },
    panes: { hidden: { start: false, end: false }, stacked: false },
    canZoomIn: true,
    canZoomOut: true,
    fitRestores: false,
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
  checkbox: (selector) =>
    selector === TYPING || selector === 'input[type="checkbox"]',
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
    checkbox: element(doc, ['checkbox']),
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

describe('isTextEntry', () => {
  const input = (type, extra = {}) => ({ tagName: 'INPUT', type, ...extra });

  test('text inputs and text areas hold typed text', () => {
    for (const type of ['text', 'number', 'search', 'email', 'url']) {
      expect(isTextEntry(input(type)), type).toBe(true);
    }
    expect(isTextEntry({ tagName: 'TEXTAREA' })).toBe(true);
  });

  test('choices, buttons, files and colors do not, nor read-only fields', () => {
    for (const type of [
      'checkbox',
      'radio',
      'button',
      'submit',
      'reset',
      'file',
      'color',
    ]) {
      expect(isTextEntry(input(type)), type).toBe(false);
    }
    expect(isTextEntry(input('text', { readOnly: true }))).toBe(false);
    expect(isTextEntry({ tagName: 'SELECT' })).toBe(false);
    expect(isTextEntry({ tagName: 'BUTTON' })).toBe(false);
    expect(isTextEntry(null)).toBe(false);
  });
});

describe('the registry', () => {
  test('aliases are lower-case single words', () => {
    const aliased = COMMANDS.filter((command) => command.aliases);

    for (const command of aliased) {
      expect(command.aliases.length, command.id).toBeGreaterThan(0);
      for (const alias of command.aliases) {
        expect(alias, command.id).toMatch(/^[a-z]+$/);
      }
    }
    // The word Download had before, and another word for Share.
    for (const command of COMMANDS) {
      if (command.id.startsWith('draft.download')) {
        expect(command.aliases, command.id).toEqual(['export']);
      }
    }
    expect(getCommand('draft.share').aliases).toEqual(['send']);
  });

  // The Download dialog starts the format's download as it opens, so its
  // result and its errors show where a press of its button shows them.
  test('each download format is a command that starts it in the dialog', () => {
    const formats = {
      json: 'Download Builder JSON',
      yaml: 'Download Builder YAML',
      topology: 'Download Topology YAML',
      png: 'Download PNG',
      svg: 'Download SVG',
      gexf: 'Download Gephi (GEXF)',
    };

    for (const [format, title] of Object.entries(formats)) {
      // A read-only draft can be downloaded too.
      const ctx = context({ store: { readOnly: true } });
      const command = getCommand(`draft.download.${format}`);

      expect(command, format).toMatchObject({ title, group: 'Draft' });
      expect(command.keys, format).toBeUndefined();
      expect(command.detail(ctx), format).toBeTruthy();
      expect(runCommand(command, ctx), format).toBe(true);
      expect(ctx.view.openDialog, format).toHaveBeenCalledExactlyOnceWith(
        'download',
        { start: format },
      );
    }

    // Download… asks which format in the dialog, and starts none.
    const ctx = context();
    expect(runCommand('draft.download', ctx)).toBe(true);
    expect(ctx.view.openDialog).toHaveBeenCalledExactlyOnceWith('download');
  });

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
          // A user could choose it for the command (a letter alone only for
          // a command whose keys work on the canvas alone).
          if (isCustomizable(command)) {
            expect(
              keyRefusal(key, platform, { letters: takesLetters(command) }),
              where,
            ).toBe('');
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
    // Settings, the layout and the first Auto-group rule: Alt (Option) and
    // Shift with a letter, the same on both platforms, and nowhere in a
    // text field, where Option types a character on a Mac.
    for (const platform of PLATFORMS) {
      expect(keys('settings.open', platform)).toEqual(['Alt+Shift+S']);
      expect(keys('structure.layout', platform)).toEqual(['Alt+Shift+L']);
      expect(keys('structure.autoGroup.network', platform)).toEqual([
        'Alt+Shift+G',
      ]);
    }
    expect(keys('structure.autoGroup.name', 'mac')).toEqual([]);
    expect(keys('structure.autoGroup.pattern', 'mac')).toEqual([]);
    expect(isCustomizable('structure.autoGroup.pattern')).toBe(true);
    for (const id of [
      'settings.open',
      'structure.layout',
      'structure.autoGroup.network',
    ]) {
      expect(worksInTextFields(id), id).toBe(false);
      expect(isCustomizable(id), id).toBe(true);
    }
    // Settings opens from the drafts page and the app header too.
    expect(getCommand('settings.open')).toMatchObject({
      page: true,
      views: ['editor', 'landing'],
    });
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
        'view.focusMode',
      ]),
    );
    expect(editor).toContain('settings.open');
    // Download…, then a command for each format, in the dialog's order.
    expect(editor.filter((id) => id.startsWith('draft.download'))).toEqual([
      'draft.download',
      'draft.download.json',
      'draft.download.yaml',
      'draft.download.topology',
      'draft.download.png',
      'draft.download.svg',
      'draft.download.gexf',
    ]);
    expect(landing.some((id) => id.startsWith('draft.download'))).toBe(false);
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
      'structure.layout.cards',
      'structure.restoreLayout',
      'structure.autoGroup.network',
      'structure.autoGroup.name',
      'structure.autoGroup.pattern',
      'structure.connect',
      'structure.disconnect',
      'add.device',
      'add.switch',
      'add.rectangle',
      'add.circle',
      'add.icon',
      'add.line',
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
      'draft.download',
      'draft.history',
      'palette.open',
      'view.fit',
    ]) {
      expect(availability(id, ctx), id).toBe(true);
    }

    // A role that may not create drafts or publish gets neither.
    const viewer = { canCreateDrafts: false, canPublish: false };
    const editor = context({ store: viewer });
    const landing = context({ store: viewer, view: { editing: false } });

    expect(availability('draft.upload', editor)).toBe(
      'Your role cannot create drafts.',
    );
    expect(availability('draft.publish', editor)).toBe(
      'Your role cannot publish diagrams.',
    );
    expect(availability('draft.download', editor)).toBe(true);
    for (const id of ['drafts.blank', 'drafts.import', 'drafts.upload']) {
      expect(availability(id, landing), id).toBe(
        'Your role cannot create drafts.',
      );
    }
  });

  // A connection is copied only with both of its nodes, so connections
  // alone have nothing to duplicate, and Duplicate says what to select.
  test('Duplicate needs a selected node, and says so for connections alone', () => {
    const { doc } = sampleDocument();
    const node = doc.nodes.find((item) => item.kind === 'device').id;
    const edge = doc.edges[0].id;
    const duplicate = getCommand('edit.duplicate');
    const selecting = (selection) => context({ store: { selection } });

    for (const [selection, expected] of [
      [{ nodes: [], edges: [] }, 'Nothing is selected to duplicate.'],
      [{ nodes: [], edges: [edge] }, DUPLICATE_NEEDS_NODES],
      [{ nodes: [node], edges: [] }, true],
      [{ nodes: [node], edges: [edge] }, true],
    ]) {
      const label = JSON.stringify(selection);

      expect(duplicate.when(selecting(selection)), label).toBe(expected);
      expect(availability(duplicate, selecting(selection)), label).toBe(
        expected,
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
    expect(getCommand('drafts.tab.shared').title).toBe('Show Shared Drafts');
    // Other users' drafts is a tab only while it lists something.
    expect(availability('drafts.tab.others', landing)).toBe(
      "No other users' drafts are listed.",
    );
    expect(
      availability(
        'drafts.tab.others',
        context({
          view: { editing: false, draftsTab: 'mine' },
          store: { damagedDrafts: { mine: [], others: [{ id: 'x' }] } },
        }),
      ),
    ).toBe(true);
  });

  test('Combine included nodes is offered only where there are some, and makes a draft', () => {
    const listed = (ctx) =>
      paletteCommands(ctx).find(
        (command) => command.id === 'draft.combineIncluded',
      );
    const some = context({ store: { summary: { included: 3 } } });
    const one = context({ store: { summary: { included: 1 } } });
    const none = context({ store: { summary: { included: 0 } } });

    expect(listed(some).title).toBe('Combine included nodes into a new draft');
    expect(listed(some).group).toBe('Draft');
    expect(listed(some).detail(some)).toBe(
      '3 included nodes become editable in a copy of this diagram',
    );
    expect(listed(one).detail(one)).toBe(
      '1 included node becomes editable in a copy of this diagram',
    );
    expect(listed(none)).toBeUndefined();
    // Never on the drafts page, where no diagram is open.
    expect(
      listed(
        context({
          store: { summary: { included: 3 } },
          view: { editing: false },
        }),
      ),
    ).toBeUndefined();

    expect(availability('draft.combineIncluded', some)).toBe(true);
    expect(runCommand('draft.combineIncluded', some)).toBe(true);
    expect(some.view.combineIncluded).toHaveBeenCalledOnce();
    expect(getCommand('draft.combineIncluded').keys).toBeUndefined();

    // It leaves the open draft as it is, so a view-only one can be combined.
    const viewOnly = context({
      store: { summary: { included: 3 }, readOnly: true },
    });

    expect(availability('draft.combineIncluded', viewOnly)).toBe(true);

    // A role that cannot create drafts is told why not.
    const viewer = context({
      store: { summary: { included: 3 }, canCreateDrafts: false },
    });

    expect(listed(viewer)).toBeTruthy();
    expect(availability('draft.combineIncluded', viewer)).toBe(
      'Your role cannot create drafts.',
    );
    expect(runCommand('draft.combineIncluded', viewer)).toBe(false);
    expect(viewer.view.combineIncluded).not.toHaveBeenCalled();
  });

  test('Open experiment is offered only while the diagram was published with one', () => {
    const listed = (ctx) =>
      paletteCommands(ctx).find((command) => command.id === 'draft.experiment');
    const some = context({ store: { experimentName: 'lab-exp' } });
    const none = context({ store: { experimentName: '' } });

    expect(listed(some).title).toBe('Open experiment');
    expect(listed(some).group).toBe('Draft');
    expect(listed(some).detail(some)).toBe('lab-exp');
    expect(listed(none)).toBeUndefined();
    // Never on the drafts page: each published diagram's card has its own.
    expect(
      listed(
        context({
          store: { experimentName: 'lab-exp' },
          view: { editing: false },
        }),
      ),
    ).toBeUndefined();

    expect(availability('draft.experiment', some)).toBe(true);
    expect(runCommand('draft.experiment', some)).toBe(true);
    expect(some.view.openExperiment).toHaveBeenCalledOnce();
    expect(some.view.openExperiment).toHaveBeenCalledWith();
    // It reads nothing and changes nothing, so a view-only draft has it.
    expect(
      availability(
        'draft.experiment',
        context({ store: { experimentName: 'lab-exp', readOnly: true } }),
      ),
    ).toBe(true);

    // A key given to it says why it does nothing without an experiment.
    expect(availability('draft.experiment', none)).toBe(
      'This diagram was not published with an experiment.',
    );
    expect(runCommand('draft.experiment', none)).toBe(false);
    expect(none.view.openExperiment).not.toHaveBeenCalled();
    expect(getCommand('draft.experiment').keys).toBeUndefined();
    expect(getCommand('draft.experiment').phrase).toBeUndefined();
  });

  test('Share is the owner’s; a draft shared with the user says whose', () => {
    const owner = context({ store: { canShare: true } });
    const recipient = context({ store: { sharedBy: 'alice' } });
    const neither = context();
    const listed = (ctx) =>
      paletteCommands(ctx).some((command) => command.id === 'draft.share');

    expect(availability('draft.share', owner)).toBe(true);
    expect(runCommand('draft.share', owner)).toBe(true);
    expect(owner.view.openDialog).toHaveBeenCalledWith('share');
    expect(listed(owner)).toBe(true);

    expect(availability('draft.share', recipient)).toBe(
      'Only alice can change who has access.',
    );
    expect(listed(recipient)).toBe(true);

    // Not offered on anyone else's draft, or a diagram with no draft.
    expect(listed(neither)).toBe(false);
    expect(getCommand('draft.share').keys).toBeUndefined();

    // The lists' drafts, shared and others' included, can be opened.
    const landing = context({
      view: { editing: false },
      store: {
        drafts: {
          mine: [{ id: 'a', owner: 'me', title: 'Mine' }],
          shared: [{ id: 'b', owner: 'bob', title: 'Theirs' }],
          others: [{ id: 'c', owner: 'carol', title: 'Seen' }],
        },
      },
    });
    const [command] = paletteCommands(landing).filter(
      (entry) => entry.id === 'drafts.open',
    );
    expect(command.choices(landing).map((choice) => choice.detail)).toEqual([
      'My Drafts · Owner: me',
      'Shared Drafts · Owner: bob',
      "Other users' drafts · Owner: carol",
    ]);
  });

  // A topology read from its Builder file is listed with the published
  // diagrams, under a handle: it is named by its topology, says it is a
  // file, is found by its path, and opens as any listed diagram does.
  test('the diagram of a topology read from its Builder file can be opened', () => {
    const file = {
      source: 'file',
      id: 'file/plant',
      kind: 'Topology',
      target: 'plant',
      path: '/phenix/topologies/plant.builder.json',
    };
    const landing = context({
      view: { editing: false },
      store: {
        drafts: { mine: [], shared: [], others: [] },
        documents: [
          { source: 'store', id: 'p1', kind: 'Topology', target: 'core' },
          file,
        ],
      },
    });
    const command = getCommand('drafts.open');
    const choices = command.choices(landing);

    expect(
      choices.map(({ id, title, detail }) => ({ id, title, detail })),
    ).toEqual([
      {
        id: 'Published Diagrams::p1',
        title: 'core',
        detail: 'Published Diagrams',
      },
      {
        id: 'Published Diagrams::file/plant',
        title: 'plant',
        detail:
          'Published Diagrams · File: /phenix/topologies/plant.builder.json',
      },
    ]);
    expect(choices[1].keywords).toEqual([
      'plant',
      '/phenix/topologies/plant.builder.json',
    ]);

    command.run(landing, choices[1]);
    expect(landing.view.openDraft).toHaveBeenCalledWith(file);
  });

  test('titles follow the state', () => {
    expect(commandTitle('view.minimap', context())).toBe('Hide minimap');
    expect(
      commandTitle('view.minimap', context({ view: { showMinimap: false } })),
    ).toBe('Show minimap');
    expect(commandTitle('view.nodeNotes', context())).toBe('Hide node notes');
    expect(
      commandTitle(
        'view.nodeNotes',
        context({ view: { showNodeNotes: false } }),
      ),
    ).toBe('Show node notes');
    expect(commandTitle('view.fit', context())).toBe('Fit diagram to view');
    expect(
      commandTitle('view.fit', context({ view: { fitRestores: true } })),
    ).toBe('Restore previous view');
    expect(commandTitle('view.focusMode', context())).toBe('Focus mode');
    expect(
      commandTitle('view.focusMode', context({ view: { focusMode: true } })),
    ).toBe('Exit focus mode');
    expect(commandTitle('view.pane.start', context())).toBe(
      'Hide Add nodes and Outline',
    );
    expect(
      commandTitle(
        'view.pane.end',
        context({ view: { panes: { hidden: { end: true } } } }),
      ),
    ).toBe('Show Inspector');

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
    // The header's Settings too, with a key of its own.
    expect(runCommand('settings.open', viewer)).toBe(true);
    expect(viewer.view.openSettings).toHaveBeenCalledOnce();
    expect(commandKeys('settings.open')).toEqual(['Alt+Shift+S']);
    // So is Focus mode, whose keys turn it off again.
    expect(runCommand('view.focusMode', viewer)).toBe(true);
    expect(viewer.view.toggleFocusMode).toHaveBeenCalledOnce();
    // And the side columns' Hide toggles, with no keys until the user gives
    // them some; the stacked layout shows every column, so they wait.
    expect(runCommand('view.pane.end', viewer)).toBe(true);
    expect(viewer.view.togglePane).toHaveBeenCalledWith('end');
    expect(commandKeys('view.pane.start')).toEqual([]);
    const stacked = context({
      view: { panes: { hidden: { start: true }, stacked: true } },
    });
    expect(runCommand('view.pane.start', stacked)).toBe(false);
    expect(stacked.view.togglePane).not.toHaveBeenCalled();
    expect(stacked.store.announce).toHaveBeenCalledWith(
      'The window is too narrow to hide columns.',
    );
  });

  test("the minimap's sizes, for a pointer that does not drag its handle", () => {
    const ctx = context({
      view: { minimapSize: { width: 200, min: 120, max: 400 } },
    });
    const offered = paletteCommands(ctx).filter((command) =>
      command.id.startsWith('view.minimapSize.'),
    );

    expect(offered.map((command) => commandTitle(command, ctx))).toEqual([
      'Minimap size: Smallest',
      'Minimap size: Default',
      'Minimap size: Large',
      'Minimap size: Largest',
    ]);
    expect(offered.map((command) => command.detail(ctx))).toEqual([
      '120 by 90 pixels',
      '200 by 150 pixels',
      '300 by 225 pixels',
      '400 by 300 pixels',
    ]);

    expect(runCommand('view.minimapSize.large', ctx)).toBe(true);
    expect(ctx.view.resizeMinimap).toHaveBeenCalledWith(300);
    expect(runCommand('view.minimapSize.default', ctx)).toBe(false);
    expect(ctx.store.announce).toHaveBeenCalledWith(
      'This is the current size.',
    );

    const hidden = context({
      view: {
        showMinimap: false,
        minimapSize: { width: 200, min: 120, max: 400 },
      },
    });
    expect(runCommand('view.minimapSize.largest', hidden)).toBe(false);
    expect(hidden.store.announce).toHaveBeenCalledWith(
      'The minimap is hidden.',
    );
  });

  test('a command per layout lays out with it, and one per Auto-group way', () => {
    const ctx = context({
      store: { currentLayout: 'cards', layoutToRun: 'cards' },
    });
    const layouts = COMMANDS.filter((command) =>
      command.id.startsWith('structure.layout.'),
    );

    expect(layouts.map((command) => command.title)).toEqual([
      'Layout: ELK layered',
      'Layout: Layered by tier',
      'Layout: Network cards',
      'Layout: Dagre',
      'Layout: Standard',
    ]);
    // The draft's layout is marked, and can run again.
    expect(commandTitle('structure.layout.cards', ctx)).toBe(
      'Layout: Network cards',
    );
    expect(getCommand('structure.layout.cards').detail(ctx)).toBe(
      'A card per network, on a grid · Current layout',
    );
    expect(getCommand('structure.layout').detail(ctx)).toBe(
      'Arrange the nodes with Network cards',
    );
    expect(runCommand('structure.layout.cards', ctx)).toBe(true);
    expect(ctx.store.layout).toHaveBeenLastCalledWith({ algorithm: 'cards' });
    expect(runCommand('structure.layout.dagre', ctx)).toBe(true);
    expect(ctx.store.layout).toHaveBeenLastCalledWith({ algorithm: 'dagre' });
    expect(runCommand('structure.layout.tiers', ctx)).toBe(true);
    expect(ctx.store.layout).toHaveBeenLastCalledWith({ algorithm: 'tiers' });
    // Auto layout runs the draft's layout.
    expect(runCommand('structure.layout', ctx)).toBe(true);
    expect(ctx.store.layout).toHaveBeenLastCalledWith();

    // A draft with none (Default) has none marked, and Auto layout names
    // the Settings default it runs.
    const blank = context({ store: { currentLayout: '', layoutToRun: 'elk' } });
    expect(
      layouts.map((command) => command.detail(blank)).join(' '),
    ).not.toContain('Current layout');
    expect(getCommand('structure.layout').detail(blank)).toBe(
      'Arrange the nodes with ELK layered',
    );

    expect(runCommand('structure.autoGroup.network', ctx)).toBe(true);
    expect(ctx.store.autoGroup).toHaveBeenLastCalledWith('network');
    expect(runCommand('structure.autoGroup.name', ctx)).toBe(true);
    expect(ctx.store.autoGroup).toHaveBeenLastCalledWith('name');
    expect(getCommand('structure.autoGroup.name').title).toBe(
      'Auto-group by name',
    );

    // The rule that needs a pattern opens the dialog that takes it, and
    // runs nothing by itself.
    const pattern = getCommand('structure.autoGroup.pattern');

    ctx.store.autoGroup.mockClear();
    expect(pattern).toMatchObject({
      title: 'Auto-group by name pattern…',
      group: 'Structure',
    });
    expect(pattern.keywords).toEqual([
      'auto group',
      'cluster',
      'organize',
      'regex',
      'regexp',
      'regular expression',
    ]);
    expect(pattern).not.toHaveProperty('phrase');
    expect(pattern.detail(ctx)).toBe(
      'Nodes whose names match a regular expression',
    );
    expect(
      pattern.detail(
        context({ store: { selection: { nodes: ['a'], edges: [] } } }),
      ),
    ).toBe(
      'Nodes whose names match a regular expression · Selected nodes only',
    );
    expect(runCommand('structure.autoGroup.pattern', ctx)).toBe(true);
    expect(ctx.view.openDialog).toHaveBeenLastCalledWith('group-pattern');
    expect(ctx.store.autoGroup).not.toHaveBeenCalled();
    expect(getCommand('structure.autoGroup.name').keywords).toEqual([
      'auto group',
      'cluster',
      'organize',
    ]);
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
      .find((choice) => choice.id === 'builtin:router');
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
    expect(ariaShortcuts('view.reset', { platform: 'mac' })).toBe(undefined);
    expect(
      withShortcut('Group selection', 'structure.group', { platform: 'mac' }),
    ).toBe('Group selection (⌘G)');
    expect(withShortcut('Reset', 'view.reset')).toBe('Reset');
    // Option and Shift with a letter, as each platform writes it.
    expect(shortcutLabel('settings.open', { platform: 'mac' })).toBe('⌥⇧S');
    expect(shortcutLabel('settings.open', { platform: 'other' })).toBe(
      'Alt+Shift+S',
    );
    expect(ariaShortcuts('settings.open', { platform: 'other' })).toBe(
      'Alt+Shift+S',
    );
    expect(
      withShortcut('Builder settings', 'settings.open', { platform: 'mac' }),
    ).toBe('Builder settings (⌥⇧S)');
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

  test('a clash is found on the platform: Mod is Command on a Mac, Ctrl elsewhere', () => {
    const clashes = (spec, platform) =>
      shortcutConflicts('structure.layout', spec, { platform }).map(
        (command) => command.id,
      );

    expect(clashes('Meta+D', 'mac')).toEqual(['edit.duplicate']);
    expect(clashes('Ctrl+D', 'mac')).toEqual([]);
    expect(clashes('Ctrl+D', 'other')).toEqual(['edit.duplicate']);
    expect(clashes('Meta+D', 'other')).toEqual([]);
  });

  test('the hints name this platform’s keys', () => {
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
      'Arrow keys move between nodes, Page Down through this node’s ' +
        'connections. Return selects or deselects, ⇧Return adds to the ' +
        'selection, Escape clears the selection. Shift and an arrow key ' +
        'move the selected nodes, Delete removes.',
    );
  });

  // The canvas is one Tab stop: the arrow keys move focus, Page Down and
  // Page Up go through a node's connections, and Shift with an arrow key
  // moves the selected nodes. The canvas's description also carries the
  // advice for screen readers, and says where every shortcut is listed.
  test('the canvas hints give the keys that move focus and nodes', () => {
    const hints = canvasHints({ readOnly: false, platform: 'other' });
    expect(hints.edge).toMatch(
      /^Arrow keys move to the nodes, Page Down to the next connection\./,
    );
    expect(hints.canvas).toBe(
      'Arrow keys move to the nodes, and Enter selects the focused one. ' +
        'With a screen reader, turn on its focus mode (forms mode in JAWS) ' +
        'for these keys, or use the Outline. ? lists every shortcut.',
    );
    expect(canvasHints({ readOnly: false, platform: 'mac' }).canvas).toBe(
      'Arrow keys move to the nodes, and Return selects the focused one. ' +
        'With a screen reader, turn on its focus mode (forms mode in JAWS) ' +
        'for these keys, or use the Outline. ? lists every shortcut.',
    );
    expect(canvasHints({ readOnly: true, platform: 'other' }).canvas).toBe(
      `Read-only draft. ${hints.canvas}`,
    );
    expect(canvasHints({ readOnly: true, platform: 'other' }).node).not.toMatch(
      /arrow key move|removes/,
    );
  });

  test('the canvas hint follows the key of the shortcut sheet', () => {
    setShortcut('shortcuts.open', ['Mod+/'], noStorage());
    expect(canvasHints({ readOnly: false, platform: 'mac' }).canvas).toMatch(
      /or use the Outline\. ⌘\/ lists every shortcut\.$/,
    );

    // Without a key, it names the button that opens the sheet.
    loadShortcutSettings(noStorage());
    setSingleKeyShortcuts(false, noStorage());

    for (const readOnly of [false, true]) {
      const { canvas } = canvasHints({ readOnly, platform: 'other' });

      expect(canvas).toMatch(
        /or use the Outline\. Shortcuts in the header lists every shortcut\.$/,
      );
      expect(canvas).not.toContain('?');
    }
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
    // A checkbox of the drafts landing selects a card and takes no typed
    // text; one in the editor is a field of the Inspector's form.
    expect(scope(t.checkbox, false)).toBe('landing');
    expect(scope(t.checkbox)).toBe('field');
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

  test('N on the canvas adds the plain Device in view, without asking, and focuses it', () => {
    const t = targets();
    const { doc } = sampleDocument();
    const ctx = context({ store: { doc } });
    const press = (target, mods = {}) => {
      const event = keydown(target, 'n', 'KeyN', mods);

      return [dispatch(event, ctx, t), event.defaultPrevented];
    };

    ctx.store.addNode = vi.fn(() => ({ id: 'added' }));

    expect(press(t.canvas)).toEqual(['add.device', true]);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.addNode).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        kind: 'device',
        position: nextPosition(doc, { kind: 'device' }),
      }),
    );
    // The plain Device, as the palette's Add Device button adds it.
    expect(ctx.store.addNode.mock.calls[0][0]).toEqual(
      expect.objectContaining(paletteNode(ctx.store, 'device', '')),
    );
    expect(ctx.view.revealNode).toHaveBeenCalledWith('added');
    expect(ctx.view.showNode).toHaveBeenCalledWith('added');

    // On a focused node too: the canvas's scope.
    expect(press(t.node)).toEqual(['add.device', true]);

    // Not in a text field (the Inspector, the outline's rename field),
    // nor on an outline row or elsewhere in the editor, nor with Shift.
    expect(press(t.field)).toEqual([null, false]);
    expect(press(t.row)).toEqual([null, false]);
    expect(press(t.button)).toEqual([null, false]);
    expect(press(t.canvas, { shiftKey: true })).toEqual([null, false]);
    expect(ctx.store.addNode).toHaveBeenCalledTimes(2);

    // The palette's Add device still asks, and leaves focus where it is.
    ctx.view.showNode.mockClear();
    runCommand('add.device', { ...ctx, source: 'palette' });
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({
      command: 'add.device',
    });
    runCommand('add.device', { ...ctx, source: 'palette' }, { value: '' });
    expect(ctx.view.showNode).not.toHaveBeenCalled();

    // A read-only draft adds nothing, and says why.
    const readOnly = context({ store: { readOnly: true } });

    expect(dispatch(keydown(t.canvas, 'n', 'KeyN'), readOnly, t)).toBe(
      'add.device',
    );
    expect(readOnly.store.addNode).not.toHaveBeenCalled();
    expect(readOnly.store.announce).toHaveBeenCalledWith(READ_ONLY);

    // A letter alone is for the canvas only; the single-key switch turns
    // it off.
    expect(takesLetters('add.device')).toBe(true);
    expect(takesLetters('view.zoomIn')).toBe(true);
    expect(takesLetters('view.reset')).toBe(false);
    expect(takesLetters('edit.rename')).toBe(false);
    setSingleKeyShortcuts(false, null);
    expect(press(t.canvas)).toEqual([null, false]);
    setSingleKeyShortcuts(true, null);
  });

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
    // Focus mode is the drafts' too, so it can be left there.
    expect(
      dispatch(
        keydown(t.button, 'F', 'KeyF', { metaKey: true, shiftKey: true }),
        ctx,
        t,
      ),
    ).toBe('view.focusMode');
    expect(ctx.view.toggleFocusMode).toHaveBeenCalledOnce();
    expect(
      dispatch(keydown(t.button, 'z', 'KeyZ', { metaKey: true }), ctx, t),
    ).toBe(null);
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });

  // Option with a letter types another character on a Mac ('Í' for ⌥⇧S),
  // so these keys match the physical key, and a text field keeps them.
  test.each(PLATFORMS)(
    'Alt+Shift with S, L and G open Settings, lay out and auto-group (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const ctx = context();
      const typed = platform === 'mac' ? { S: 'Í', L: 'Ò', G: '˝' } : {};
      const run = (target, letter, on = ctx) => {
        const event = keydown(target, typed[letter] || letter, `Key${letter}`, {
          altKey: true,
          shiftKey: true,
        });

        return [dispatch(event, on, t), event.defaultPrevented];
      };

      expect(run(t.button, 'S')).toEqual(['settings.open', true]);
      expect(run(t.canvas, 'S')).toEqual(['settings.open', true]);
      expect(run(t.row, 'L')).toEqual(['structure.layout', true]);
      expect(run(t.node, 'G')).toEqual(['structure.autoGroup.network', true]);
      expect(ctx.view.openSettings).toHaveBeenCalledTimes(2);
      expect(ctx.store.layout).toHaveBeenCalledExactlyOnceWith();
      expect(ctx.store.autoGroup).toHaveBeenCalledExactlyOnceWith('network');

      // Not in a text field, nor in a dialog, on either platform.
      for (const letter of ['S', 'L', 'G']) {
        expect(run(t.field, letter), letter).toEqual([null, false]);
        expect(run(t.inDialog, letter), letter).toEqual([null, false]);
      }

      // Without Shift, or with the command key too, they are other keys.
      expect(
        dispatch(keydown(t.button, 's', 'KeyS', { altKey: true }), ctx, t),
      ).toBe(null);
      expect(
        dispatch(
          keydown(t.button, 'S', 'KeyS', {
            ...mod(platform),
            altKey: true,
            shiftKey: true,
          }),
          ctx,
          t,
        ),
      ).toBe(null);

      // Settings opens from the drafts page and from the app header too;
      // the layout and Auto-group are the editor's alone.
      const landing = context({ view: { editing: false } });
      expect(run(t.button, 'S', landing)).toEqual(['settings.open', true]);
      expect(run(t.outside, 'S', landing)).toEqual(['settings.open', true]);
      expect(run(t.outsideField, 'S', landing)).toEqual([null, false]);
      expect(run(t.button, 'L', landing)).toEqual([null, false]);
      expect(run(t.button, 'G', landing)).toEqual([null, false]);
      expect(run(t.outside, 'L')).toEqual([null, false]);
      expect(landing.view.openSettings).toHaveBeenCalledTimes(2);
      expect(landing.store.layout).not.toHaveBeenCalled();

      // From a checkbox that selects a card of the drafts page as well: it
      // is no text field. The Inspector's checkboxes keep the key.
      expect(run(t.checkbox, 'S', landing)).toEqual(['settings.open', true]);
      expect(landing.view.openSettings).toHaveBeenCalledTimes(3);
      expect(run(t.checkbox, 'S')).toEqual([null, false]);
      expect(run(t.checkbox, 'L')).toEqual([null, false]);

      // A read-only draft: the key is taken, and the reason said.
      const viewer = context({ store: { readOnly: true } });
      expect(run(t.button, 'L', viewer)).toEqual(['structure.layout', true]);
      expect(run(t.button, 'G', viewer)).toEqual([
        'structure.autoGroup.network',
        true,
      ]);
      expect(viewer.store.layout).not.toHaveBeenCalled();
      expect(viewer.store.autoGroup).not.toHaveBeenCalled();
      expect(viewer.store.announce).toHaveBeenCalledTimes(2);
      expect(viewer.store.announce).toHaveBeenLastCalledWith(READ_ONLY);
      expect(run(t.button, 'S', viewer)).toEqual(['settings.open', true]);
    },
  );

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
