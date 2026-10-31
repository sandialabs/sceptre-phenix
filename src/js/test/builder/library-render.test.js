// The Node Templates tab, the collection dialog, the palette's library and the
// template editor on a library template, rendered on the server.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// The list the Node Templates tab shows first (see shownList): server
// rendering runs no watcher, so a test names it here.
const startList = vi.hoisted(() => ({ value: '' }));

vi.mock('@/builder/templates.js', async (importOriginal) => {
  const actual = await importOriginal();

  return {
    ...actual,
    shownList: (library, value, options) =>
      actual.shownList(library, value || startList.value, options),
  };
});

import BuilderPalette from '@/components/builder/BuilderPalette.vue';
import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
import CollectionDialog from '@/components/builder/dialogs/CollectionDialog.vue';
import TemplateDialog from '@/components/builder/dialogs/TemplateDialog.vue';
import InspectorTemplateLinks from '@/components/builder/inspector/InspectorTemplateLinks.vue';
import {
  VIEW_API,
  availability,
  createCommandContext,
  getCommand,
  runCommand,
} from '@/builder/commands.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import {
  addTemplate,
  createDocument,
  updateTemplate,
} from '@/builder/model.js';
import {
  SHOW_BUILTIN,
  blankTemplate,
  linkedCopy,
} from '@/builder/templates.js';

import {
  libraryOf,
  memoryStorage,
  sampleDocument,
  tags,
  textOf,
  withTemplates,
} from './fixtures.js';
import { ICON_DATA } from './png.js';
import { renderWithStore } from './render.js';

function template(id, init = {}) {
  return {
    id,
    name: id.toUpperCase(),
    device: {
      iconKey: 'router',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: `${id}.qc2` }] },
        network: { interfaces: [] },
      },
    },
    ...init,
  };
}

// The element with a test id, whole: its tag alone for one that holds
// nothing (an input), else all of it, with what it holds of its own kind.
function element(html, testid) {
  const found = html.match(
    new RegExp(`<([a-z0-9]+)\\b[^>]*data-testid="${testid}"[^>]*>`),
  );

  if (!found) {
    return '';
  }

  const [tag, name] = found;

  if (name === 'input') {
    return tag;
  }

  const marks = new RegExp(`<${name}\\b|</${name}>`, 'g');
  let depth = 0;

  marks.lastIndex = found.index;

  for (let mark = marks.exec(html); mark; mark = marks.exec(html)) {
    depth += mark[0].startsWith('</') ? -1 : 1;

    if (depth === 0) {
      return html.slice(found.index, marks.lastIndex);
    }
  }

  return html.slice(found.index);
}

// The test ids of the buttons in a piece of markup, in order.
function buttonIds(html) {
  return [...html.matchAll(/<button\b[^>]*data-testid="([^"]+)"/g)].map(
    ([, id]) => id,
  );
}

// A component rendered with a Builder store that `prepare` sets up first.
function render(component, props = {}, prepare = () => {}) {
  return renderWithStore(component, {
    props,
    prepare(store) {
      store.doc = createDocument({ name: 'Plant' });
      prepare(store);
    },
  });
}

beforeEach(() => {
  startList.value = '';
  // The server's icon library holds the icon the templates name.
  iconLibrary.state.index = indexIcons([{ name: 'plc-icon', data: ICON_DATA }]);
});

describe('the Node Templates tab', () => {
  const library = () =>
    libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          updated: '2026-10-01T12:00:00Z',
          device: { ...template('plc').device, icon: 'plc-icon' },
        }),
        template('hmi'),
        template('rtu'),
      ],
      collections: [
        { id: 'c1', name: 'Plant floor', templateIds: ['rtu', 'plc'] },
        { id: 'c2', name: 'Office', templateIds: ['plc'] },
      ],
    });
  const tab = (prepare) =>
    render(BuilderTemplates, {}, (store) => {
      store.templates = library();
      prepare?.(store);
    });

  test('lists the library’s templates, a card each, under what makes more', async () => {
    const { html } = await tab();

    expect(buttonIds(html).slice(0, 3)).toEqual([
      'templates-new',
      'collections-new',
      'templates-import',
    ]);
    expect(textOf(element(html, 'templates-new'))).toBe('New template');
    expect(element(html, 'templates-new')).toContain('aria-haspopup="dialog"');
    expect(textOf(element(html, 'collections-new'))).toBe('New collection');
    // Each button of the row has an icon before its label.
    expect(element(html, 'templates-new')).toContain('builder-icon--plus');
    expect(element(html, 'collections-new')).toContain('builder-icon--plus');
    expect(element(html, 'templates-import')).toContain('builder-icon--upload');

    // Show: every template, those of one collection, or the built-in
    // templates.
    expect(html).toContain('<label for="templates-show"');
    expect(
      [...html.matchAll(/<option[^>]*>([\s\S]*?)<\/option>/g)].map(
        ([, inner]) => textOf(inner),
      ),
    ).toEqual([
      'All library templates',
      'Plant floor',
      'Office',
      'Built-in templates',
    ]);
    expect(html).toContain('<optgroup label="Library collections"');
    expect(html).toContain(`<option value="${SHOW_BUILTIN}"`);
    expect(html).not.toContain('collection-block');
    // A template of the library is not read only.
    expect(html).not.toContain('template-read-only');

    const [list] = html.match(
      /<ul[^>]*data-testid="templates-list"[\s\S]*?<\/ul>/,
    );

    expect(list).toContain('class="builder-cards"');
    expect(
      [...list.matchAll(/data-testid="template-card-([a-z]+)"/g)].map(
        ([, id]) => id,
      ),
    ).toEqual(['plc', 'hmi', 'rtu']);
  });

  test('a card names its template, says what is known of it, and has Edit and Delete', async () => {
    const { html } = await tab();
    const [card, next] = html.split('<li ').slice(1);

    expect(card).toContain('data-testid="template-card-plc"');
    expect(card).toContain('builder-card builder-panel');
    expect(card).not.toContain('is-selected');
    expect(textOf(card.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe('PLC');
    // The icon devices made from it are drawn with: its custom one.
    expect(card).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(textOf(element(card, 'template-time-plc'))).toMatch(
      /^Updated .*2026/,
    );
    expect(textOf(element(card, 'template-collections-plc'))).toBe(
      'In Plant floor and Office',
    );
    expect(textOf(element(card, 'template-about-plc'))).toBe('A controller');

    const select = element(card, 'template-select-plc');

    expect(select).toContain('type="checkbox"');
    expect(card).toContain('class="builder-card__select"');
    // At the card's top right: after the name, in the DOM as on the
    // screen.
    expect(card.indexOf('class="builder-card__select"')).toBeGreaterThan(
      card.indexOf('</h2>'),
    );
    expect(card).toMatch(
      /<span class="builder-visually-hidden"[^>]*>\s*Select PLC\s*<\/span>/,
    );
    expect(buttonIds(card)).toEqual([
      'template-edit-plc',
      'template-delete-plc',
      'template-export-plc',
    ]);
    expect(element(card, 'template-edit-plc')).toContain(
      'aria-label="Edit template PLC"',
    );
    expect(element(card, 'template-export-plc')).toContain(
      'aria-label="Download template PLC"',
    );
    expect(element(card, 'template-delete-plc')).toContain(
      'aria-label="Delete template PLC"',
    );
    // The item actions: Edit blue with a pencil, Delete red with a trash
    // can, and Download with an arrow down. No label wraps.
    expect(element(card, 'template-edit-plc')).toContain(
      'builder-button--primary',
    );
    expect(element(card, 'template-edit-plc')).toContain(
      'builder-icon--pencil',
    );
    expect(element(card, 'template-delete-plc')).toContain(
      'builder-button--danger',
    );
    expect(element(card, 'template-delete-plc')).toContain(
      'builder-icon--trash',
    );
    expect(element(card, 'template-export-plc')).toContain(
      'builder-icon--download',
    );
    expect(card).toContain('class="builder-card__actions"');
    expect(card).not.toContain('builder-card__actions--fit');

    // A template never changed has no time, and one in no collection and
    // with no description says neither.
    expect(next).toContain('data-testid="template-card-hmi"');
    expect(next).toContain('builder-icon--router');
    expect(next).not.toContain('template-time-hmi');
    expect(next).not.toContain('template-collections-hmi');
    expect(next).not.toContain('template-about-hmi');
  });

  test('a card says who changed the template last, as the library is the same for everyone', async () => {
    const { html } = await tab((store) => {
      store.templates = libraryOf({
        templates: [
          template('plc', {
            updated: '2026-10-01T12:00:00Z',
            createdBy: 'alice',
            updatedBy: 'bob',
          }),
        ],
      });
    });

    expect(textOf(element(html, 'template-time-plc'))).toMatch(
      /^Updated .*2026.* by bob$/,
    );
  });

  test('templates that share a name are told apart by when they changed', async () => {
    const { html } = await tab((store) => {
      store.templates = libraryOf({
        templates: [
          template('a', { name: 'PLC', updated: '2026-10-01T12:00:00Z' }),
          template('b', { name: 'PLC', updated: '2026-10-02T12:00:00Z' }),
          template('c', { name: 'PLC' }),
        ],
      });
    });
    const labels = [
      ...html.matchAll(/aria-label="Edit template ([^"]+)"/g),
    ].map(([, name]) => name);

    expect(labels).toHaveLength(3);
    expect(labels[0]).toMatch(/^PLC, updated .*2026/);
    expect(labels[1]).toMatch(/^PLC, updated .*2026/);
    expect(labels[0]).not.toBe(labels[1]);
    // One never changed has no time to tell.
    expect(labels[2]).toBe('PLC');
  });

  test('the row above the cards: Select all, the count, and what acts on the selection', async () => {
    const { html } = await tab();
    const bar = element(html, 'bulk-bar-templates');

    expect(bar).toContain('role="group"');
    expect(bar).toContain('aria-label="Bulk actions: Library"');
    expect(textOf(element(html, 'bulk-count-templates'))).toBe(
      '0 of 3 selected',
    );
    expect(buttonIds(bar)).toEqual([
      'bulk-collect-templates',
      'bulk-export-templates',
      'bulk-delete-templates',
    ]);

    const collect = element(bar, 'bulk-collect-templates');

    expect(textOf(collect)).toBe('Add to collection');
    expect(collect).toContain('builder-icon--library');
    expect(collect).toContain('aria-haspopup="menu"');
    // With nothing selected they keep focus, do nothing, and say how many
    // are selected.
    expect(collect).toContain('aria-disabled="true"');
    expect(collect).toContain('aria-describedby="bulk-count-templates"');

    const remove = element(bar, 'bulk-delete-templates');

    expect(textOf(remove)).toBe('Delete selected');
    expect(remove).toContain('aria-disabled="true"');
    expect(remove).toContain('aria-describedby="bulk-count-templates"');
    expect(remove).toContain('builder-button--danger');
    expect(remove).toContain('builder-icon--trash');
    expect(element(bar, 'bulk-export-templates')).toContain(
      'builder-icon--download',
    );
    expect(html).not.toContain('bulk-uncollect-templates');

    // Add to collection always offers a new collection, so it stays without one.
    const none = await tab((store) => {
      store.templates = { ...store.templates, collections: [] };
    });

    expect(buttonIds(element(none.html, 'bulk-bar-templates'))).toEqual(
      buttonIds(bar),
    );
  });

  test('the built-in templates are read only, with View, Duplicate and Download', async () => {
    startList.value = SHOW_BUILTIN;

    const { html } = await tab();
    const [card] = html.split('<li ').slice(1);

    expect(textOf(element(html, 'templates-builtin-note'))).toBe(
      'Built-in templates are read only. To change one, select Duplicate. This adds a copy to the library.',
    );
    expect(
      [...html.matchAll(/data-testid="template-card-builtin-([a-z]+)"/g)].map(
        ([, id]) => id,
      ),
    ).toEqual(BUILTIN_TEMPLATES.map((entry) => entry.id));
    expect(card).toContain('data-testid="template-card-builtin-server"');
    expect(textOf(element(card, 'template-read-only-builtin-server'))).toBe(
      'Read only',
    );
    expect(buttonIds(card)).toEqual([
      'template-view-builtin-server',
      'template-duplicate-builtin-server',
      'template-export-builtin-server',
    ]);
    expect(element(card, 'template-view-builtin-server')).toContain(
      'aria-haspopup="dialog"',
    );
    expect(element(card, 'template-duplicate-builtin-server')).toContain(
      'aria-label="Duplicate template Server to the library"',
    );
    expect(textOf(element(card, 'template-duplicate-builtin-server'))).toBe(
      'Duplicate',
    );
    expect(element(card, 'template-duplicate-builtin-server')).toContain(
      'builder-icon--copy',
    );
    // A read-only template has View, with an eye.
    expect(textOf(element(card, 'template-view-builtin-server'))).toBe('View');
    expect(element(card, 'template-view-builtin-server')).toContain(
      'builder-icon--eye',
    );
    expect(element(card, 'template-view-builtin-server')).not.toContain(
      'builder-button--primary',
    );
    // Duplicate selected, and Download selected: nothing that changes them.
    const bar = element(html, 'bulk-bar-templates');

    expect(bar).toContain('aria-label="Bulk actions: Built-in templates"');
    expect(buttonIds(bar)).toEqual([
      'bulk-duplicate-templates',
      'bulk-export-templates',
    ]);
    expect(textOf(element(bar, 'bulk-duplicate-templates'))).toBe(
      'Duplicate selected',
    );
    expect(html).not.toContain('template-edit-');
    expect(html).not.toContain('template-delete-');
  });

  test('a collection of a template file on the server is read only, and can be duplicated whole', async () => {
    startList.value = 'preloaded:srv';

    const { html } = await tab((store) => {
      store.templates = libraryOf({
        templates: [
          template('plc'),
          template('fw', { source: 'preloaded', version: 1 }),
        ],
        collections: [
          {
            id: 'srv',
            source: 'preloaded',
            name: 'Edge kit',
            templateIds: ['fw'],
          },
        ],
      });
    });
    const block = element(html, 'collection-block');

    expect(textOf(block.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Edge kit Read only',
    );
    expect(textOf(element(block, 'collection-preloaded'))).toBe(
      'Read from a template file on the phenix server. To change a template, select Duplicate. This adds a copy to the library.',
    );
    expect(buttonIds(block)).toEqual([
      'collection-duplicate',
      'collection-export',
    ]);
    expect(textOf(element(block, 'collection-duplicate'))).toBe(
      'Duplicate collection',
    );
    expect(element(block, 'collection-duplicate')).toContain(
      'builder-icon--copy',
    );
    expect(element(block, 'collection-export')).toContain(
      'builder-icon--download',
    );
    expect(
      [...html.matchAll(/data-testid="template-card-([^"]+)"/g)].map(
        ([, id]) => id,
      ),
    ).toEqual(['preloaded-fw']);
    expect(buttonIds(html.split('<li ')[1])).toEqual([
      'template-view-preloaded-fw',
      'template-duplicate-preloaded-fw',
      'template-export-preloaded-fw',
    ]);
    // Under Server in Show.
    expect(element(html, 'templates-show-preloaded')).toContain(
      'label="Server"',
    );
  });

  test('a collection of the library has Edit, Delete and Download collection as item actions', async () => {
    startList.value = 'c1';

    const { html } = await tab();
    const block = element(html, 'collection-block');

    expect(buttonIds(block)).toEqual([
      'collection-edit',
      'collection-delete',
      'collection-export',
    ]);
    expect(element(block, 'collection-edit')).toContain(
      'builder-button--primary',
    );
    expect(element(block, 'collection-edit')).toContain('builder-icon--pencil');
    expect(element(block, 'collection-delete')).toContain(
      'builder-button--danger',
    );
    expect(element(block, 'collection-delete')).toContain(
      'builder-icon--trash',
    );
    expect(element(block, 'collection-export')).toContain(
      'builder-icon--download',
    );
    expect(textOf(element(block, 'collection-edit'))).toBe('Edit collection');
  });

  test('what it says before the library is read, when it cannot be, and when it holds nothing', async () => {
    const state = async (templates) => {
      const { html } = await render(BuilderTemplates, {}, (store) => {
        store.templates = { ...store.templates, ...templates };
      });

      return html;
    };

    for (const status of ['idle', 'loading']) {
      const html = await state({ status });

      expect(html).toMatch(/<p role="status"[^>]*>Loading…<\/p>/);
      expect(html).not.toContain('templates-list');
      expect(html).not.toContain('templates-error');
    }

    const failed = await state({ status: 'failed', error: 'Store is down.' });

    expect(element(failed, 'templates-error')).toContain('role="alert"');
    expect(textOf(element(failed, 'templates-error'))).toBe(
      'Could not load the template library. Store is down.',
    );
    expect(textOf(element(failed, 'templates-retry'))).toBe('Retry');
    expect(failed).not.toContain('Loading…');
    expect(failed).not.toContain('templates-show');

    // While Retry reads it again, the button waits, in place.
    const retrying = await state({
      status: 'loading',
      error: 'Store is down.',
    });

    expect(element(retrying, 'templates-retry')).toContain(
      'aria-disabled="true"',
    );
    expect(element(retrying, 'templates-retry')).toContain('aria-busy="true"');
    expect(retrying).toMatch(
      /data-testid="templates-retry"[\s\S]*?Retry[\s\S]*?data-testid="builder-spinner"/,
    );

    // A later read that failed keeps the list, under why.
    const stale = await state({
      ...libraryOf({ templates: [template('plc')] }),
      status: 'failed',
      error: 'Store is down.',
    });

    expect(stale).toContain('templates-error');
    expect(stale).toContain('data-testid="template-card-plc"');

    // A library the server cannot read lists none of its templates. Show still
    // offers the built-in ones.
    const damaged = await state(libraryOf({ damaged: true }));

    expect(textOf(element(damaged, 'templates-damaged'))).toBe(
      'The Builder cannot read the template library. A newer version of phenix may have saved it.',
    );
    expect(damaged).toContain('templates-show');
    expect(damaged).not.toContain('templates-empty');
    expect(damaged).not.toContain('templates-list');
    expect(damaged).not.toContain('bulk-bar-templates');

    expect(textOf(element(await state(libraryOf()), 'templates-empty'))).toBe(
      'The library has no templates. Select New template to make one. Built-in templates and server collections are under Show.',
    );
    // No row above a list of nothing.
    expect(await state(libraryOf())).not.toContain('bulk-bar-templates');
  });
});

describe('the collection dialog', () => {
  const labelOf = (html, id) =>
    textOf(
      html.match(new RegExp(`<label for="${id}"[^>]*>[\\s\\S]*?</label>`))[0],
    );

  test('a new collection: its two fields and Create collection', async () => {
    const { html } = await render(CollectionDialog);
    const [dialog] = tags(html, 'dialog');
    const [title] = html.match(/<h2[^>]*>[\s\S]*?<\/h2>/);

    expect(dialog).toContain('aria-labelledby="collection-dialog-title"');
    expect(textOf(title)).toBe('New collection');
    expect(labelOf(html, 'collection-name')).toBe('Name *');
    expect(labelOf(html, 'collection-description')).toBe('Description');
    expect(element(html, 'collection-name')).toContain('required');
    expect(element(html, 'collection-name')).not.toContain('aria-invalid');
    expect(buttonIds(html).slice(-2)).toEqual([
      'collection-cancel',
      'collection-save',
    ]);
    expect(textOf(element(html, 'collection-save'))).toBe('Create collection');
    expect(element(html, 'collection-save')).toContain('type="submit"');
    expect(html).not.toContain('collection-members');
    // Its alert and its hint are there from the start, empty.
    expect(html).toMatch(
      /<p id="collection-error"[^>]*role="alert"[^>]*><!---->/,
    );
    expect(textOf(element(html, 'collection-name-hint'))).toBe('');
  });

  test('one started from a selection says what joins it', async () => {
    const one = await render(CollectionDialog, { templateIds: ['a'] });
    const three = await render(CollectionDialog, {
      templateIds: ['a', 'b', 'c'],
    });

    expect(textOf(element(one.html, 'collection-members'))).toBe(
      'Create collection adds the selected template to the new collection.',
    );
    expect(textOf(element(three.html, 'collection-members'))).toBe(
      'Create collection adds the 3 selected templates to the new collection.',
    );
  });

  test('a collection of the library: named in the title, with its values and Save', async () => {
    const library = libraryOf({
      collections: [
        { id: 'c1', name: 'Plant floor', description: 'Level 1' },
        { id: 'c2', name: ' plant FLOOR ' },
      ],
    });
    const { html } = await render(
      CollectionDialog,
      { collection: library.collections[0] },
      (store) => {
        store.templates = library;
      },
    );

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Edit collection Plant floor',
    );
    expect(element(html, 'collection-name')).toContain('value="Plant floor"');
    expect(element(html, 'collection-description')).toContain(
      'value="Level 1"',
    );
    expect(textOf(element(html, 'collection-save'))).toBe('Save');
    // A name another collection has is said, not refused.
    expect(element(html, 'collection-name-hint')).toContain('role="status"');
    expect(textOf(element(html, 'collection-name-hint'))).toBe(
      'Another collection has this name.',
    );
    expect(html).not.toContain('collection-members');

    // Its own name is not another's.
    const alone = await render(
      CollectionDialog,
      { collection: library.collections[0] },
      (store) => {
        store.templates = libraryOf({
          collections: [library.collections[0]],
        });
      },
    );

    expect(textOf(element(alone.html, 'collection-name-hint'))).toBe('');
  });
});

describe('the palette’s library', () => {
  // The names of the sections, as their headers show them.
  const groupNames = (html) =>
    [
      ...html.matchAll(
        /<span class="builder-palette__group-name"[^>]*>([\s\S]*?)<\/span>/g,
      ),
    ].map(([, inner]) => textOf(inner));
  // The list of a section, by its id.
  const listOf = (html, id) =>
    html.match(
      new RegExp(
        `<ul[^>]*data-testid="palette-templates-${id}"[\\s\\S]*?</ul>`,
      ),
    )[0];

  afterEach(() => {
    vi.unstubAllGlobals();
  });
  const library = () =>
    libraryOf({
      templates: [
        template('server', { name: 'Server', description: 'Generic' }),
        template('plc', {
          device: { ...template('plc').device, icon: 'plc-icon' },
        }),
      ],
    });

  test('the library button stands after "+", named for where it goes', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.readOnly = true;
    });
    const header = html.slice(
      html.indexOf('builder-palette__header--templates'),
      html.indexOf('id="palette-new-template-why"'),
    );

    expect(buttonIds(header)).toEqual([
      'palette-new-template',
      'palette-library',
    ]);
    // The headings have no help buttons beside them.
    expect(header).toContain('>Templates</h3>');
    expect(html).not.toContain('palette-help');
    expect(html).not.toContain('palette-templates-help');
    expect(html).not.toContain('builder-icon--help');
    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Nodes & Objects',
    );

    const button = element(html, 'palette-library');

    expect(button).toContain('aria-label="Node Templates library"');
    expect(button).toContain('builder-icon--library');
    // It leaves the draft, which a read-only one allows too.
    expect(button).not.toContain('aria-disabled');
    expect(button).not.toMatch(/\sdisabled(\s|>|=)/);
  });

  test('a library that was read is a section, before the built-in templates, with the icons it names', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = library();
    });
    const list = listOf(html, 'library');

    expect(groupNames(html)).toEqual(['Library', 'Built-in']);
    expect(list).toContain('aria-label="Library"');
    expect(buttonIds(list)).toEqual([
      'palette-template-server',
      'palette-template-plc',
    ]);
    expect(list).toContain('aria-label="Add Server device"');
    expect(list).toContain('aria-describedby="palette-template-server-hint"');
    expect(list).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    // Only a template of the diagram has a menu.
    expect(list).not.toContain('aria-haspopup');
    // The built-in templates are always there. Server shares its test id with
    // the library template "server", so its own adds the section.
    expect(buttonIds(listOf(html, 'builtin'))).toEqual(
      BUILTIN_TEMPLATES.map((entry) =>
        entry.id === 'server'
          ? 'palette-template-server-in-builtin'
          : `palette-template-${entry.id}`,
      ),
    );
    expect(textOf(element(html, 'palette-library-note'))).toBe('');
    expect(html).not.toContain('palette-library-retry');

    // While it is read again it stays, and nothing is said.
    const again = await render(BuilderPalette, {}, (store) => {
      store.templates = { ...library(), status: 'loading' };
    });

    expect(again.html).toContain('palette-templates-library');
    expect(textOf(element(again.html, 'palette-library-note'))).toBe('');
  });

  test('the diagram’s templates come first', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, template('rtu')).doc;
      store.templates = library();
    });

    expect(groupNames(html)).toEqual(['This diagram', 'Library', 'Built-in']);
    expect(html.indexOf('palette-templates-diagram')).toBeLessThan(
      html.indexOf('palette-templates-library'),
    );
  });

  test('a template of a server collection says where it is from', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = libraryOf({
        templates: [
          template('fw', {
            source: 'preloaded',
            description: 'Edge firewall',
          }),
        ],
        collections: [
          {
            id: 'srv',
            source: 'preloaded',
            name: 'Edge kit',
            templateIds: ['fw'],
          },
        ],
      });
    });
    const list = listOf(html, 'preloaded:srv');

    expect(groupNames(html)).toEqual(['Server: Edge kit', 'Built-in']);
    expect(buttonIds(list)).toEqual(['palette-template-preloaded-fw']);
    expect(
      textOf(
        list.match(
          /<span id="palette-template-preloaded-fw-hint"[^>]*>[\s\S]*?<\/span>/,
        )[0],
      ),
    ).toBe(
      'Edge firewall From the server collection Edge kit, which is read only.',
    );
  });

  test('each collection is a section whose header collapses it, and the palette remembers the collapsed ones', async () => {
    vi.stubGlobal(
      'localStorage',
      memoryStorage({
        'phenix.builder.palette': JSON.stringify({
          collapsed: ['library:plant'],
        }),
      }),
    );

    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = libraryOf({
        templates: [template('plc'), template('hmi'), template('rtu')],
        collections: [
          { id: 'plant', name: 'Plant', templateIds: ['plc', 'hmi'] },
          { id: 'one', name: 'One', templateIds: ['hmi'] },
        ],
      });
    });
    const header = (id) => element(html, `palette-section-${id}`);

    expect(groupNames(html)).toEqual([
      'Other library templates',
      'Plant',
      'One',
      'Built-in',
    ]);
    // The header is a button that names its section and counts its
    // templates, and says whether its list shows.
    expect(header('library:plant')).toContain(
      'aria-label="Plant, 2 templates"',
    );
    expect(header('library:one')).toContain('aria-label="One, 1 template"');
    // The header shows the number in parentheses after the name.
    expect(textOf(header('library:plant'))).toBe('Plant (2)');
    expect(textOf(header('library:one'))).toBe('One (1)');
    expect(header('library:plant')).toContain('aria-expanded="false"');
    expect(header('library:one')).toContain('aria-expanded="true"');
    expect(header('library:plant')).toContain(
      'aria-controls="palette-section-list-library%3Aplant"',
    );
    expect(listOf(html, 'library:plant')).toContain(
      'id="palette-section-list-library%3Aplant"',
    );
    // A collapsed list stays in the page, hidden.
    expect(tags(listOf(html, 'library:plant'), 'ul')[0]).toContain(
      'display:none',
    );
    expect(tags(listOf(html, 'library:one'), 'ul')[0]).not.toContain(
      'display:none',
    );
    // A template in two collections is in both, under two test ids.
    expect(buttonIds(listOf(html, 'library:plant'))).toEqual([
      'palette-template-plc',
      'palette-template-hmi',
    ]);
    expect(buttonIds(listOf(html, 'library:one'))).toEqual([
      'palette-template-hmi-in-library:one',
    ]);
    expect(buttonIds(listOf(html, 'library'))).toEqual([
      'palette-template-rtu',
    ]);
  });

  test('without storage, every section shows', async () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked');
      },
    });

    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, template('rtu')).doc;
      store.templates = library();
    });

    expect(groupNames(html)).toEqual(['This diagram', 'Library', 'Built-in']);
    expect(element(html, 'palette-section-diagram')).toContain(
      'aria-expanded="true"',
    );
    expect(element(html, 'palette-section-library')).toContain(
      'aria-expanded="true"',
    );
    expect(html).not.toContain('display:none');
  });

  test('while it is first read the list says so, with the built-in templates only', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = { ...store.templates, status: 'loading' };
    });
    const note = element(html, 'palette-library-note');

    expect(note).toContain('role="status"');
    expect(textOf(note)).toBe('Loading the template library…');
    expect(
      [...html.matchAll(/data-testid="palette-templates-([^"]+)"/g)].map(
        ([, id]) => id,
      ),
    ).toEqual(['builtin']);
    expect(html).not.toContain('palette-library-retry');
  });

  test('when it cannot be read the built-in templates are still there, with why and Retry', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, template('rtu')).doc;
      store.templates = {
        ...store.templates,
        status: 'failed',
        error: 'Store is down.',
      };
    });

    expect(groupNames(html)).toEqual(['This diagram', 'Built-in']);
    expect(textOf(element(html, 'palette-library-note'))).toBe(
      'The Builder could not load the template library.',
    );

    const retry = element(html, 'palette-library-retry');

    expect(textOf(retry)).toBe('Retry');
    expect(retry).toContain('aria-describedby="palette-library-note"');
    expect(retry).not.toContain('aria-disabled');
    // The note and Retry follow the lists.
    expect(html.indexOf('palette-templates-builtin')).toBeLessThan(
      html.indexOf('data-testid="palette-library-note"'),
    );

    // While Retry reads it again, they stay, and Retry waits.
    const again = await render(BuilderPalette, {}, (store) => {
      store.templates = {
        ...store.templates,
        status: 'loading',
        error: 'Store is down.',
      };
    });

    expect(again.html).toContain('palette-templates-builtin');
    expect(element(again.html, 'palette-library-retry')).toContain(
      'aria-disabled="true"',
    );
    expect(textOf(element(again.html, 'palette-library-note'))).toBe(
      'The Builder could not load the template library.',
    );
  });

  test('a library the server cannot read says so, with the built-in templates and no Retry', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = libraryOf({ damaged: true });
    });

    expect(html).toContain('palette-templates-builtin');
    expect(textOf(element(html, 'palette-library-note'))).toBe(
      'The Builder cannot read the template library. A newer version of phenix may have saved it.',
    );
    expect(html).not.toContain('palette-library-retry');
  });

  // A library of one template, at `version`, and a diagram with a copy of
  // its version 1 (see linkedCopy), as the store renders it.
  const LINKED = '6f1c2a54-3b7e-4c1d-9a2f-0e8b7d6c5a41';
  const linked = (version, { readOnly = false, edit = null } = {}) =>
    render(BuilderPalette, {}, (store) => {
      const first = libraryOf({
        templates: [template(LINKED, { name: 'PLC' })],
      }).items[0];
      const { doc, template: copy } = addTemplate(store.doc, linkedCopy(first));

      store.doc = edit ? updateTemplate(doc, copy.id, edit) : doc;
      store.templates = libraryOf({
        templates: version
          ? [template(LINKED, { name: 'PLC', description: 'v2', version })]
          : [],
      });
      store.readOnly = readOnly;
    });
  const copyId = (html) =>
    html.match(/data-testid="palette-template-diagram-([^"]+)"/)[1];

  test('a copy the library has a newer version of says so, with Update, and its menu has Update and Unlink', async () => {
    const { html, store } = await linked(2);
    const id = copyId(html);
    const mark = element(html, `palette-template-link-${id}`);

    expect(mark).toContain('builder-palette__link--update');
    expect(mark).toContain('builder-icon--refresh');
    expect(
      textOf(
        mark.match(
          /<span class="builder-palette__link-text"[^>]*>[\s\S]*?<\/span>/,
        )[0],
      ),
    ).toBe('Update available');
    expect(textOf(element(mark, `palette-template-update-${id}`))).toBe(
      'Update',
    );

    const update = element(mark, `palette-template-update-${id}`);

    expect(update).toContain('aria-label="Update template PLC"');
    expect(update).toContain('aria-haspopup="dialog"');
    // The entry's description says it in full.
    expect(
      textOf(
        html.match(
          new RegExp(
            `<span id="palette-template-diagram-${id}-hint"[^>]*>[\\s\\S]*?</span>`,
          ),
        )[0],
      ),
    ).toBe(
      'The library has a newer version of this template (version 2). The diagram has version 1.',
    );
    expect(element(html, `palette-template-diagram-${id}`)).toContain(
      `aria-describedby="palette-template-diagram-${id}-hint"`,
    );
    expect(store.templateUpdates.map((link) => link.template.id)).toEqual([id]);
  });

  test('a copy whose library template was deleted says so, with Unlink', async () => {
    const { html } = await linked(0);
    const id = copyId(html);
    const mark = element(html, `palette-template-link-${id}`);

    expect(mark).toContain('builder-palette__link--deleted');
    expect(mark).toContain('builder-icon--warning');
    expect(mark).toContain('Deleted from library');
    expect(textOf(element(mark, `palette-template-unlink-${id}`))).toBe(
      'Unlink',
    );
    expect(element(mark, `palette-template-unlink-${id}`)).toContain(
      'aria-label="Unlink template PLC"',
    );
    expect(mark).not.toContain('aria-haspopup');
  });

  test('a current copy has no mark, and a read-only diagram shows the mark without a button', async () => {
    const current = await linked(1);

    expect(current.html).not.toContain('palette-template-link-');

    const viewing = await linked(2, { readOnly: true });
    const id = copyId(viewing.html);

    expect(textOf(element(viewing.html, `palette-template-link-${id}`))).toBe(
      'Update available',
    );
    expect(viewing.html).not.toContain(`palette-template-update-${id}`);
  });
});

describe('the notice of the diagram’s templates in the Inspector', () => {
  const LINKED = [
    '6f1c2a54-3b7e-4c1d-9a2f-0e8b7d6c5a41',
    '0a7e3c9b-5d21-4f8e-b6c4-2e9d1f0a8b37',
  ];
  // A diagram with copies of PLC and HMI at version 1, and a library that
  // lists the templates of `listed` ({name: version}).
  const notice = (listed, { readOnly = false } = {}) =>
    render(InspectorTemplateLinks, {}, (store) => {
      store.doc = ['PLC', 'HMI'].reduce(
        (doc, name, index) =>
          addTemplate(
            doc,
            linkedCopy(
              libraryOf({ templates: [template(LINKED[index], { name })] })
                .items[0],
            ),
          ).doc,
        store.doc,
      );
      store.templates = libraryOf({
        templates: Object.entries(listed).map(([name, version]) =>
          template(LINKED[name === 'PLC' ? 0 : 1], { name, version }),
        ),
      });
      store.readOnly = readOnly;
    });
  const ids = (html) =>
    [
      ...html.matchAll(
        /data-testid="inspector-template-link-(?!text-)([^"]+)"/g,
      ),
    ].map(([, id]) => id);

  test('it is not there while every copy is current', async () => {
    const { html } = await notice({ PLC: 1, HMI: 1 });

    expect(html).not.toContain('inspector-template-links');
  });

  test('it lists the copies with an update, each with Update, and Update all', async () => {
    const { html } = await notice({ PLC: 3, HMI: 2 });
    const section = element(html, 'inspector-template-links');

    expect(section).toMatch(/^<section/);
    expect(section).toContain(
      'aria-labelledby="inspector-template-links-title"',
    );
    expect(textOf(section.match(/<h3[^>]*>[\s\S]*?<\/h3>/)[0])).toBe(
      'Template updates',
    );
    expect(textOf(element(section, 'inspector-template-links-summary'))).toBe(
      'The library has a newer version of 2 templates in this diagram. Devices made from these templates do not change.',
    );

    const [plc, hmi] = ids(section);
    const row = element(section, `inspector-template-link-${plc}`);

    expect(textOf(element(row, `inspector-template-link-text-${plc}`))).toBe(
      'The library has a newer version of this template (version 3). The diagram has version 1.',
    );
    expect(row).toContain('PLC');

    const update = element(row, `inspector-template-update-${plc}`);

    expect(update).toContain('aria-label="Update template PLC"');
    expect(update).toContain(
      `aria-describedby="inspector-template-link-text-${plc}"`,
    );
    expect(update).toContain('aria-haspopup="dialog"');
    expect(section).toContain(`inspector-template-update-${hmi}`);
    expect(textOf(element(section, 'inspector-template-update-all'))).toBe(
      'Update all',
    );
  });

  test('a copy whose library template was deleted has Unlink, and one update has no Update all', async () => {
    const { html } = await notice({ HMI: 2 });
    const section = element(html, 'inspector-template-links');
    const [plc, hmi] = ids(section);

    expect(textOf(element(section, 'inspector-template-links-summary'))).toBe(
      'The library has a newer version of 1 template in this diagram. 1 template is no longer in the library or on the server. Devices made from these templates do not change.',
    );
    expect(
      textOf(element(section, `inspector-template-link-text-${plc}`)),
    ).toBe('Someone deleted the library template.');
    expect(element(section, `inspector-template-unlink-${plc}`)).toContain(
      'aria-label="Unlink template PLC"',
    );
    expect(section).toContain(`inspector-template-update-${hmi}`);
    expect(section).not.toContain('inspector-template-update-all');
  });

  test('a read-only diagram shows the notice without buttons', async () => {
    const { html } = await notice({ PLC: 2, HMI: 2 }, { readOnly: true });
    const section = element(html, 'inspector-template-links');

    expect(ids(section)).toHaveLength(2);
    expect(buttonIds(section)).toEqual([]);
  });
});

describe('the template editor on a template of the library', () => {
  const saveText = (html) => textOf(element(html, 'template-save'));
  const titleOf = (html) => textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0]);

  test('a new one: New library template, and Save to library', async () => {
    const { html } = await render(TemplateDialog, {
      mode: 'library-new',
      template: blankTemplate(),
    });

    expect(titleOf(html)).toBe('New library template');
    expect(saveText(html)).toBe('Save to library');
    expect(element(html, 'template-save')).not.toContain('aria-disabled');
    // The Inspector's own form, as for a template of a diagram.
    expect(html).toContain('builder-inspector--template');
    expect(html).toContain('Hostname');
  });

  test('one the library has: named in the title, with its icon from the icon library', async () => {
    const library = libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          device: { ...template('plc').device, icon: 'plc-icon' },
        }),
        // Another of the same name in the library, which is said.
        template('other', { name: ' plc ' }),
      ],
    });
    const { html } = await render(
      TemplateDialog,
      { mode: 'library-edit', template: library.items[0] },
      (store) => {
        store.templates = library;
        // A template of the open diagram has nothing to do with it.
        store.doc = addTemplate(store.doc, template('x', { name: 'X' })).doc;
      },
    );

    expect(titleOf(html)).toBe('Edit template PLC');
    expect(saveText(html)).toBe('Save');
    expect(element(html, 'template-name')).toContain('value="PLC"');
    expect(element(html, 'template-description')).toContain(
      'value="A controller"',
    );
    expect(html).toContain('value="plc.qc2"');
    expect(html).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(textOf(element(html, 'template-name-hint'))).toBe(
      'Another template has this name.',
    );
  });

  test('a name a template of the diagram has means nothing to the library', async () => {
    const { html } = await render(
      TemplateDialog,
      { mode: 'library-new', template: { ...blankTemplate(), name: 'RTU' } },
      (store) => {
        store.doc = addTemplate(store.doc, template('rtu')).doc;
        store.templates = libraryOf({ templates: [template('plc')] });
      },
    );

    expect(textOf(element(html, 'template-name-hint'))).toBe('');
  });
});

describe('the library commands', () => {
  function context({ store = {}, view = {} } = {}) {
    const fullView = { editing: true, ...view };

    for (const name of VIEW_API) {
      fullView[name] ??= vi.fn();
    }

    return createCommandContext({
      store: withTemplates({
        doc: sampleDocument().doc,
        readOnly: false,
        selection: { nodes: [], edges: [] },
        announce: vi.fn(),
        addNode: vi.fn(() => ({ id: 'added' })),
        ...store,
      }),
      view: fullView,
    });
  }

  test('Open Node Templates library leaves the editor for the tab, also from a read-only draft', () => {
    const ctx = context({ store: { readOnly: true } });
    const command = getCommand('templates.library');

    expect(command).toMatchObject({
      title: 'Open Node Templates library',
      group: 'Go to',
      keywords: ['template', 'library', 'collection'],
    });
    expect(command.keys).toBeUndefined();
    expect(command).not.toHaveProperty('phrase');
    // The editor's alone: the landing has the tab itself.
    expect(command.views).toBeUndefined();
    expect(availability(command, ctx)).toBe(true);
    expect(runCommand('templates.library', ctx)).toBe(true);
    expect(ctx.view.openTemplateLibrary).toHaveBeenCalledOnce();
    expect(VIEW_API).toContain('openTemplateLibrary');
  });

  test('Show Node Templates shows the landing’s tab, unless it is shown', () => {
    const command = getCommand('drafts.tab.templates');
    const landing = context({ view: { editing: false, draftsTab: 'drafts' } });

    expect(command).toMatchObject({
      title: 'Show Node Templates',
      group: 'Drafts',
      views: ['landing'],
      keywords: ['tab', 'list', 'library', 'collection'],
    });
    expect(command.keys).toBeUndefined();
    expect(availability(command, landing)).toBe(true);
    expect(runCommand(command, landing)).toBe(true);
    expect(landing.view.showDraftsTab).toHaveBeenCalledWith('templates');
    expect(
      availability(
        command,
        context({ view: { editing: false, draftsTab: 'templates' } }),
      ),
    ).toBe('This tab is already shown.');
  });

  test('Show Icons shows the landing’s Icons tab, unless it is shown', () => {
    const command = getCommand('drafts.tab.icons');
    const landing = context({ view: { editing: false, draftsTab: 'drafts' } });

    expect(command).toMatchObject({
      title: 'Show Icons',
      group: 'Drafts',
      views: ['landing'],
    });
    expect(command.keywords).toContain('custom icon');
    expect(availability(command, landing)).toBe(true);
    expect(runCommand(command, landing)).toBe(true);
    expect(landing.view.showDraftsTab).toHaveBeenCalledWith('icons');
    expect(
      availability(
        command,
        context({ view: { editing: false, draftsTab: 'icons' } }),
      ),
    ).toBe('This tab is already shown.');
    expect(VIEW_API).toContain('openIcons');
  });

  test('Add device offers the library’s templates by section, each once, with the icons they name', () => {
    const templates = libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          device: { ...template('plc').device, icon: 'plc-icon' },
        }),
        template('hmi'),
        template('fw', {
          source: 'preloaded',
          device: {
            iconKey: 'firewall',
            spec: { general: { hostname: 'fw' } },
          },
        }),
      ],
      collections: [
        { id: 'plant', name: 'Plant', templateIds: ['plc'] },
        { id: 'office', name: 'Office', templateIds: ['plc'] },
        { id: 'srv', source: 'preloaded', name: 'Edge', templateIds: ['fw'] },
      ],
    });
    const ctx = context({ store: { templates } });
    const choices = getCommand('add.device').choices(ctx);

    // A template in two collections is one choice, under the first. The
    // built-in templates come last.
    expect(
      choices.slice(0, 4).map((choice) => [choice.title, choice.detail]),
    ).toEqual([
      ['Device', 'A virtual machine, container or external device'],
      ['HMI', 'Other library templates · hmi.qc2'],
      ['PLC', 'Plant · plc.qc2'],
      ['FW', 'Server: Edge · no image'],
    ]);
    expect(choices.slice(0, 4).map((choice) => choice.value)).toEqual([
      '',
      'library:hmi',
      'library:plc',
      'preloaded:fw',
    ]);
    expect(choices.slice(4).map((choice) => choice.value)).toEqual(
      BUILTIN_TEMPLATES.map((entry) => `builtin:${entry.id}`),
    );
    expect(choices[2].keywords).toEqual(['A controller']);

    runCommand('add.device', ctx, choices[2]);
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'plc',
        look: { iconKey: 'router', icon: 'plc-icon' },
      }),
    );
  });
});

describe('a read-only template in the editor', () => {
  const view = (template) =>
    render(TemplateDialog, { mode: 'view', template, icons: {} });
  const builtin = () => ({ ...BUILTIN_TEMPLATES[2], source: 'builtin' });

  test('a built-in template is read only, says so, and offers Duplicate to library', async () => {
    const { html } = await view(builtin());

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      `Template ${BUILTIN_TEMPLATES[2].name}`,
    );
    expect(textOf(element(html, 'template-origin'))).toBe(
      'A built-in template, which is read only',
    );
    expect(html).toContain('aria-describedby="template-origin"');

    const name = element(html, 'template-name');

    expect(name).toContain('readonly');
    expect(name).not.toContain('required');
    expect(element(html, 'template-description')).toContain('readonly');
    expect(html).not.toMatch(/Name<span aria-hidden="true"> \*<\/span>/);
    expect(textOf(element(html, 'template-cancel'))).toBe('Close');

    const duplicate = element(html, 'template-save');

    expect(textOf(duplicate)).toBe('Duplicate to library');
    expect(duplicate).toContain('type="button"');
    expect(duplicate).not.toContain('form=');
  });

  test('a template of a template file on the server says where it is from', async () => {
    const { html } = await view(
      template('fw', { source: 'preloaded', description: 'Edge firewall' }),
    );

    expect(textOf(element(html, 'template-origin'))).toBe(
      'Read from a template file on the phenix server',
    );
    // The node fields are locked.
    expect(html).toMatch(/value="fw"[^>]*readonly|readonly[^>]*value="fw"/);
    expect(textOf(element(html, 'template-save'))).toBe('Duplicate to library');
  });

  // A template of the library opens to edit from its card and the command
  // palette. Opened to view, it says where it is from, and is not copied.
  test('a template of the library opened to view says so, and has no Duplicate', async () => {
    const own = template('rtu', { source: 'library' });
    const { html } = await view(own);

    expect(textOf(element(html, 'template-origin'))).toBe(
      'A template of the library.',
    );
    expect(html).toContain('aria-describedby="template-origin"');
    expect(element(html, 'template-name')).toContain('readonly');
    expect(html).not.toContain('template-save');
  });

  test('an editable template has no origin line', async () => {
    const own = template('rtu');
    const { html } = await render(
      TemplateDialog,
      { mode: 'library-edit', template: own, icons: {} },
      (store) => {
        store.templates = libraryOf({ templates: [own] });
      },
    );

    expect(html).not.toContain('template-origin');
  });
});
