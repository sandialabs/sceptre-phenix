// The Node Templates tab, the collection dialog, the palette's library and
// the template editor on a template of the library, rendered on the server,
// and the commands that go with the library; and what sharing adds to them:
// Share, other users' lists, the Share dialog, and another user's template
// in the editor. What needs a browser (menus, focus, saving, deleting,
// other users' cards) is left to builder-templates.spec.js and
// builder-sharing-templates.spec.js.

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
import TemplateShareDialog from '@/components/builder/dialogs/TemplateShareDialog.vue';
import {
  VIEW_API,
  availability,
  createCommandContext,
  getCommand,
  runCommand,
} from '@/builder/commands.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { addTemplate, createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { blankTemplate } from '@/builder/templates.js';

import { libraryOf, sampleDocument, tags, withTemplates } from './fixtures.js';
import { ICON_DATA } from './png.js';

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
      'template-export-plc',
    ]);
    expect(element(card, 'template-edit-plc')).toContain(
      'aria-label="Edit template PLC"',
    );
    expect(element(card, 'template-export-plc')).toContain(
      'aria-label="Export template PLC"',
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
      'bulk-export-templates',
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
    // Reading the library is all it takes to export a template.
    expect(buttonIds(viewer)).toEqual([
      'template-export-plc',
      'template-export-hmi',
      'template-export-rtu',
    ]);
    expect(viewer).not.toContain('templates-import');
    // Nothing can be done to several, so nothing can be selected.
    expect(viewer).not.toContain('type="checkbox"');
    expect(viewer).toContain('data-testid="template-card-plc"');

    phenix.role = roleWith('list', 'delete');

    const deleter = (await tab()).html;

    expect(buttonIds(element(deleter, 'bulk-bar-templates'))).toEqual([
      'bulk-delete-templates',
      'bulk-export-templates',
    ]);
    expect(buttonIds(deleter.split('<li ')[1])).toEqual([
      'template-delete-plc',
      'template-export-plc',
    ]);

    phenix.role = roleWith('list', 'update');

    const editor = (await tab()).html;

    expect(buttonIds(element(editor, 'bulk-bar-templates'))).toEqual([
      'bulk-collect-templates',
      'bulk-export-templates',
    ]);
    expect(buttonIds(editor.split('<li ')[1])).toEqual([
      'template-edit-plc',
      'template-export-plc',
    ]);

    // With no collection to add to and none to make, there is no menu.
    const none = (
      await tab((store) => {
        store.templates = { ...store.templates, collections: [] };
      })
    ).html;

    expect(none).toContain('bulk-bar-templates');
    expect(buttonIds(element(none, 'bulk-bar-templates'))).toEqual([
      'bulk-export-templates',
    ]);
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

    const button = element(html, 'palette-library');

    expect(button).toContain('aria-label="Node Templates library"');
    expect(button).toContain('builder-icon--library');
    // It leaves the draft, which a read-only one allows too.
    expect(button).not.toContain('aria-disabled');
    expect(button).not.toMatch(/\sdisabled(\s|>|=)/);
  });

  test('a library that was read is the one group, under no heading, with the icons it names', async () => {
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

  test('other users’ templates are grouped by how they are reached, and say whose they are', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.templates = libraryOf({
        templates: [
          template('plc'),
          template('rtu', {
            owner: 'bob smith',
            source: 'shared',
            description: 'Remote unit',
          }),
          template('fw', { owner: 'carol', source: 'server' }),
        ],
      });
    });
    const [shared] = html.match(
      /<ul[^>]*data-testid="palette-templates-shared"[\s\S]*?<\/ul>/,
    );

    expect(groupNames(html)).toEqual([
      'My library',
      'Shared with me',
      'Server-wide',
    ]);
    expect(shared).toContain('aria-label="Shared with me"');
    expect(buttonIds(shared)).toEqual([
      'palette-template-shared-bob smith-rtu',
    ]);
    // An id holds no space, and a user name may.
    expect(shared).toContain(
      'aria-describedby="palette-template-shared-bob%20smith-rtu-hint"',
    );
    expect(
      textOf(
        shared.match(
          /<span id="palette-template-shared-bob%20smith-rtu-hint"[^>]*>[\s\S]*?<\/span>/,
        )[0],
      ),
    ).toBe('Remote unit Shared by bob smith.');
    expect(
      textOf(
        element(html, 'palette-templates-server').match(
          /<span id="palette-template-server-carol-fw-hint"[^>]*>[\s\S]*?<\/span>/,
        )[0],
      ),
    ).toBe('Published by carol.');
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

  test('Add device offers the library’s templates by group, with the icons they name', () => {
    const templates = libraryOf({
      templates: [
        template('plc', {
          description: 'A controller',
          device: { ...template('plc').device, icon: 'plc-icon' },
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
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'plc',
        look: { iconKey: 'router', icon: 'plc-icon' },
      }),
    );
  });
});

describe('sharing on the Node Templates tab', () => {
  const shared = (init = {}) =>
    libraryOf({
      templates: [
        template('plc', {
          serverWide: true,
          shares: [
            { user: 'bob', stale: false },
            { user: 'carol', stale: true },
          ],
        }),
        template('hmi'),
        template('rtu', { owner: 'bob', source: 'shared' }),
        template('fw', { owner: 'c/d', source: 'server', serverWide: true }),
      ],
      collections: [
        { id: 'c1', name: 'Plant floor', templateIds: ['plc'] },
        {
          id: 'kit',
          owner: 'bob',
          source: 'shared',
          name: 'Kit',
          templateIds: ['rtu'],
        },
        {
          id: 'edge',
          owner: 'c/d',
          source: 'server',
          name: 'Edge',
          serverWide: true,
          templateIds: ['fw'],
        },
      ],
      ...init,
    });
  const tab = (library) =>
    render(BuilderTemplates, {}, (store) => {
      store.templates = library;
    });

  test('a role that may share has Share on each card and on the selection', async () => {
    const { html } = await tab({ ...shared(), canShare: true });
    const [card, next] = html.split('<li ').slice(1);

    expect(buttonIds(card)).toEqual([
      'template-edit-plc',
      'template-share-plc',
      'template-delete-plc',
      'template-export-plc',
    ]);

    const share = element(card, 'template-share-plc');

    expect(textOf(share)).toBe('Share');
    expect(share).toContain('aria-label="Share template PLC"');
    expect(share).toContain('aria-haspopup="dialog"');
    // Who has it, those whose account was removed left out, and that
    // everyone can use it.
    expect(textOf(element(card, 'template-shared-with-plc'))).toBe(
      'Shared with bob',
    );
    expect(textOf(element(card, 'template-server-wide-plc'))).toBe(
      'Server-wide',
    );
    expect(textOf(card.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'PLC Server-wide',
    );
    expect(next).not.toContain('template-shared-with-hmi');
    expect(next).not.toContain('template-server-wide-hmi');
    // Only other users' cards say whose they are.
    expect(card).not.toContain('template-owner-');

    expect(buttonIds(element(html, 'bulk-bar-templates'))).toEqual([
      'bulk-collect-templates',
      'bulk-share-templates',
      'bulk-delete-templates',
      'bulk-export-templates',
    ]);

    const bulk = element(html, 'bulk-share-templates');

    expect(textOf(bulk)).toBe('Share selected');
    expect(bulk).toContain('aria-disabled="true"');
    expect(bulk).toContain('aria-describedby="bulk-count-templates"');
  });

  test('Share is for a user who may share with people: not one who may only publish, as with sign-in off, nor a role that cannot change configs', async () => {
    const sharer = (await tab({ ...shared(), canShare: true })).html;

    expect(sharer).toContain('data-testid="template-share-plc"');
    expect(sharer).toContain('data-testid="bulk-share-templates"');

    // Without an account of their own, as with sign-in off, there is no
    // one to share with, and server-wide reaches no one else either.
    for (const flags of [{ canPublish: true }, {}]) {
      const { html } = await tab({ ...shared(), ...flags });

      expect(html).not.toContain('template-share-');
      expect(html).not.toContain('bulk-share-templates');
    }

    phenix.role = roleWith('list', 'create', 'delete');

    const unchanging = (
      await tab({ ...shared(), canShare: true, canPublish: true })
    ).html;

    expect(unchanging).not.toContain('template-share-');
    expect(unchanging).not.toContain('bulk-share-templates');
  });

  test('Show lists what other users share and publish, after the user’s own', async () => {
    const { html } = await tab(shared());
    const show = html.match(/<select[\s\S]*?<\/select>/)[0];

    expect(
      [
        ...show.matchAll(
          /<option[^>]*value="([^"]*)"[^>]*>([\s\S]*?)<\/option>/g,
        ),
      ].map(([, value, inner]) => [value, textOf(inner)]),
    ).toEqual([
      ['', 'My templates'],
      ['c1', 'Plant floor'],
      ['shared:', 'Shared with me'],
      ['shared:bob/kit', 'Kit (bob)'],
      ['server:', 'Server-wide'],
      ['server:c/d/edge', 'Edge (c/d)'],
    ]);
    expect(
      [...show.matchAll(/<optgroup label="([^"]+)"/g)].map(
        ([, label]) => label,
      ),
    ).toEqual([
      'My collections',
      'Shared collections',
      'Server-wide collections',
    ]);

    // The user's own list holds only their own templates.
    expect(
      [...html.matchAll(/data-testid="template-card-([^"]+)"/g)].map(
        ([, id]) => id,
      ),
    ).toEqual(['plc', 'hmi']);

    // Nothing of other users: neither choice.
    const alone = (
      await tab(libraryOf({ templates: [template('plc')] }))
    ).html.match(/<select[\s\S]*?<\/select>/)[0];

    expect(alone).not.toContain('Shared with me');
    expect(alone).not.toContain('Server-wide');
  });

  test('a library the server cannot read still offers what other users share', async () => {
    const { html } = await tab({
      ...libraryOf({
        templates: [template('rtu', { owner: 'bob', source: 'shared' })],
      }),
      damaged: true,
    });

    expect(textOf(element(html, 'templates-damaged'))).toBe(
      'Your library cannot be read. A newer version of phenix may have saved it.',
    );
    expect(element(html, 'templates-show')).toMatch(
      /<option value="shared:"[^>]*> Shared with me <\/option>/,
    );
    // The user's own list: no cards, and no word of an empty library.
    expect(html).not.toContain('templates-empty');
    expect(html).not.toContain('templates-list');
    expect(html).not.toContain('bulk-bar-templates');
  });
});

describe('the Share dialog of the template library', () => {
  const plc = template('plc', {
    shares: [
      { user: 'bob', stale: false },
      { user: 'carol', stale: true },
    ],
  });
  const dialog = (targets, init = {}) =>
    render(TemplateShareDialog, { targets }, (store) => {
      store.templates = { ...libraryOf({ templates: [plc] }), ...init };
    });

  test('one template: who has access, each with Remove, and whether it is server-wide', async () => {
    const { html } = await dialog(
      [{ kind: 'template', ...libraryOf({ templates: [plc] }).items[0] }],
      {
        canShare: true,
        canPublish: true,
      },
    );

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe('Share PLC');
    expect(
      textOf(
        element(html, 'template-share-dialog').match(
          /<p id="template-share-intro"[\s\S]*?<\/p>/,
        )[0],
      ),
    ).toBe(
      'People you add find this under Shared with me in their Node Templates and in Add nodes. They can use it and copy it, and they see your later changes. They cannot change it.',
    );
    expect(html).toContain('aria-describedby="template-share-intro"');
    expect(html).toMatch(/<label for="template-share-user"[^>]*>User<\/label>/);
    expect(element(html, 'template-share-user')).toContain('role="combobox"');
    expect(textOf(element(html, 'template-share-add'))).toBe('Add');
    expect(textOf(element(html, 'template-share-empty'))).toBe('No one yet.');

    const people = element(html, 'template-share-people');

    expect(people).toContain('aria-labelledby="template-share-people-title"');
    // The name, then what the person is, each a part of the row.
    const parts = (row) =>
      [
        ...row.matchAll(
          /<span[^>]*class="builder-template-share__[a-z]+"[^>]*>([\s\S]*?)<\/span>/g,
        ),
      ].map(([, inner]) => textOf(inner));

    expect(parts(people.split('<li ')[1])).toEqual(['alice (you)', 'Owner']);

    const bob = element(people, 'template-share-row-bob');
    const remove = element(bob, 'template-share-remove-bob');

    expect(textOf(remove)).toBe('Remove');
    expect(remove).toContain('aria-label="Remove bob"');
    expect(remove).not.toContain('aria-disabled');

    // A share whose account was removed starts marked for removal, and
    // cannot be kept.
    const carol = element(people, 'template-share-row-carol');

    expect(carol).toContain('is-removed');
    expect(parts(carol)).toEqual(['carol', 'Account removed']);
    expect(textOf(element(carol, 'template-share-remove-carol'))).toBe('Keep');
    expect(element(carol, 'template-share-remove-carol')).toContain(
      'aria-disabled="true"',
    );

    const server = element(html, 'template-share-server-wide');

    expect(server).toContain('type="checkbox"');
    expect(server).not.toContain('checked');
    expect(html).toContain(
      'Published server-wide: anyone who can use the Builder on this server can use it',
    );

    // Removing carol is a change, which Save sends; Cancel would not ask
    // about it.
    expect(textOf(element(html, 'template-share-changes'))).toBe(
      '1 change not saved',
    );
    expect(element(html, 'template-share-save')).not.toContain('aria-disabled');
    expect(textOf(element(html, 'template-share-cancel'))).toBe('Cancel');
  });

  test('several items: no list of people, and a choice of what to do server-wide', async () => {
    const { html } = await dialog(
      [
        { kind: 'template', ...plc, serverWide: true },
        { kind: 'template', ...template('hmi') },
      ],
      { canShare: true, canPublish: true },
    );

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Share 2 templates',
    );
    expect(html).not.toContain('template-share-people');
    expect(textOf(element(html, 'template-share-several'))).toBe(
      'Adds these people to every selected item. People who already have access keep it.',
    );

    const server = element(html, 'template-share-server-wide');

    expect(server).toMatch(/^<fieldset/);
    expect(textOf(server.match(/<legend[^>]*>[\s\S]*?<\/legend>/)[0])).toBe(
      'Server-wide',
    );
    expect(
      [...server.matchAll(/<label[^>]*>([\s\S]*?)<\/label>/g)].map(
        ([, inner]) => textOf(inner),
      ),
    ).toEqual([
      'Leave as it is',
      'Publish server-wide',
      'Remove from server-wide',
    ]);
    expect(element(server, 'template-share-server-wide-keep')).toContain(
      'checked',
    );
    expect(element(server, 'template-share-server-wide-publish')).toContain(
      'name="template-share-server-wide"',
    );
    // Nothing to save yet.
    expect(html).not.toContain('template-share-changes');
    expect(element(html, 'template-share-save')).toContain(
      'aria-disabled="true"',
    );
  });

  test('a role that may not publish is not offered server-wide', async () => {
    const { html } = await dialog(
      [{ kind: 'collection', id: 'c1', name: 'Plant floor', shares: [] }],
      { canShare: true, canPublish: false },
    );

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Share Plant floor',
    );
    expect(html).not.toContain('template-share-server-wide');
    expect(html).not.toContain('Server-wide');
  });
});

describe('another user’s template in the editor', () => {
  const rtu = template('rtu', {
    owner: 'bob',
    source: 'shared',
    description: 'Remote unit',
  });
  const view = (role = EVERYTHING) => {
    phenix.role = role;

    return render(
      TemplateDialog,
      { mode: 'view', template: rtu, icons: {} },
      (store) => {
        store.templates = libraryOf({ templates: [template('rtu'), rtu] });
      },
    );
  };

  test('it is read only, says whose it is, and offers Copy to my library', async () => {
    const { html } = await view();

    expect(textOf(html.match(/<h2[^>]*>[\s\S]*?<\/h2>/)[0])).toBe(
      'Template RTU',
    );
    expect(textOf(element(html, 'template-origin'))).toBe('Shared by bob');
    expect(html).toContain('aria-describedby="template-origin"');

    const name = element(html, 'template-name');

    expect(name).toContain('readonly');
    expect(name).not.toContain('required');
    expect(element(html, 'template-description')).toContain('readonly');
    expect(html).not.toMatch(/Name<span aria-hidden="true"> \*<\/span>/);
    // A name the user's own library has means nothing here.
    expect(textOf(element(html, 'template-name-hint'))).toBe('');
    // The node fields are locked.
    expect(html).toMatch(/value="rtu"[^>]*readonly|readonly[^>]*value="rtu"/);

    expect(textOf(element(html, 'template-cancel'))).toBe('Close');

    const copy = element(html, 'template-save');

    expect(textOf(copy)).toBe('Copy to my library');
    expect(copy).toContain('type="button"');
    expect(copy).not.toContain('form=');
  });

  test('a role that may not add templates can only look', async () => {
    const { html } = await view(roleWith('list', 'get'));

    expect(html).not.toContain('template-save');
    expect(textOf(element(html, 'template-cancel'))).toBe('Close');
  });
});
