import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { DUPLICATE_NEEDS_NODES } from '@/builder/clipboard.js';
import {
  COMMANDS,
  GROUPS,
  NODE_FIELD_RANKS,
  READ_ONLY,
  SCOPES,
  SELECT_ONE_NODE,
  ariaShortcuts,
  availability,
  canvasHints,
  commandKeys,
  commandTitle,
  defaultKeys,
  getCommand,
  isCustomizable,
  nodeChoices,
  paletteCommands,
  runCommand,
  saveDraft,
  shortcutConflicts,
  shortcutLabel,
  takesLetters,
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

import {
  addNode,
  placeholderLayers,
  setLayers,
  setNodeLayer,
} from '@/builder/model.js';
import { nextPosition } from '@/components/builder/paletteDnd.js';

import { PLATFORMS, context } from './command-context.js';
import { memoryStorage, sampleDocument } from './fixtures.js';

beforeEach(() => {
  loadShortcutSettings(memoryStorage());
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(memoryStorage());
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
    // The word Download had before.
    for (const command of COMMANDS) {
      if (command.id.startsWith('draft.download')) {
        expect(command.aliases, command.id).toEqual(['export']);
      }
    }
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
      drawio: 'Download draw.io',
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
    // Not F11, which browsers keep. It works in text fields too.
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
    // Shift with a letter, never in a text field, where Option types a letter.
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
        'drafts.tab.drafts',
        'help.open',
        'shortcuts.open',
        'settings.open',
        'settings.layers',
        'view.theme.dark',
        'view.focusMode',
      ]),
    );
    expect(editor).toContain('settings.open');
    expect(editor).toContain('settings.layers');
    // Download…, then a command for each format, in the dialog's order.
    expect(editor.filter((id) => id.startsWith('draft.download'))).toEqual([
      'draft.download',
      'draft.download.json',
      'draft.download.yaml',
      'draft.download.topology',
      'draft.download.png',
      'draft.download.svg',
      'draft.download.drawio',
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
      'structure.removeNetwork',
      'structure.setLayer',
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

    // A role that may not change configs cannot publish, but makes drafts
    // as any Builder user does.
    const viewer = { canPublish: false };
    const editor = context({ store: viewer });
    const landing = context({ store: viewer, view: { editing: false } });

    expect(availability('draft.upload', editor)).toBe(true);
    expect(availability('draft.publish', editor)).toBe(
      'Your role cannot publish diagrams.',
    );
    expect(availability('draft.download', editor)).toBe(true);
    for (const id of ['drafts.blank', 'drafts.import', 'drafts.upload']) {
      expect(availability(id, landing), id).toBe(true);
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
      'Return to the Drafts page first.',
    );
    expect(
      availability('edit.undo', context({ view: { editing: false } })),
    ).toBe('Open a draft first.');
    expect(
      availability('view.zoomIn', context({ view: { canZoomIn: false } })),
    ).toMatch(/largest zoom/);

    // The landing's tab commands, for the tab already shown.
    const landing = context({ view: { editing: false, draftsTab: 'drafts' } });
    expect(availability('drafts.tab.drafts', landing)).toBe(
      'This tab is already shown.',
    );
    expect(getCommand('drafts.tab.drafts').title).toBe('Show Drafts');
    expect(availability('drafts.tab.published', landing)).toBe(true);
    // The Layers tab lists the server's layer sets. On the drafts page,
    // Edit layers shows it.
    expect(availability('drafts.tab.layers', landing)).toBe(true);
    expect(getCommand('drafts.tab.layers').title).toBe('Show Layers');
    expect(runCommand('drafts.tab.layers', landing)).toBe(true);
    expect(landing.view.showDraftsTab).toHaveBeenLastCalledWith('layers');
    expect(runCommand('settings.layers', landing)).toBe(true);
    expect(landing.view.showDraftsTab).toHaveBeenLastCalledWith('layers');
    expect(landing.view.openLayers).not.toHaveBeenCalled();
    expect(getCommand('settings.layers').detail(landing)).toBe(
      'The layer sets',
    );
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

    // It leaves the open draft as it is, so a read-only one can be combined.
    const viewOnly = context({
      store: { summary: { included: 3 }, readOnly: true },
    });

    expect(availability('draft.combineIncluded', viewOnly)).toBe(true);
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
    // It reads nothing and changes nothing, so a read-only draft has it.
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

  test('Duplicate draft copies the open draft at once, or asks for a listed draft', () => {
    const editor = context();

    expect(getCommand('draft.duplicate').title).toBe('Duplicate draft…');
    expect(availability('draft.duplicate', editor)).toBe(true);
    expect(runCommand('draft.duplicate', editor)).toBe(true);
    expect(editor.view.duplicateDraft).toHaveBeenCalledWith(null);
    expect(editor.view.openPalette).not.toHaveBeenCalled();

    // A published diagram shown read only has no draft to copy.
    const published = context({ store: { published: { id: 'p1' } } });

    expect(availability('draft.duplicate', published)).toBe(
      'A published diagram has no draft to duplicate. Select Edit first.',
    );

    // On the drafts page it asks which draft, and offers no published
    // diagram.
    const draft = { id: 'a', createdBy: 'me', title: 'Mine' };
    const landing = context({
      view: { editing: false },
      store: {
        drafts: { all: [draft] },
        documents: [{ id: 'p1', kind: 'Topology', target: 'core' }],
      },
    });
    const command = getCommand('draft.duplicate');

    expect(runCommand(command, landing)).toBe(true);
    expect(landing.view.openPalette).toHaveBeenCalledWith({
      command: 'draft.duplicate',
    });
    expect(command.choices(landing).map((choice) => choice.id)).toEqual([
      'Drafts:a',
    ]);
    command.run(landing, command.choices(landing)[0]);
    expect(landing.view.duplicateDraft).toHaveBeenCalledWith(draft);

    expect(
      availability(
        'draft.duplicate',
        context({ view: { editing: false }, store: { drafts: { all: [] } } }),
      ),
    ).toBe('There are no drafts to duplicate.');
    expect(getCommand('draft.duplicate').keys).toBeUndefined();
  });

  test('every listed draft can be opened, and says who created it', () => {
    const landing = context({
      view: { editing: false },
      store: {
        drafts: {
          all: [
            { id: 'a', createdBy: 'me', title: 'Mine' },
            { id: 'b', createdBy: 'bob', title: 'Theirs' },
          ],
        },
      },
    });
    const [command] = paletteCommands(landing).filter(
      (entry) => entry.id === 'drafts.open',
    );
    const choices = command.choices(landing);

    expect(choices.map((choice) => [choice.id, choice.detail])).toEqual([
      ['Drafts:a', 'Drafts · Created by me'],
      ['Drafts:b', 'Drafts · Created by bob'],
    ]);
    expect(choices[1].keywords).toEqual(['bob']);
  });

  // A topology read from its Builder file is listed with the published
  // diagrams: named by its topology, found by its path, and opened as they are.
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
        drafts: { all: [] },
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
        id: 'Published Diagrams:p1',
        title: 'core',
        detail: 'Published Diagrams',
      },
      {
        id: 'Published Diagrams:file/plant',
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
    expect(commandTitle('view.fit', context())).toBe('Fit diagram to view');
    expect(
      commandTitle('view.fit', context({ view: { fitRestores: true } })),
    ).toBe('Restore previous view');
    expect(commandTitle('view.focusMode', context())).toBe('Focus mode');
    expect(
      commandTitle('view.focusMode', context({ view: { focusMode: true } })),
    ).toBe('Exit focus mode');
    expect(commandTitle('view.pane.start', context())).toBe(
      'Hide Nodes & Templates',
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
    // Edit layers opens the layers editor of the diagram, in a read-only
    // draft too, which shows its layers.
    expect(runCommand('settings.layers', viewer)).toBe(true);
    expect(viewer.view.openLayers).toHaveBeenCalledOnce();
    expect(viewer.view.openSettings).toHaveBeenCalledOnce();
    expect(commandKeys('settings.layers')).toEqual([]);
    // So is Focus mode, whose keys turn it off again.
    expect(runCommand('view.focusMode', viewer)).toBe(true);
    expect(viewer.view.toggleFocusMode).toHaveBeenCalledOnce();
    // And the side columns' Hide toggles, with no keys until the user gives
    // them some. The stacked layout shows every column, so they wait.
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
      'Layout: Network cards',
      'Layout: ELK layered',
      'Layout: By layer',
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
    // device's end. There is nothing to disconnect without one.
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

    // Remove network removes a network with its switches. It is the one way
    // to remove a network that no switch shows.
    const removeNetwork = getCommand('structure.removeNetwork');
    const [network] = removeNetwork.choices(ctx);

    expect(network).toMatchObject({
      title: 'EXP',
      detail: 'VLAN 100 · 1 switch',
      disabled: '',
      value: doc.networks[0].id,
    });
    ctx.store.removeNetwork = vi.fn();
    runCommand(removeNetwork, ctx, network, [network]);
    expect(ctx.store.removeNetwork).toHaveBeenCalledWith(doc.networks[0].id);
    expect(
      removeNetwork.choices(
        context({ store: { doc: { ...doc, nodes: [], edges: [] } } }),
      )[0],
    ).toMatchObject({
      title: 'EXP',
      detail: 'VLAN 100 · 0 switches',
      disabled: '',
    });
    expect(
      availability(
        removeNetwork,
        context({ store: { doc: { ...doc, networks: [] } } }),
      ),
    ).toBe('The diagram has no networks.');

    const router = getCommand('add.device')
      .choices(ctx)
      .find((choice) => choice.id === 'builtin:router');
    // The store refuses the add (null), so nothing is shown.
    ctx.store.addNode = vi.fn(() => null);
    runCommand('add.device', ctx, router);
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'router',
        position: nextPosition(doc, { kind: 'device' }),
      }),
    );
    expect(ctx.view.revealNode).not.toHaveBeenCalled();

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

    // On the grid while the view says Snap to grid is on.
    const offGrid = { x: 2001, y: 1003, width: 700, height: 500 };
    ctx.view.visibleArea = () => offGrid;
    for (const snap of [false, true]) {
      ctx.view.snapToGrid = snap;
      ctx.store.addNode = vi.fn(() => ({ id: 'added' }));
      runCommand('add.device', ctx, router);
      expect(ctx.store.addNode.mock.calls[0][0].position, String(snap)).toEqual(
        nextPosition(doc, { kind: 'device', area: offGrid, snap }),
      );
    }
    expect(
      nextPosition(doc, { kind: 'device', area: offGrid, snap: true }),
    ).not.toEqual(nextPosition(doc, { kind: 'device', area: offGrid }));
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
    // At a readable zoom: Go to zooms in when the canvas is too far out.
    expect(ctx.view.showNode).toHaveBeenCalledWith(alpha.id, { zoomIn: true });

    runCommand('goto.node', { ...ctx, additive: true }, { value: bravo.id });
    expect(ctx.store.selection.nodes).toEqual([alpha.id, bravo.id]);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Added bravo to the selection, 2 items selected',
    );

    // The palette stays open and says it on its own status line. The live
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

  test('nodes are found by addresses, VLANs, annotations, labels, description and image, in that order', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    node.label = 'Web front end';
    node.device.spec.network.interfaces = [
      {
        name: 'eth0',
        vlan: 'EXP',
        address: '10.1.2.3',
        mac: '00:11:22:33:44:55',
      },
    ];
    node.device.spec.annotations = { owner: 'ops', ports: [80, 443] };
    node.device.spec.labels = { role: 'plc' };
    node.device.spec.general = {
      ...node.device.spec.general,
      description: 'The web server',
    };

    const choice = nodeChoices(doc).find((entry) => entry.id === alpha.id);

    expect(choice.title).toBe('Web front end');
    expect(choice.keywords).toEqual(
      expect.arrayContaining([
        'alpha',
        'ubuntu.qc2',
        'EXP',
        'VLAN 100',
        '100',
        '10.1.2.3',
        '00:11:22:33:44:55',
        'owner=ops',
        'role=plc',
      ]),
    );
    expect(choice.detail).toBe('Device · ubuntu.qc2 · EXP');
    // Named and ranked fields. EXP is eth0's VLAN, so it is not a field twice.
    expect(choice.fields).toEqual([
      { label: 'Hostname', value: 'alpha', rank: 0, prefix: 'hostname' },
      {
        label: 'IP address',
        value: '10.1.2.3',
        rank: 1,
        prefix: 'eth0 IP address',
      },
      {
        label: 'MAC address',
        value: '00:11:22:33:44:55',
        rank: 2,
        prefix: 'eth0 MAC address',
      },
      { label: 'VLAN', value: 'EXP', rank: 3, prefix: 'eth0 VLAN' },
      { label: 'VLAN', value: 'VLAN 100', rank: 3 },
      {
        label: 'Annotation',
        value: 'owner=ops',
        rank: 4,
        prefix: 'annotation',
      },
      {
        label: 'Annotation',
        value: 'ports=[80,443]',
        rank: 4,
        prefix: 'annotation',
      },
      { label: 'Label', value: 'role=plc', rank: 5, prefix: 'label' },
      {
        label: 'Description',
        value: 'The web server',
        rank: 6,
        prefix: 'description',
      },
      { label: 'Image', value: 'ubuntu.qc2', rank: 7 },
    ]);
    expect(Object.keys(NODE_FIELD_RANKS)).toEqual(
      expect.arrayContaining(choice.fields.map((field) => field.label)),
    );
  });

  test('a switch is found by its network and its description', () => {
    const { doc, sw, network } = sampleDocument();

    doc.networks = doc.networks.map((entry) =>
      entry.id === network.id
        ? { ...entry, description: 'Plant floor' }
        : entry,
    );

    const choice = nodeChoices(doc).find((entry) => entry.id === sw.id);

    expect(choice.fields).toEqual([
      { label: 'Network', value: 'EXP', rank: 3 },
      { label: 'VLAN', value: 'VLAN 100', rank: 3 },
      {
        label: 'Description',
        value: 'Plant floor',
        rank: 6,
        prefix: 'description',
      },
    ]);
  });

  // The server matches a VLAN to a network name in case as Go folds it,
  // where "σασ" names network "ΣΑΣ" (toLowerCase gives "σας").
  test('a device is on the network its VLAN names, in case as the server folds it', () => {
    const { doc, bravo, network } = sampleDocument();

    doc.networks = doc.networks.map((entry) =>
      entry.id === network.id ? { ...entry, name: 'ΣΑΣ' } : entry,
    );
    doc.nodes = doc.nodes.map((entry) =>
      entry.id === bravo.id
        ? {
            ...entry,
            device: {
              ...entry.device,
              spec: {
                ...entry.device.spec,
                network: { interfaces: [{ name: 'eth0', vlan: 'σασ' }] },
              },
            },
          }
        : entry,
    );

    const choice = nodeChoices(doc).find((entry) => entry.id === bravo.id);

    expect(choice.detail).toContain('ΣΑΣ');
    expect(choice.fields.map((field) => field.label)).not.toContain('Network');
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
    setShortcut('structure.layout', ['Mod+Shift+L'], memoryStorage());
    setShortcut('edit.undo', [], memoryStorage());
    // Local keys are the controls' own, and cannot change.
    setShortcut('edit.delete', ['Mod+Shift+D'], memoryStorage());

    expect(shortcutLabel('structure.layout', { platform: 'mac' })).toBe('⇧⌘L');
    expect(shortcutLabel('edit.undo', { platform: 'mac' })).toBe('');
    expect(commandKeys('edit.delete', { platform: 'other' })).toEqual([
      'Delete',
      'Backspace',
    ]);

    setSingleKeyShortcuts(false, memoryStorage());
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
    expect(canvasHints({ readOnly: false, platform: 'mac' }).node).toBe(
      'Arrow keys move between nodes. Page Down moves through this node’s ' +
        'connections. Return selects or deselects. ⇧Return adds to the ' +
        'selection. Escape clears the selection. Shift and an arrow key ' +
        'move the selected nodes. Delete removes.',
    );
  });

  // The canvas is one Tab stop: arrows move focus, Page Down and Page Up go
  // through connections, and Shift with an arrow moves the selected nodes.
  test('the canvas hints give the keys that move focus and nodes', () => {
    const hints = canvasHints({ readOnly: false, platform: 'other' });
    expect(hints.edge).toMatch(
      /^Arrow keys move to the nodes\. Page Down moves to the next connection\./,
    );
    expect(hints.canvas).toBe(
      'Arrow keys move to the nodes. Enter selects the focused one. ' +
        'With a screen reader, use its focus mode (forms mode in JAWS) ' +
        'for these keys. You can also find a node with Go to node in the ' +
        'command palette. ? lists every shortcut.',
    );
    expect(canvasHints({ readOnly: false, platform: 'mac' }).canvas).toBe(
      'Arrow keys move to the nodes. Return selects the focused one. ' +
        'With a screen reader, use its focus mode (forms mode in JAWS) ' +
        'for these keys. You can also find a node with Go to node in the ' +
        'command palette. ? lists every shortcut.',
    );
    expect(canvasHints({ readOnly: true, platform: 'other' }).canvas).toBe(
      `This draft is read-only. ${hints.canvas}`,
    );
    expect(canvasHints({ readOnly: true, platform: 'other' }).node).not.toMatch(
      /arrow key move|removes/,
    );
  });

  test('the canvas hint follows the key of the shortcut sheet', () => {
    setShortcut('shortcuts.open', ['Mod+/'], memoryStorage());
    expect(canvasHints({ readOnly: false, platform: 'mac' }).canvas).toMatch(
      /command palette\. ⌘\/ lists every shortcut\.$/,
    );

    // Without a key, it names the button that opens the sheet.
    loadShortcutSettings(memoryStorage());
    setSingleKeyShortcuts(false, memoryStorage());

    for (const readOnly of [false, true]) {
      const { canvas } = canvasHints({ readOnly, platform: 'other' });

      expect(canvas).toMatch(
        /command palette\. Shortcuts in the header lists every shortcut\.$/,
      );
      expect(canvas).not.toContain('?');
    }
  });
});

// The Settings dialog's Snap to grid, from the palette.
describe('the Snap to grid command', () => {
  test('says the state, asks the view to turn it on or off, and is in the View group', () => {
    const command = getCommand('view.snapToGrid');
    const ctx = context();

    expect(command.title).toBe('Snap to grid');
    expect(command.group).toBe('View');
    expect(commandTitle(command, ctx)).toBe('Turn on snap to grid');
    expect(commandTitle(command, context({ view: { snapToGrid: true } }))).toBe(
      'Turn off snap to grid',
    );
    expect(
      paletteCommands(ctx).some((entry) => entry.id === 'view.snapToGrid'),
    ).toBe(true);

    runCommand('view.snapToGrid', ctx);
    expect(ctx.view.toggleSnapToGrid).toHaveBeenCalledOnce();
  });
});

describe('layers', () => {
  // sampleDocument with three layers: Control matches alpha by hostname, bravo
  // is at Field, the switch at the undefined 7, and no node is at Plant.
  function layered() {
    const sample = sampleDocument();
    let doc = setLayers(sample.doc, [
      { id: 5, label: 'Plant' },
      {
        id: 3,
        label: 'Control',
        iconKey: 'plc',
        color: '#15803d',
        matchers: { 'general.hostname': '^alpha' },
      },
      { id: 1, label: 'Field' },
    ]);

    doc = setNodeLayer(doc, sample.bravo.id, 1);
    doc = setNodeLayer(doc, sample.sw.id, 7);

    return { ...sample, doc };
  }

  test('Set layer and Select on layer have no default keys, and can be given some', () => {
    for (const id of ['structure.setLayer', 'selection.layer']) {
      for (const platform of PLATFORMS) {
        expect(defaultKeys(id, platform), id).toEqual([]);
      }
      expect(isCustomizable(id), id).toBe(true);
      expect(paletteCommands(context()), id).toContain(getCommand(id));
    }
  });

  test('Set layer of selection lists the diagram’s layers and No layer', () => {
    const { doc, alpha, sw } = layered();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id, sw.id], edges: [] } },
    });
    const choices = getCommand('structure.setLayer').choices(ctx, []);

    expect(availability('structure.setLayer', ctx)).toBe(true);
    expect(getCommand('structure.setLayer').detail(ctx)).toBe(
      '1 device and 1 switch selected',
    );
    expect(
      choices.map(({ id, title, detail, value }) => ({
        id,
        title,
        detail,
        value,
      })),
    ).toEqual([
      { id: '5', title: 'Plant', detail: '0 nodes', value: 5 },
      { id: '3', title: 'Control', detail: '1 node', value: 3 },
      { id: '1', title: 'Field', detail: '1 node', value: 1 },
      {
        id: 'none',
        title: 'No layer',
        detail: 'A device then goes to the layer its fields match, if any',
        value: null,
      },
    ]);
    // Its layer, for the palette to show the icon and color.
    expect(choices[1].layer).toEqual(
      expect.objectContaining({ iconKey: 'plc', color: '#15803d' }),
    );
    expect(choices.every((choice) => !choice.disabled)).toBe(true);

    // It runs once for the whole selection, through the store.
    runCommand('structure.setLayer', ctx, choices[1]);
    expect(ctx.store.setNodesLayer).toHaveBeenCalledExactlyOnceWith(
      [alpha.id, sw.id],
      3,
    );

    // Without a choice, the palette asks for one.
    runCommand('structure.setLayer', ctx);
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({
      command: 'structure.setLayer',
    });
  });

  test('Set layer of selection reads the document once for a large selection', () => {
    const { doc } = layered();
    const model = doc.nodes.find((node) => node.kind === 'device');
    const size = 500;
    let reads = 0;
    const nodes = new Proxy(
      Array.from({ length: size }, (_, i) => ({ ...model, id: `n-${i}` })),
      {
        get(target, key, receiver) {
          reads += /^\d+$/.test(String(key)) ? 1 : 0;

          return Reflect.get(target, key, receiver);
        },
      },
    );
    const ctx = context({
      store: {
        doc: { ...doc, nodes },
        selection: { nodes: nodes.map((node) => node.id), edges: [] },
      },
    });

    reads = 0;
    expect(availability('structure.setLayer', ctx)).toBe(true);
    expect(getCommand('structure.setLayer').detail(ctx)).toBe(
      '500 devices selected',
    );
    // One pass each, not one pass for each selected node.
    expect(reads).toBe(2 * size);
  });

  test('Set layer of selection says why it cannot run', () => {
    const { doc, alpha, edge } = layered();
    const state = (store) =>
      availability('structure.setLayer', context({ store }));
    const note = addNode(doc, { kind: 'note', text: 'Read me' });
    const included = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
          : node,
      ),
    };

    expect(
      state({
        doc,
        readOnly: true,
        selection: { nodes: [alpha.id], edges: [] },
      }),
    ).toBe(READ_ONLY);
    expect(state({ doc, selection: { nodes: [], edges: [edge.id] } })).toBe(
      'Select a device or a switch.',
    );
    expect(
      state({
        doc: note.doc,
        selection: { nodes: [note.node.id], edges: [] },
      }),
    ).toBe('Select a device or a switch.');
    expect(
      state({ doc: included, selection: { nodes: [alpha.id], edges: [] } }),
    ).toMatch(/^Included topology shared defines alpha/);
  });

  test('Set layer of selection offers the placeholder layers of a diagram without its own', () => {
    const { doc, alpha } = sampleDocument();

    for (const layers of [[], placeholderLayers()]) {
      const ctx = context({
        store: {
          doc: { ...doc, layers },
          selection: { nodes: [alpha.id], edges: [] },
        },
      });
      const choices = getCommand('structure.setLayer').choices(ctx, []);

      expect(availability('structure.setLayer', ctx)).toBe(true);
      expect(choices.map((choice) => choice.title)).toEqual([
        'Layer 6',
        'Layer 5',
        'Layer 4',
        'Layer 3',
        'Layer 2',
        'Layer 1',
        'Layer 0',
        'No layer',
      ]);
    }
  });

  test('Set layer of selection adds the ID to a label that two layers have', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: {
        doc: {
          ...doc,
          layers: [
            { id: 4, label: 'Control' },
            { id: 2, label: 'Control' },
            { id: 1, label: 'Field' },
          ],
        },
        selection: { nodes: [alpha.id], edges: [] },
      },
    });
    const choices = getCommand('structure.setLayer').choices(ctx, []);

    expect(choices.map((choice) => choice.title)).toEqual([
      'Control (ID 4)',
      'Control (ID 2)',
      'Field',
      'No layer',
    ]);
  });

  test('Select nodes on layer is available at the first node on a layer, without reading the rest', () => {
    const { doc, alpha } = sampleDocument();
    const first = setNodeLayer(doc, alpha.id, 2);
    const unread = new Proxy(
      {},
      {
        get() {
          throw new Error('read a node after the first at a layer');
        },
      },
    );
    const ctx = context({
      store: {
        doc: {
          ...first,
          nodes: [first.nodes.find((node) => node.id === alpha.id), unread],
        },
      },
    });

    expect(availability('selection.layer', ctx)).toBe(true);
  });

  test('Select nodes on layer selects the nodes at a layer, matched or chosen', () => {
    const { doc, alpha, sw } = layered();
    const ctx = context({ store: { doc } });
    const command = getCommand('selection.layer');
    const choices = command.choices(ctx, []);

    expect(availability(command, ctx)).toBe(true);
    // The diagram's layers, and 7, which only the switch names.
    expect(
      choices.map(({ title, detail, disabled }) => [title, detail, disabled]),
    ).toEqual([
      ['Layer 7', '1 node', ''],
      ['Plant', '0 nodes', 'No node is on this layer.'],
      ['Control', '1 node', ''],
      ['Field', '1 node', ''],
    ]);

    // alpha is at Control by its hostname.
    runCommand(command, ctx, choices[2]);
    expect(ctx.store.selection).toEqual({ nodes: [alpha.id], edges: [] });
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Selected 1 node on layer Control',
    );
    expect(ctx.view.revealNode).toHaveBeenLastCalledWith([alpha.id]);

    runCommand(command, ctx, choices[0]);
    expect(ctx.store.selection.nodes).toEqual([sw.id]);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Selected 1 node on Layer 7',
    );

    // A read-only draft can select too.
    expect(
      availability(command, context({ store: { doc, readOnly: true } })),
    ).toBe(true);

    // No node on a layer: nothing to choose.
    expect(availability(command, context())).toBe('No node is on a layer.');
  });

  test('Select nodes on layer counts every node of a layer', () => {
    const { doc, alpha, bravo } = layered();
    const both = setNodeLayer(doc, bravo.id, 3);
    const ctx = context({ store: { doc: both } });
    const command = getCommand('selection.layer');
    const control = command
      .choices(ctx, [])
      .find((choice) => choice.value === 3);

    expect(control.detail).toBe('2 nodes');
    runCommand(command, ctx, control);
    expect(ctx.store.selection.nodes).toEqual([alpha.id, bravo.id]);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Selected 2 nodes on layer Control',
    );
  });
});

describe('Select devices on network and using image', () => {
  // alpha is connected to EXP. bravo is on EXP by the VLAN of its own
  // interface. charlie is on no network. Each has the drives given.
  function imaged() {
    const sample = sampleDocument();
    const withDrives = (node, images, vlan) => ({
      ...node,
      device: {
        ...node.device,
        spec: {
          ...node.device.spec,
          hardware: {
            ...node.device.spec?.hardware,
            drives: images.map((image) => ({ image })),
          },
          network: vlan
            ? { interfaces: [{ name: 'eth0', type: 'ethernet', vlan }] }
            : node.device.spec?.network,
        },
      },
    });
    const added = addNode(sample.doc, {
      kind: 'device',
      hostname: 'charlie',
      position: { x: 400, y: 400 },
    });
    const charlie = added.node;
    const images = {
      [sample.alpha.id]: ['ubuntu.qc2', 'data.qc2'],
      [sample.bravo.id]: ['ubuntu.qc2', 'ubuntu.qc2'],
      [charlie.id]: ['  ', 'kali.qc2'],
    };
    const doc = {
      ...added.doc,
      networks: [...added.doc.networks, { id: 'net-empty', name: 'OT' }],
      nodes: added.doc.nodes.map((node) =>
        images[node.id]
          ? withDrives(
              node,
              images[node.id],
              node.id === sample.bravo.id ? 'EXP' : '',
            )
          : node,
      ),
    };

    return { ...sample, doc, charlie };
  }

  test('Select devices on network selects the devices with an interface on it', () => {
    const { doc, alpha, bravo, network } = imaged();
    const ctx = context({ store: { doc } });
    const command = getCommand('selection.network');
    const choices = command.choices(ctx, []);

    expect(command).toMatchObject({ group: 'Selection', steps: ['Network'] });
    expect(defaultKeys('selection.network', 'mac')).toEqual([]);
    expect(isCustomizable('selection.network')).toBe(true);
    expect(availability(command, ctx)).toBe(true);
    expect(
      choices.map(({ title, detail, disabled }) => [title, detail, disabled]),
    ).toEqual([
      ['EXP', '2 devices · VLAN 100', ''],
      ['OT', '0 devices · no VLAN alias', 'No device is on this network.'],
    ]);

    runCommand(command, ctx, choices[0]);
    expect(ctx.store.selection).toEqual({
      nodes: [alpha.id, bravo.id],
      edges: [],
    });
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Selected 2 devices on network EXP',
    );
    expect(ctx.view.revealNode).toHaveBeenLastCalledWith([alpha.id, bravo.id]);
    expect(choices[0].value).toEqual([alpha.id, bravo.id]);
    expect(network.id).toBe(choices[0].id);

    // A read-only draft can select too. A diagram with no network cannot.
    expect(
      availability(command, context({ store: { doc, readOnly: true } })),
    ).toBe(true);
    expect(
      availability(
        command,
        context({ store: { doc: { ...doc, networks: [] } } }),
      ),
    ).toBe('The diagram has no networks.');
  });

  test('Select devices using image lists each image once, and selects its devices', () => {
    const { doc, alpha, bravo, charlie } = imaged();
    const ctx = context({ store: { doc } });
    const command = getCommand('selection.image');
    const choices = command.choices(ctx, []);

    expect(command).toMatchObject({ group: 'Selection', steps: ['Image'] });
    expect(defaultKeys('selection.image', 'other')).toEqual([]);
    expect(isCustomizable('selection.image')).toBe(true);
    // Alphabetical, blank images left out, a device counted once.
    expect(choices.map(({ title, detail }) => [title, detail])).toEqual([
      ['data.qc2', '1 device'],
      ['kali.qc2', '1 device'],
      ['ubuntu.qc2', '2 devices'],
    ]);

    runCommand(command, ctx, choices[2]);
    expect(ctx.store.selection.nodes).toEqual([alpha.id, bravo.id]);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(
      'Selected 2 devices using image ubuntu.qc2',
    );
    expect(ctx.view.revealNode).toHaveBeenLastCalledWith([alpha.id, bravo.id]);

    runCommand(command, ctx, choices[1]);
    expect(ctx.store.selection.nodes).toEqual([charlie.id]);

    // No drive names an image: nothing to choose.
    const bare = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.kind === 'device'
          ? {
              ...node,
              device: {
                ...node.device,
                spec: { ...node.device.spec, hardware: { drives: [] } },
              },
            }
          : node,
      ),
    };

    expect(availability(command, context({ store: { doc: bare } }))).toBe(
      'No device has a disk image.',
    );
  });
});

describe('Go to network', () => {
  test('selects the switches of a network and goes to the first one', () => {
    const { doc, sw, network } = sampleDocument();
    const ctx = context({ store: { doc } });

    runCommand('goto.network', ctx, { value: network.id, title: 'EXP' });
    expect(ctx.store.selection).toEqual({ nodes: [sw.id], edges: [] });
    expect(ctx.view.showNode).toHaveBeenCalledExactlyOnceWith(sw.id, {
      zoomIn: true,
    });
  });
});

describe('Select all', () => {
  test('selects the connections too only while the canvas shows them', () => {
    const shown = context();

    runCommand('selection.all', shown);
    expect(shown.store.selectAll).toHaveBeenCalledExactlyOnceWith({
      edges: true,
    });

    const hidden = context({ view: { showConnections: false } });

    runCommand('selection.all', hidden);
    expect(hidden.store.selectAll).toHaveBeenCalledExactlyOnceWith({
      edges: false,
    });
  });
});

describe('Add switch', () => {
  test('has no default keys, and runs from the palette to add a switch in view', () => {
    const ctx = context();

    // No default keys, and users can give it some.
    for (const platform of PLATFORMS) {
      expect(defaultKeys('add.switch', platform)).toEqual([]);
    }
    expect(isCustomizable('add.switch')).toBe(true);
    expect(paletteCommands(ctx)).toContain(getCommand('add.switch'));

    ctx.store.addNode = vi.fn(() => ({ id: 'added' }));
    expect(getCommand('add.switch').title).toBe('Add switch');
    expect(availability('add.switch', ctx)).toBe(true);
    runCommand('add.switch', { ...ctx, source: 'palette' });
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.addNode).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ kind: 'switch' }),
    );
  });
});

describe('Open Node Editor', () => {
  test('is in Edit, with keys free of every other command, the browser and the OS', () => {
    const command = getCommand('node.edit');

    expect(command).toMatchObject({
      title: 'Open Node Editor',
      group: 'Edit',
    });
    expect(isCustomizable(command)).toBe(true);
    for (const platform of PLATFORMS) {
      expect(defaultKeys(command, platform)).toEqual(['Alt+Shift+E']);
      expect(keyRefusal('Alt+Shift+E', platform)).toBe('');
      expect(shortcutConflicts(command, 'Alt+Shift+E', { platform })).toEqual(
        [],
      );
    }
    expect(withShortcut('Open Node Editor', command, { platform: 'mac' })).toBe(
      'Open Node Editor (⌥⇧E)',
    );
    expect(ariaShortcuts(command, { platform: 'other' })).toBe('Alt+Shift+E');
  });

  test('opens on one node selected, else asks for a device or switch', () => {
    const sample = sampleDocument();
    const { alpha, bravo, sw, edge } = sample;
    // A group, a note, a shape, an icon and a line, which the Inspector
    // edits too.
    const drawn = [];
    let doc = sample.doc;

    for (const kind of ['group', 'note', 'shape', 'icon', 'line']) {
      const added = addNode(doc, { kind });

      doc = added.doc;
      drawn.push(added.node);
    }

    const selected = (selection) =>
      availability(
        'node.edit',
        context({
          store: { doc, selection: { nodes: [], edges: [], ...selection } },
        }),
      );

    // Without one node selected, it asks for a node: the diagram has
    // devices and switches to choose.
    expect(selected({})).toBe(true);
    expect(selected({ nodes: [alpha.id, bravo.id] })).toBe(true);
    expect(selected({ edges: [edge.id] })).toBe(true);
    expect(selected({ nodes: [alpha.id] })).toBe(true);
    // A switch, and every other kind the Inspector edits.
    for (const node of [sw, ...drawn]) {
      expect(selected({ nodes: [node.id] }), node.kind).toBe(true);
    }
    // A read-only draft opens it read only.
    expect(
      availability(
        'node.edit',
        context({
          store: {
            doc,
            readOnly: true,
            selection: { nodes: [alpha.id], edges: [] },
          },
        }),
      ),
    ).toBe(true);
  });

  test('opens the Node Editor on the selected node, named in the palette', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id], edges: [] } },
    });

    expect(getCommand('node.edit').detail(ctx)).toBe('alpha');
    expect(runCommand('node.edit', { ...ctx, source: 'palette' })).toBe(true);
    expect(ctx.view.openDialog).toHaveBeenCalledExactlyOnceWith('node-editor', {
      id: alpha.id,
    });
    // The selection stays as it is.
    expect(ctx.store.select).not.toHaveBeenCalled();
  });

  test('says why it cannot run', () => {
    // Only a note: nothing is selected, and no device or switch is there to
    // choose.
    const { doc: note } = addNode(
      { ...sampleDocument().doc, nodes: [], edges: [], networks: [] },
      { kind: 'note' },
    );
    const ctx = context({ store: { doc: note } });

    expect(SELECT_ONE_NODE).toBe('Select one node.');
    expect(runCommand('node.edit', ctx)).toBe(false);
    expect(ctx.store.announce).toHaveBeenCalledWith(SELECT_ONE_NODE);
    expect(ctx.view.openDialog).not.toHaveBeenCalled();
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
  });

  test('without one node selected, asks for a device or switch and opens on it', () => {
    const sample = sampleDocument();
    const { alpha, bravo, sw } = sample;
    const { doc } = addNode(sample.doc, { kind: 'note' });
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id, bravo.id], edges: [] } },
    });
    const command = getCommand('node.edit');

    expect(command.direct(ctx)).toBeUndefined();
    expect(command.detail(ctx)).toBe('Choose a node');
    // Only devices and switches, found as Go to node finds them.
    const choices = command.choices(ctx, []);

    expect(choices.map((choice) => choice.value).sort()).toEqual(
      [alpha.id, bravo.id, sw.id].sort(),
    );
    expect(choices).toEqual(
      nodeChoices(
        doc,
        choices.map((choice) => choice.value),
      ),
    );

    // Its keys and the palette ask for the node first.
    expect(runCommand('node.edit', { ...ctx, source: 'key' })).toBe(true);
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({
      command: 'node.edit',
    });
    expect(ctx.view.openDialog).not.toHaveBeenCalled();

    // The chosen node is selected, and the Node Editor opens on it.
    const choice = choices.find((entry) => entry.value === sw.id);

    expect(
      runCommand('node.edit', { ...ctx, source: 'palette' }, choice, [choice]),
    ).toBe(true);
    expect(ctx.store.select).toHaveBeenCalledExactlyOnceWith({
      nodes: [sw.id],
      edges: [],
    });
    expect(ctx.view.openDialog).toHaveBeenCalledExactlyOnceWith('node-editor', {
      id: sw.id,
    });
  });

  test('runs at once on the selected node, from its keys and the palette', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id], edges: [] } },
    });
    const command = getCommand('node.edit');

    expect(command.direct(ctx)).toMatchObject({ value: alpha.id });
    expect(runCommand('node.edit', { ...ctx, source: 'key' })).toBe(true);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.view.openDialog).toHaveBeenCalledExactlyOnceWith('node-editor', {
      id: alpha.id,
    });
  });
});
