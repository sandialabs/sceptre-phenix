// The Node Templates tab, the collection dialog, the palette's library and
// the template editor on a template of the library, rendered on the server,
// and the commands that go with the library. What needs a browser (menus,
// focus, saving, deleting) is left to builder-templates.spec.js.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

// The signed-in user: a test gives it a role to say what it may do.
const phenix = vi.hoisted(() => ({ username: 'alice', role: null }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

import BuilderPalette from '@/components/builder/BuilderPalette.vue';
import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
import CollectionDialog from '@/components/builder/dialogs/CollectionDialog.vue';
import TemplateDialog from '@/components/builder/dialogs/TemplateDialog.vue';
import {
  VIEW_API,
  availability,
  createCommandContext,
  getCommand,
  runCommand,
} from '@/builder/commands.js';
import { addTemplate, createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { blankTemplate } from '@/builder/templates.js';

import { libraryOf, sampleDocument, tags, withTemplates } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

// A role that may do `verbs` to configs, and so to its template library.
// roleAllowed remembers its answers by the role's name.
function roleWith(...verbs) {
  return {
    name: `configs-${verbs.join('-') || 'none'}`,
    policies: [{ resources: ['configs'], resourceNames: ['*'], verbs }],
  };
}

const EVERYTHING = roleWith('list', 'get', 'create', 'update', 'delete');

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

// What an element holds, as text: tags out, white space as one space.
function textOf(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]*>/g, '')
    .replace(/\s+/g, ' ')
    .trim();
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
async function render(component, props = {}, prepare = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);

  const store = useBuilderStore(pinia);

  store.doc = createDocument({ name: 'Plant' });
  prepare(store);

  return { html: await renderToString(app), store };
}

beforeEach(() => {
  phenix.role = EVERYTHING;
});

describe('the Node Templates tab', () => {
  const library = () =>
    libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          updated: '2026-10-01T12:00:00Z',
          device: { ...template('plc').device, icon: ICON_KEY },
        }),
        template('hmi'),
        template('rtu'),
      ],
      collections: [
        { id: 'c1', name: 'Plant floor', templateIds: ['rtu', 'plc'] },
        { id: 'c2', name: 'Office', templateIds: ['plc'] },
      ],
      icons: { [ICON_KEY]: { name: 'plc', data: ICON_DATA } },
    });
  const tab = (prepare) =>
    render(BuilderTemplates, {}, (store) => {
      store.templates = library();
      prepare?.(store);
    });

  test('lists the user’s templates, a card each, under what makes more', async () => {
    const { html } = await tab();

    expect(buttonIds(html).slice(0, 2)).toEqual([
      'templates-new',
      'collections-new',
    ]);
    expect(textOf(element(html, 'templates-new'))).toBe('New template');
    expect(element(html, 'templates-new')).toContain('aria-haspopup="dialog"');
    expect(textOf(element(html, 'collections-new'))).toBe('New collection');
    expect(html).not.toContain('templates-view-only');

    // Show: every template, or those of one collection.
    expect(html).toContain('<label for="templates-show"');
    expect(
      [...html.matchAll(/<option[^>]*>([\s\S]*?)<\/option>/g)].map(
        ([, inner]) => textOf(inner),
      ),
    ).toEqual(['My templates', 'Plant floor', 'Office']);
    expect(html).toContain('<optgroup label="My collections"');
    expect(html).not.toContain('collection-block');

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
    expect(card).toMatch(
      /<span class="builder-visually-hidden"[^>]*>\s*Select PLC\s*<\/span>/,
    );
    expect(buttonIds(card)).toEqual([
      'template-edit-plc',
      'template-delete-plc',
    ]);
    expect(element(card, 'template-edit-plc')).toContain(
      'aria-label="Edit template PLC"',
    );
    expect(element(card, 'template-delete-plc')).toContain(
      'aria-label="Delete template PLC"',
    );
    expect(element(card, 'template-delete-plc')).toContain(
      'builder-button--danger',
    );

    // A template never changed has no time, and one in no collection and
    // with no description says neither.
    expect(next).toContain('data-testid="template-card-hmi"');
    expect(next).toContain('builder-icon--router');
    expect(next).not.toContain('template-time-hmi');
    expect(next).not.toContain('template-collections-hmi');
    expect(next).not.toContain('template-about-hmi');
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
    expect(bar).toContain('aria-label="Bulk actions: My templates"');
    expect(textOf(element(html, 'bulk-count-templates'))).toBe(
      '0 of 3 selected',
    );
    expect(buttonIds(bar)).toEqual([
      'bulk-collect-templates',
      'bulk-delete-templates',
    ]);

    const collect = element(bar, 'bulk-collect-templates');

    expect(textOf(collect)).toBe('Add to collection');
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
    expect(html).not.toContain('bulk-uncollect-templates');
  });

  test('a role is offered only what it may do', async () => {
    phenix.role = roleWith('list');

    const viewer = (await tab()).html;

    expect(textOf(element(viewer, 'templates-view-only'))).toBe(
      'Your role can view templates, but not create them.',
    );
    expect(buttonIds(viewer)).toEqual([]);
    // Nothing can be done to several, so nothing can be selected.
    expect(viewer).not.toContain('type="checkbox"');
    expect(viewer).toContain('data-testid="template-card-plc"');

    phenix.role = roleWith('list', 'delete');

    const deleter = (await tab()).html;

    expect(buttonIds(element(deleter, 'bulk-bar-templates'))).toEqual([
      'bulk-delete-templates',
    ]);
    expect(buttonIds(deleter.split('<li ')[1])).toEqual([
      'template-delete-plc',
    ]);

    phenix.role = roleWith('list', 'update');

    const editor = (await tab()).html;

    expect(buttonIds(element(editor, 'bulk-bar-templates'))).toEqual([
      'bulk-collect-templates',
    ]);
    expect(buttonIds(editor.split('<li ')[1])).toEqual(['template-edit-plc']);

    // With no collection to add to and none to make, there is no menu.
    const none = (
      await tab((store) => {
        store.templates = { ...store.templates, collections: [] };
      })
    ).html;

    expect(none).toContain('bulk-bar-templates');
    expect(buttonIds(element(none, 'bulk-bar-templates'))).toEqual([]);
  });

  test('what it says before the library is read, when it cannot be, and when it holds nothing', async () => {
    const state = async (templates, role = EVERYTHING) => {
      phenix.role = role;

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
      'Could not load your templates. Store is down.',
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

    // A later read that failed keeps the list, under why.
    const stale = await state({
      ...libraryOf({ templates: [template('plc')] }),
      status: 'failed',
      error: 'Store is down.',
    });

    expect(stale).toContain('templates-error');
    expect(stale).toContain('data-testid="template-card-plc"');

    const damaged = await state(libraryOf({ damaged: true }));

    expect(textOf(element(damaged, 'templates-damaged'))).toBe(
      'Your library cannot be read. A newer version of phenix may have saved it.',
    );
    expect(damaged).not.toContain('templates-show');

    expect(textOf(element(await state(libraryOf()), 'templates-empty'))).toBe(
      'Your library has no templates. Select New template to make one.',
    );
    expect(
      textOf(
        element(await state(libraryOf(), roleWith('list')), 'templates-empty'),
      ),
    ).toBe('Your library has no templates.');
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
      'The selected template is added to it.',
    );
    expect(textOf(element(three.html, 'collection-members'))).toBe(
      'The 3 selected templates are added to it.',
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
  const groupNames = (html) =>
    [...html.matchAll(/<h4[^>]*>([\s\S]*?)<\/h4>/g)].map(([, inner]) =>
      textOf(inner),
    );
  const library = () =>
    libraryOf({
      templates: [
        template('server', { name: 'Server', description: 'Generic' }),
        template('plc', {
          device: { ...template('plc').device, icon: ICON_KEY },
        }),
      ],
      icons: { [ICON_KEY]: { name: 'plc', data: ICON_DATA } },
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

    const button = element(html, 'palette-library');

    expect(button).toContain('aria-label="Node Templates library"');
    expect(button).toContain('builder-icon--library');
    // It leaves the draft, which a read-only one allows too.
    expect(button).not.toContain('aria-disabled');
    expect(button).not.toMatch(/\sdisabled(\s|>|=)/);
  });

  test('a library that was read is the one group, under no heading, with its own icons', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = library();
    });
    const [list] = html.match(
      /<ul[^>]*data-testid="palette-templates-own"[\s\S]*?<\/ul>/,
    );

    expect(groupNames(html)).toEqual([]);
    expect(list).toContain('aria-label="My library"');
    expect(buttonIds(list)).toEqual([
      'palette-template-server',
      'palette-template-plc',
    ]);
    expect(list).toContain('aria-label="Add Server device"');
    expect(list).toContain('aria-describedby="palette-template-server-hint"');
    expect(list).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    // Only a template of the diagram has a menu.
    expect(list).not.toContain('aria-haspopup');
    expect(html).not.toContain('palette-templates-builtin');
    expect(textOf(element(html, 'palette-library-note'))).toBe('');
    expect(html).not.toContain('palette-library-retry');

    // While it is read again it stays, and nothing is said.
    const again = await render(BuilderPalette, {}, (store) => {
      store.templates = { ...library(), status: 'loading' };
    });

    expect(again.html).toContain('palette-templates-own');
    expect(textOf(element(again.html, 'palette-library-note'))).toBe('');
  });

  test('the diagram’s templates come first, and both groups are named', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, template('rtu')).doc;
      store.templates = library();
    });

    expect(groupNames(html)).toEqual(['This diagram', 'My library']);
    expect(html.indexOf('palette-templates-diagram')).toBeLessThan(
      html.indexOf('palette-templates-own'),
    );
  });

  test('while it is first read the list says so, and nothing stands in', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = { ...store.templates, status: 'loading' };
    });
    const note = element(html, 'palette-library-note');

    expect(note).toContain('role="status"');
    expect(textOf(note)).toBe('Loading your templates…');
    expect(html).not.toContain('palette-templates-');
    expect(html).not.toContain('palette-library-retry');
  });

  test('when it cannot be read the built-in templates stand in, with why and Retry', async () => {
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
      'Your library could not be loaded.',
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
      'Your library could not be loaded.',
    );
  });

  test('a library the server cannot read says so, with the built-in templates and no Retry', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = libraryOf({ damaged: true });
    });

    expect(html).toContain('palette-templates-builtin');
    expect(textOf(element(html, 'palette-library-note'))).toBe(
      'Your library cannot be read. A newer version of phenix may have saved it.',
    );
    expect(html).not.toContain('palette-library-retry');
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

  test('one the library has: named in the title, with its icon from the library', async () => {
    const library = libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          device: { ...template('plc').device, icon: ICON_KEY },
        }),
        // Another of the same name in the library, which is said.
        template('other', { name: ' plc ' }),
      ],
      icons: { [ICON_KEY]: { name: 'plc icon', data: ICON_DATA } },
    });
    const { html } = await render(
      TemplateDialog,
      {
        mode: 'library-edit',
        template: library.items[0],
        icons: library.icons,
      },
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
    const fullView = { editing: true, dialog: '', ...view };

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
        shelveIcons: vi.fn(),
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
    const landing = context({ view: { editing: false, draftsTab: 'mine' } });

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

  test('Add device offers the library’s templates by group, and takes their icons along', () => {
    const icons = { [ICON_KEY]: { name: 'plc', data: ICON_DATA } };
    const templates = libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          device: { ...template('plc').device, icon: ICON_KEY },
        }),
        template('hmi', { owner: 'bob', source: 'shared' }),
        template('fw', {
          owner: 'carol',
          source: 'server',
          device: {
            iconKey: 'firewall',
            spec: { general: { hostname: 'fw' } },
          },
        }),
      ],
      icons,
    });
    const ctx = context({ store: { templates } });
    const choices = getCommand('add.device').choices(ctx);

    expect(choices.map((choice) => [choice.title, choice.detail])).toEqual([
      ['Device', 'A virtual machine, container or external device'],
      ['PLC', 'My library · plc.qc2'],
      ['HMI', 'Shared with me · hmi.qc2'],
      ['FW', 'Server-wide · no image'],
    ]);
    expect(choices.map((choice) => choice.value)).toEqual([
      '',
      'own:plc',
      'shared:bob/hmi',
      'server:carol/fw',
    ]);
    expect(choices[2].keywords).toEqual(['Shared by bob.']);

    runCommand('add.device', ctx, choices[1]);
    expect(ctx.store.shelveIcons).toHaveBeenCalledWith(icons);
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'plc',
        look: { iconKey: 'router', icon: ICON_KEY },
      }),
    );
  });
});
