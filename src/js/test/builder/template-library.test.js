// The server's template library: how the palette groups its templates, what
// the Node Templates tab says, and the store's reads and changes of it.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

// The signed-in user: a test gives it a role to say what it may do.
const phenix = vi.hoisted(() => ({ username: 'alice', role: null }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

const api = vi.hoisted(() => ({
  listTemplates: vi.fn(),
  createTemplates: vi.fn(),
  updateTemplate: vi.fn(),
  createTemplateCollection: vi.fn(),
  updateTemplateCollection: vi.fn(),
  deleteTemplates: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import {
  TEMPLATES_PATH,
  TEMPLATE_COLLECTIONS_PATH,
  TEMPLATE_DELETE_PATH,
  TEMPLATE_ITEMS_PATH,
  createBuilderApi,
  libraryErrorMessage,
  templateCollectionPath,
  templateItemPath,
} from '@/builder/api.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { createDocument, findNode } from '@/builder/model.js';
import {
  LIBRARY_RETRY_MS,
  LibraryError,
  useBuilderStore,
} from '@/builder/store.js';
import {
  TEMPLATE_GROUP_LABELS,
  builtinTemplates,
  collectionDeleteQuestion,
  collectionProblem,
  diagramTemplateActions,
  membersMessage,
  paletteTemplateGroups,
  templateByKey,
  templateContent,
  templateKey,
  templatesDeleteQuestion,
  templatesDeletedMessage,
} from '@/builder/templates.js';
import { paletteNode } from '@/components/builder/paletteDnd.js';

import { libraryOf, sampleDocument } from './fixtures.js';

// A template as the server lists it.
function listed(id, init = {}) {
  return {
    id,
    source: 'library',
    name: id.toUpperCase(),
    description: '',
    device: {
      iconKey: 'server',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: `${id}.qc2` }] },
        network: { interfaces: [] },
      },
    },
    version: 1,
    etag: '"1"',
    collections: [],
    ...init,
  };
}

// The built-in templates as the server lists them.
const BUILTINS = BUILTIN_TEMPLATES.map((template) => ({
  ...template,
  source: 'builtin',
  version: 1,
}));

// The library as the API client answers a read of it.
function answer({ templates = [listed('plc')], ...rest } = {}) {
  return {
    templates,
    collections: [],
    builtins: BUILTINS,
    damaged: false,
    limits: {},
    ...rest,
  };
}

// A failure as axios rejects with one.
function failure(status, message = '', headers = {}) {
  return Object.assign(new Error(`HTTP ${status}`), {
    response: { status, data: message ? { message } : {}, headers },
  });
}

// An HTTP client that records each request and answers with `responses`,
// by "<method> <url>".
function fakeHttp(responses = {}) {
  const calls = [];
  const handler =
    (method) =>
    (url, ...rest) => {
      calls.push(
        method === 'get'
          ? { method, url }
          : { method, url, body: rest[0], config: rest[1] },
      );

      return Promise.resolve(responses[`${method} ${url}`] ?? { data: {} });
    };

  return {
    calls,
    get: handler('get'),
    post: handler('post'),
    put: handler('put'),
  };
}

describe('the library calls of the API client', () => {
  test('the routes match the documented backend contract', () => {
    expect(TEMPLATES_PATH).toBe('builder/templates');
    expect(TEMPLATE_ITEMS_PATH).toBe('builder/templates/items');
    expect(templateItemPath('plc')).toBe('builder/templates/items/plc');
    expect(TEMPLATE_COLLECTIONS_PATH).toBe('builder/templates/collections');
    expect(templateCollectionPath('c1')).toBe(
      'builder/templates/collections/c1',
    );
    expect(TEMPLATE_DELETE_PATH).toBe('builder/templates/delete');
    // Nothing of an id can name another route.
    expect(templateItemPath('../x y')).toBe(
      'builder/templates/items/..%2Fx%20y',
    );
    expect(templateCollectionPath('c/d')).toBe(
      'builder/templates/collections/c%2Fd',
    );
  });

  test('a read gives the library as listed, and lists where the server sent none', async () => {
    const data = {
      templates: [{ ...listed('plc'), source: 'library' }],
      collections: [{ id: 'c1', name: 'Plant', source: 'library' }],
      builtins: BUILTINS,
      preloaded: [],
      damaged: false,
      limits: { templates: 200 },
    };
    const http = fakeHttp({ 'get builder/templates': { data } });
    const library = {
      templates: data.templates,
      collections: data.collections,
      builtins: BUILTINS,
      damaged: false,
      limits: { templates: 200 },
    };

    expect(await createBuilderApi(http).listTemplates()).toEqual(library);
    // Icons are the icon library's: a library carries none.
    expect(
      await createBuilderApi(
        fakeHttp({
          'get builder/templates': { data: { ...data, icons: { a: {} } } },
        }),
      ).listTemplates(),
    ).toEqual(library);
    expect(http.calls).toEqual([{ method: 'get', url: 'builder/templates' }]);

    // Each list says where its items are kept, whatever the server said.
    expect(
      await createBuilderApi(
        fakeHttp({
          'get builder/templates': {
            data: {
              templates: [{ id: 'a', source: 'own' }],
              collections: [{ id: 'c' }],
              builtins: [{ id: 'router', source: 'library' }],
            },
          },
        }),
      ).listTemplates(),
    ).toMatchObject({
      templates: [{ id: 'a', source: 'library' }],
      collections: [{ id: 'c', source: 'library' }],
      builtins: [{ id: 'router', source: 'builtin' }],
    });

    for (const value of [
      undefined,
      {},
      {
        createdBy: 'alice',
        // Only what has an id is a template or a collection.
        templates: [null, {}, { id: '' }, { id: 7 }],
        collections: 'no',
        builtins: 'no',
        extra: true,
        damaged: 1,
        limits: null,
      },
    ]) {
      expect(
        await createBuilderApi(
          fakeHttp({ 'get builder/templates': { data: value } }),
        ).listTemplates(),
      ).toEqual({
        templates: [],
        collections: [],
        builtins: [],
        damaged: false,
        limits: {},
      });
    }

    expect(
      (
        await createBuilderApi(
          fakeHttp({ 'get builder/templates': { data: { damaged: true } } }),
        ).listTemplates()
      ).damaged,
    ).toBe(true);
  });

  test('templates are added with the collection that goes with them, when there is one', async () => {
    const created = { created: [{ id: 'n1', etag: '"1"' }] };
    const http = fakeHttp({
      'post builder/templates/items': { status: 201, data: created },
    });
    const client = createBuilderApi(http);
    const templates = [templateContent(listed('plc'))];

    expect(await client.createTemplates({ templates })).toEqual({
      created: created.created,
      collection: null,
    });
    // The server refuses a field it does not know, so none is sent empty.
    expect(http.calls[0].body).toEqual({ templates });

    await client.createTemplates({
      templates,
      collection: { name: 'Plant' },
    });
    expect(http.calls[1].body).toEqual({
      templates,
      collection: { name: 'Plant' },
    });

    await client.createTemplates({ templates, collection: null });
    expect(http.calls[2].body).toEqual({ templates });
    expect(
      await createBuilderApi(fakeHttp()).createTemplates({
        templates,
      }),
    ).toEqual({ created: [], collection: null });
  });

  test('a template and a collection are replaced with If-Match', async () => {
    const http = fakeHttp({
      'put builder/templates/items/plc': { data: { id: 'plc' } },
      'put builder/templates/collections/c1': { data: { id: 'c1' } },
      'post builder/templates/collections': { data: { id: 'c2' } },
    });
    const client = createBuilderApi(http);
    const content = templateContent(listed('plc'));

    expect(await client.updateTemplate('plc', content, '"3"')).toEqual({
      id: 'plc',
    });
    await client.updateTemplate('plc', content, '"4"');

    const collection = { name: 'Plant', description: '', templateIds: ['plc'] };

    expect(
      await client.updateTemplateCollection('c1', collection, '"2"'),
    ).toEqual({ id: 'c1' });
    expect(await client.createTemplateCollection(collection)).toEqual({
      id: 'c2',
    });

    expect(http.calls).toEqual([
      {
        method: 'put',
        url: 'builder/templates/items/plc',
        body: content,
        config: { headers: { 'If-Match': '"3"' } },
      },
      {
        method: 'put',
        url: 'builder/templates/items/plc',
        body: content,
        config: { headers: { 'If-Match': '"4"' } },
      },
      {
        method: 'put',
        url: 'builder/templates/collections/c1',
        body: collection,
        config: { headers: { 'If-Match': '"2"' } },
      },
      {
        method: 'post',
        url: 'builder/templates/collections',
        body: collection,
        config: undefined,
      },
    ]);
  });

  test('a delete names what goes, each at its tag, and says how many went', async () => {
    const http = fakeHttp({
      'post builder/templates/delete': {
        data: { deleted: { templates: 2, collections: 0 } },
      },
    });
    const client = createBuilderApi(http);
    const a = { id: 'a', etag: '"1"' };
    const b = { id: 'b', etag: '"4"' };
    const c1 = { id: 'c1', etag: '"2"' };

    expect(await client.deleteTemplates({ templates: [a, b] })).toEqual({
      templates: 2,
      collections: 0,
    });
    await client.deleteTemplates({ collections: [c1] });

    expect(http.calls.map((call) => call.body)).toEqual([
      { templates: [a, b] },
      { collections: [c1] },
    ]);
    expect(
      await createBuilderApi(fakeHttp()).deleteTemplates({ templates: [a] }),
    ).toEqual({ templates: 0, collections: 0 });
  });

  test('why a library request failed', () => {
    // The server's words for a refused change, without an id.
    expect(
      libraryErrorMessage(
        failure(
          404,
          'phenix cannot find template 0b5e6f0a-7c1d-4e2f-9a3b-5c6d7e8f9a0b',
        ),
      ),
    ).toBe('Phenix cannot find template.');
    expect(
      libraryErrorMessage(
        failure(409, 'this version of phenix cannot read this library'),
      ),
    ).toBe('This version of phenix cannot read this library.');
    // Never the words for a draft.
    expect(libraryErrorMessage(failure(409))).toBe(
      'The server rejected the request.',
    );
    expect(libraryErrorMessage(failure(403))).toBe(
      'Your role does not include the Builder.',
    );
    expect(
      libraryErrorMessage(failure(403, 'create not allowed for bob')),
    ).toBe('Create not allowed for bob.');
    // A session that ended is said as everywhere, whatever the server said.
    expect(libraryErrorMessage(failure(401, 'token expired'))).toBe(
      'Your session has ended. Log in again.',
    );
    expect(libraryErrorMessage(new Error('network'))).toBe(
      'The Builder could not reach the server. Check the connection and try again.',
    );
  });
});

describe('the palette’s groups, with a library', () => {
  const doc = () => ({
    ...createDocument(),
    templates: [{ ...listed('rtu'), id: 'd1', name: 'RTU' }],
  });
  const ids = (groups) => groups.map((group) => group.id);

  test('a library that was read is listed before the built-in templates', () => {
    const library = libraryOf({
      templates: [listed('server'), listed('plc', { description: 'Plant' })],
    });
    const groups = paletteTemplateGroups(createDocument(), library);

    expect(groups.map((group) => [group.id, group.label])).toEqual([
      ['library', 'Library'],
      ['builtin', 'Built-in'],
    ]);
    expect(groups[0].entries).toEqual([
      expect.objectContaining({
        key: 'library:server',
        source: 'library',
        id: 'server',
        testid: 'palette-template-server',
        name: 'SERVER',
        image: 'server.qc2',
        link: null,
      }),
      expect.objectContaining({
        key: 'library:plc',
        testid: 'palette-template-plc',
        description: 'Plant',
        template: library.items[1],
      }),
    ]);
    // A library template with a built-in template's id is one of each.
    expect(groups[1].entries[0]).toMatchObject({
      key: 'builtin:server',
      testid: 'palette-template-server-in-builtin',
    });
    expect(TEMPLATE_GROUP_LABELS).toEqual({
      diagram: 'This diagram',
      library: 'Library',
      other: 'Other library templates',
      builtin: 'Built-in',
    });
  });

  const collectionsLibrary = () =>
    libraryOf({
      templates: [
        listed('plc'),
        listed('hmi'),
        listed('rtu'),
        listed('p1', { source: 'preloaded' }),
      ],
      collections: [
        // Listed first by the server, but after the library's own.
        {
          id: 'example',
          source: 'preloaded',
          name: 'Example',
          templateIds: ['p1'],
        },
        { id: 'plant', name: 'Plant', templateIds: ['plc', 'hmi'] },
        { id: 'control', name: 'Control', templateIds: ['hmi', 'hmi'] },
        { id: 'empty', name: 'Empty', templateIds: [] },
        // A template the library does not hold is left out.
        { id: 'gone', name: 'Gone', templateIds: ['deleted'] },
        // A collection of the library holds none of the server's.
        { id: 'mixed', name: 'Mixed', templateIds: ['p1'] },
      ],
    });

  test('each collection is a section, and a template is in each collection that holds it', () => {
    const library = collectionsLibrary();
    const groups = paletteTemplateGroups(doc(), library);

    expect(
      groups.map((group) => [
        group.id,
        group.label,
        group.entries.map((entry) => entry.key),
      ]),
    ).toEqual([
      ['diagram', 'This diagram', ['diagram:d1']],
      // The templates in no collection.
      ['library', 'Other library templates', ['library:rtu']],
      ['library:plant', 'Plant', ['library:plc', 'library:hmi']],
      ['library:control', 'Control', ['library:hmi']],
      ['preloaded:example', 'Server: Example', ['preloaded:p1']],
      ['builtin', 'Built-in', BUILTIN_TEMPLATES.map((t) => `builtin:${t.id}`)],
    ]);
    expect(groups[4].entries[0]).toMatchObject({
      testid: 'palette-template-preloaded-p1',
      description: 'From the server collection Example, which is read only.',
    });
    // The first entry of a template has its test id, a further one names
    // its section, so no two are the same.
    const testIds = groups.flatMap((group) =>
      group.entries.map((entry) => entry.testid),
    );

    expect(groups[2].entries[1].testid).toBe('palette-template-hmi');
    expect(groups[3].entries[0].testid).toBe(
      'palette-template-hmi-in-library:control',
    );
    expect(new Set(testIds).size).toBe(testIds.length);
    // The same library gives the same sections.
    expect(paletteTemplateGroups(doc(), library)).toEqual(groups);
  });

  test('the built-in templates are always there, and the library only once it was read', () => {
    const unread = { ...libraryOf({ templates: [listed('plc')] }) };

    delete unread.loaded;

    // None was asked for: a store before its first read.
    expect(
      ids(paletteTemplateGroups(doc(), { ...unread, status: 'idle' })),
    ).toEqual(['diagram', 'builtin']);
    expect(ids(paletteTemplateGroups(doc(), null))).toEqual([
      'diagram',
      'builtin',
    ]);
    // Its first read is under way.
    expect(
      ids(paletteTemplateGroups(doc(), { ...unread, status: 'loading' })),
    ).toEqual(['diagram', 'builtin']);
    // An empty library is still the library.
    expect(ids(paletteTemplateGroups(doc(), libraryOf()))).toEqual([
      'diagram',
      'builtin',
    ]);
    // A later read that failed keeps what the earlier one answered.
    expect(
      ids(
        paletteTemplateGroups(doc(), {
          ...libraryOf({ templates: [listed('plc')] }),
          status: 'failed',
          error: 'No.',
        }),
      ),
    ).toEqual(['diagram', 'library', 'builtin']);
  });

  test('the built-in templates are the ones the server lists, once it was read', () => {
    const router = { ...BUILTINS[2], name: 'Edge router' };

    expect(builtinTemplates(null)).toEqual(BUILTINS);
    expect(builtinTemplates(libraryOf({ builtins: [router] }))).toEqual([
      router,
    ]);
    // A library that lists none, or that was not read, keeps them all.
    expect(builtinTemplates(libraryOf())).toEqual(BUILTINS);
    expect(
      builtinTemplates({ ...libraryOf({ builtins: [router] }), loaded: false }),
    ).toEqual(BUILTINS);
  });

  test('a key names a template by where it is kept', () => {
    const library = libraryOf({
      templates: [listed('plc'), listed('plc', { source: 'preloaded' })],
    });
    const [kept, preloaded] = library.items;

    expect(templateKey('library', 'plc')).toBe('library:plc');
    expect(templateKey('builtin', 'router')).toBe('builtin:router');
    expect(templateByKey(null, 'library:plc', library)).toBe(kept);
    expect(templateByKey(null, 'preloaded:plc', library)).toBe(preloaded);
    expect(templateByKey(null, 'builtin:router', library)).toEqual(BUILTINS[2]);

    for (const key of [
      'library:hmi',
      'own:plc',
      'other:bob/plc',
      'library:',
      'preloaded:hmi',
    ]) {
      expect(templateByKey(null, key, library), key).toBeUndefined();
    }

    // Without a library there is no template of one.
    expect(templateByKey(null, 'library:plc')).toBeUndefined();
    expect(templateByKey(null, 'library:plc', { items: 'no' })).toBeUndefined();
  });
});

describe('what goes with a template to another place', () => {
  test('its name, description and a copy of its device, and nothing else', () => {
    const template = listed('plc', { description: 'Plant' });
    const content = templateContent(template);

    expect(content).toEqual({
      name: 'PLC',
      description: 'Plant',
      device: template.device,
    });
    expect(content.device).not.toBe(template.device);
    expect(content.device.spec).not.toBe(template.device.spec);
    expect(templateContent(listed('plc'))).not.toHaveProperty('description');
  });
});

describe('the menu of a template of the diagram', () => {
  const labels = (link) =>
    diagramTemplateActions(link).map((item) => [item.id, item.label]);

  test('always offers Save to library', () => {
    expect(labels()).toEqual([
      ['edit', 'Edit'],
      ['library', 'Save to library'],
      ['delete', 'Delete'],
    ]);
  });

  test('offers Update for a copy with a later version, and Unlink for any copy', () => {
    expect(labels({ state: 'update' })).toEqual([
      ['edit', 'Edit'],
      ['update', 'Update'],
      ['library', 'Save to library'],
      ['unlink', 'Unlink'],
      ['delete', 'Delete'],
    ]);

    for (const state of ['current', 'deleted', 'unknown']) {
      expect(labels({ state }), state).toEqual([
        ['edit', 'Edit'],
        ['library', 'Save to library'],
        ['unlink', 'Unlink'],
        ['delete', 'Delete'],
      ]);
    }
  });
});

describe('what the Node Templates tab says', () => {
  test('what keeps a collection from being saved', () => {
    expect(collectionProblem({ name: 'Plant floor' })).toBeNull();
    expect(collectionProblem({ name: '' })).toEqual({
      field: 'name',
      message: 'Enter a name.',
    });
    expect(collectionProblem({ name: 'é'.repeat(64) })).toBeNull();
    expect(collectionProblem({ name: 'é'.repeat(65) })).toEqual({
      field: 'name',
      message: 'Use a shorter name (128 bytes at most).',
    });
    expect(
      collectionProblem({ name: 'a', description: 'd'.repeat(1024) }),
    ).toBeNull();
    expect(
      collectionProblem({ name: 'a', description: 'd'.repeat(1025) }),
    ).toEqual({
      field: 'description',
      message: 'Use a shorter description (1024 bytes at most).',
    });
  });

  test('what deleting templates does, for every user', () => {
    expect(templatesDeleteQuestion([listed('plc')])).toEqual({
      title: 'Delete template PLC?',
      message:
        'This removes the template from the library and its collections, for all users. Diagrams keep their copies of it. You cannot undo this.',
      confirmLabel: 'Delete template',
    });
    expect(templatesDeleteQuestion([listed('plc'), listed('hmi')])).toEqual({
      title: 'Delete 2 templates?',
      message:
        'This removes the templates from the library and its collections, for all users. Diagrams keep their copies of them. You cannot undo this.',
      confirmLabel: 'Delete templates',
    });
  });

  test('what deleting a collection does', () => {
    expect(collectionDeleteQuestion({ id: 'c1', name: 'Plant floor' })).toEqual(
      {
        title: 'Delete collection Plant floor?',
        message:
          'This removes the collection for all users. Its templates stay in the library.',
        confirmLabel: 'Delete collection',
      },
    );
  });

  test('what a change came to', () => {
    const [plc, hmi] = [listed('plc'), listed('hmi')];

    expect(templatesDeletedMessage([plc])).toBe('Deleted template PLC.');
    expect(templatesDeletedMessage([plc, hmi])).toBe('Deleted 2 templates.');
    expect(membersMessage('add', [plc], 'Plant')).toBe('Added PLC to Plant.');
    expect(membersMessage('add', [plc, hmi], 'Plant')).toBe(
      'Added 2 templates to Plant.',
    );
    expect(membersMessage('remove', [hmi], 'Plant')).toBe(
      'Removed HMI from Plant.',
    );
    expect(membersMessage('remove', [plc, hmi], 'Plant')).toBe(
      'Removed 2 templates from Plant.',
    );
  });
});

describe('the template library in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    phenix.role = null;

    for (const call of Object.values(api)) {
      call.mockReset();
    }

    api.listTemplates.mockResolvedValue(answer());
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  test('before it is read there is none, and the built-in templates are listed', () => {
    expect(store.templates).toMatchObject({
      status: 'idle',
      error: '',
      loaded: false,
      items: [],
      collections: [],
      builtins: [],
      damaged: false,
    });
    expect(store.templates.limits).toEqual({
      templates: 200,
      collections: 50,
      nameBytes: 128,
      descriptionBytes: 1024,
      deviceBytes: 16384,
    });
    expect(store.libraryTemplates).toEqual([]);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'builtin',
    ]);
  });

  test('a read keeps what the server answered, and the palette lists it', async () => {
    const collection = { id: 'c1', source: 'library', name: 'Plant' };
    const preloaded = {
      id: 'p',
      source: 'preloaded',
      name: 'Example',
      templateIds: ['p1'],
    };

    api.listTemplates.mockResolvedValue(
      answer({
        templates: [listed('plc'), listed('p1', { source: 'preloaded' })],
        collections: [collection, preloaded],
        limits: { templates: 3 },
      }),
    );

    const reading = store.fetchTemplates();

    expect(store.templates.status).toBe('loading');
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'builtin',
    ]);

    expect(await reading).toBe(store.templates);
    expect(store.templates).toMatchObject({
      status: 'ready',
      error: '',
      loaded: true,
      collections: [collection, preloaded],
      builtins: BUILTINS,
    });
    // What the server did not say keeps its default.
    expect(store.templates.limits).toMatchObject({
      templates: 3,
      collections: 50,
    });
    // The read-only templates and collections are not the library's.
    expect(store.libraryTemplates.map((template) => template.id)).toEqual([
      'plc',
    ]);
    expect(store.libraryCollections).toEqual([collection]);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'library',
      'preloaded:p',
      'builtin',
    ]);
    expect(store.templateByKey('library:plc')).toEqual(listed('plc'));
    expect(store.templateByKey('preloaded:p1').source).toBe('preloaded');
  });

  test('a read that fails says why, never in the page alert, and keeps what was read', async () => {
    api.listTemplates.mockRejectedValueOnce(failure(500, 'store is down'));
    await store.fetchTemplates();

    expect(store.templates).toMatchObject({
      status: 'failed',
      error: 'Store is down.',
      loaded: false,
      items: [],
    });
    expect(store.error).toBe('');
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'builtin',
    ]);

    await store.fetchTemplates();
    expect(store.templates).toMatchObject({ status: 'ready', error: '' });

    // A failure after that leaves the library as it was read.
    api.listTemplates.mockRejectedValueOnce(new Error('offline'));
    await store.fetchTemplates();

    expect(store.templates).toMatchObject({
      status: 'failed',
      error:
        'The Builder could not reach the server. Check the connection and try again.',
      loaded: true,
    });
    expect(store.libraryTemplates).toHaveLength(1);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'library',
      'builtin',
    ]);
  });

  test('a role that may not read the library is left with none', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockRejectedValueOnce(failure(403));
    await store.fetchTemplates();

    expect(store.templates).toMatchObject({
      status: 'failed',
      error: 'Your role does not include the Builder.',
      loaded: false,
      items: [],
    });
  });

  test('an answer that arrives after a later read’s is dropped', async () => {
    let first;
    let second;

    api.listTemplates
      .mockReturnValueOnce(new Promise((resolve) => (first = resolve)))
      .mockReturnValueOnce(new Promise((resolve) => (second = resolve)));

    const older = store.fetchTemplates();
    const newer = store.fetchTemplates();

    second(answer({ templates: [listed('new')] }));
    await newer;
    expect(store.templates.status).toBe('ready');

    first(answer({ templates: [listed('old')] }));
    await older;
    expect(store.libraryTemplates.map((template) => template.id)).toEqual([
      'new',
    ]);
  });

  test('an earlier answer is kept while a later read is under way, and its failure is said', async () => {
    let first;
    let second;

    api.listTemplates
      .mockReturnValueOnce(new Promise((resolve) => (first = resolve)))
      .mockReturnValueOnce(new Promise((_, reject) => (second = reject)));

    const older = store.fetchTemplates();
    const newer = store.fetchTemplates();

    first(answer());
    await older;
    expect(store.templates).toMatchObject({ status: 'loading', loaded: true });

    second(failure(500));
    await newer;
    expect(store.templates).toMatchObject({ status: 'failed', loaded: true });
    expect(store.libraryTemplates).toHaveLength(1);
  });

  test('the failure of an earlier read is not said once a later read has answered', async () => {
    let first;
    let second;

    api.listTemplates
      .mockReturnValueOnce(new Promise((_, reject) => (first = reject)))
      .mockReturnValueOnce(new Promise((resolve) => (second = resolve)));

    const older = store.fetchTemplates();
    const newer = store.fetchTemplates();

    second(answer());
    await newer;
    first(failure(500, 'store is down'));
    await older;

    expect(store.templates).toMatchObject({
      status: 'ready',
      error: '',
      loaded: true,
    });
  });

  test('an answer for a user who logged out meanwhile is dropped', async () => {
    let resolve;

    api.listTemplates.mockReturnValueOnce(
      new Promise((done) => (resolve = done)),
    );

    const reading = store.fetchTemplates();

    store.endSession();
    resolve(answer());
    await reading;

    expect(store.templates).toMatchObject({ status: 'idle', loaded: false });
    expect(store.libraryTemplates).toEqual([]);
  });

  test('templates are added as copies, and the library is read again', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.createTemplates.mockResolvedValue({
      created: [{ id: 'new', etag: '"1"' }],
      collection: null,
    });

    const template = listed('rtu', { description: 'Remote' });

    template.device.icon = 'rtu-icon';

    const result = await store.createLibraryTemplates([template]);

    expect(result.created).toEqual([{ id: 'new', etag: '"1"' }]);
    expect(api.createTemplates).toHaveBeenCalledExactlyOnceWith({
      // No id, source or tag: the server refuses a template with any. The
      // custom icon goes as the name it is.
      templates: [
        { name: 'RTU', description: 'Remote', device: template.device },
      ],
      collection: null,
    });
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
  });

  test('a change does not wait for a read of the library', async () => {
    api.createTemplates.mockResolvedValue({ created: [], collection: null });
    await store.createLibraryTemplates([listed('rtu')], {
      collection: { name: 'Plant' },
    });

    // Only the read after the change.
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
    expect(api.createTemplates.mock.calls[0][0].collection).toEqual({
      name: 'Plant',
    });
  });

  test('a failure is said in the server’s words', () => {
    expect(
      store.describeLibraryError(
        failure(413, 'a library holds at most 200 templates'),
        'save the template',
      ),
    ).toBe(
      'Could not save the template. A library holds at most 200 templates.',
    );
    expect(
      store.describeLibraryError(failure(401), 'delete the template'),
    ).toBe(
      'Could not delete the template. Your session has ended. Log in again.',
    );
    expect(store.describeLibraryError(failure(500), 'save the template')).toBe(
      'Could not save the template. The server rejected the request.',
    );
    expect(
      store.describeLibraryError(
        new LibraryError('This collection is no longer in the library.'),
        'add to collection Plant',
      ),
    ).toBe(
      'Could not add to collection Plant. This collection is no longer in the library.',
    );
  });

  test('a template is replaced against the tag it was listed with', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.updateTemplate.mockResolvedValue({ ...listed('plc'), etag: '"2"' });

    const content = { name: 'PLC 2', device: listed('plc').device };
    const saved = await store.updateLibraryTemplate('plc', content, '"1"');

    expect(saved.etag).toBe('"2"');
    // An emptied description is sent, which removes it.
    expect(api.updateTemplate).toHaveBeenCalledExactlyOnceWith(
      'plc',
      {
        name: 'PLC 2',
        description: '',
        device: content.device,
      },
      '"1"',
    );
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
  });

  test('a refused change rejects with the refusal, and reads nothing again', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.updateTemplate.mockRejectedValue(failure(412));

    const refused = await store
      .updateLibraryTemplate('plc', listed('plc'), '"0"')
      .catch((error) => error);

    expect(refused.response.status).toBe(412);
    expect(api.updateTemplate).toHaveBeenCalledTimes(1);
    expect(api.listTemplates).not.toHaveBeenCalled();
  });

  test('collections are added and replaced whole', async () => {
    await store.fetchTemplates();
    api.createTemplateCollection.mockResolvedValue({ id: 'c1', etag: '"1"' });
    api.updateTemplateCollection.mockResolvedValue({ id: 'c1', etag: '"2"' });

    expect(await store.createCollection({ name: 'Plant' })).toEqual({
      id: 'c1',
      etag: '"1"',
    });
    expect(api.createTemplateCollection).toHaveBeenCalledExactlyOnceWith({
      name: 'Plant',
      description: '',
      templateIds: [],
    });

    await store.updateCollection(
      'c1',
      // What else a listed collection holds is not sent.
      { id: 'c1', etag: '"1"', name: 'Floor', templateIds: ['plc'] },
      '"1"',
    );
    expect(api.updateTemplateCollection).toHaveBeenCalledExactlyOnceWith(
      'c1',
      { name: 'Floor', description: '', templateIds: ['plc'] },
      '"1"',
    );
    expect(api.listTemplates).toHaveBeenCalledTimes(3);
  });

  describe('the templates of a collection', () => {
    const collection = (templateIds, etag = '"1"') => ({
      id: 'c1',
      source: 'library',
      name: 'Plant',
      description: 'Floor',
      templateIds,
      etag,
    });

    beforeEach(async () => {
      api.listTemplates.mockResolvedValue(
        answer({ collections: [collection(['a', 'b'])] }),
      );
      await store.fetchTemplates();
      api.updateTemplateCollection.mockResolvedValue({});
    });

    test('templates join it after those it holds, and leave it', async () => {
      expect(
        await store.changeCollectionMembers('c1', { add: ['c', 'a', 'd'] }),
      ).toBe(true);
      expect(api.updateTemplateCollection).toHaveBeenLastCalledWith(
        'c1',
        {
          name: 'Plant',
          description: 'Floor',
          templateIds: ['a', 'b', 'c', 'd'],
        },
        '"1"',
      );

      expect(await store.changeCollectionMembers('c1', { remove: ['a'] })).toBe(
        true,
      );
      expect(
        api.updateTemplateCollection.mock.calls.at(-1)[1].templateIds,
      ).toEqual(['b']);
    });

    test('one that is already as asked is not sent', async () => {
      expect(await store.changeCollectionMembers('c1', { add: ['b'] })).toBe(
        false,
      );
      expect(await store.changeCollectionMembers('c1', { remove: ['z'] })).toBe(
        false,
      );
      expect(api.updateTemplateCollection).not.toHaveBeenCalled();
    });

    test('one that changed meanwhile is read again, and changed as it is now, once', async () => {
      api.updateTemplateCollection.mockRejectedValueOnce(failure(412));
      // Another tab took b out, and put x in.
      api.listTemplates.mockResolvedValue(
        answer({ collections: [collection(['a', 'x'], '"2"')] }),
      );

      expect(await store.changeCollectionMembers('c1', { add: ['c'] })).toBe(
        true,
      );
      expect(api.updateTemplateCollection).toHaveBeenCalledTimes(2);
      expect(api.updateTemplateCollection).toHaveBeenLastCalledWith(
        'c1',
        expect.objectContaining({ templateIds: ['a', 'x', 'c'] }),
        '"2"',
      );

      api.updateTemplateCollection.mockReset();
      api.updateTemplateCollection.mockRejectedValue(failure(412));

      const refused = await store
        .changeCollectionMembers('c1', { add: ['d'] })
        .catch((error) => error);

      expect(refused.response.status).toBe(412);
      expect(api.updateTemplateCollection).toHaveBeenCalledTimes(2);
    });

    test('any other refusal is not tried again', async () => {
      api.listTemplates.mockClear();
      api.updateTemplateCollection.mockRejectedValue(
        failure(413, 'a collection holds at most 200 templates'),
      );

      const refused = await store
        .changeCollectionMembers('c1', { add: ['c'] })
        .catch((error) => error);

      expect(refused.response.status).toBe(413);
      expect(api.updateTemplateCollection).toHaveBeenCalledTimes(1);
      expect(api.listTemplates).not.toHaveBeenCalled();
    });

    test('one that left the library cannot be changed', async () => {
      const refused = await store
        .changeCollectionMembers('gone', { add: ['a'] })
        .catch((error) => error);

      expect(refused).toBeInstanceOf(LibraryError);
      expect(refused.reason).toBe(
        'This collection is no longer in the library.',
      );
    });
  });

  test('templates and collections are deleted in one request, each at the tag the library has', async () => {
    api.listTemplates.mockResolvedValue(
      answer({
        templates: [listed('a'), listed('b', { version: 4, etag: '"4"' })],
        collections: [
          {
            id: 'c1',
            source: 'library',
            name: 'Spares',
            templateIds: [],
            version: 2,
            etag: '"2"',
          },
        ],
      }),
    );
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.deleteTemplates.mockResolvedValue({ templates: 2, collections: 1 });

    expect(
      await store.deleteLibraryItems({
        templates: ['a', 'b'],
        collections: ['c1'],
      }),
    ).toEqual({ templates: 2, collections: 1 });
    expect(api.deleteTemplates).toHaveBeenCalledExactlyOnceWith({
      templates: [
        { id: 'a', etag: '"1"' },
        { id: 'b', etag: '"4"' },
      ],
      collections: [{ id: 'c1', etag: '"2"' }],
    });
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
  });

  test('a delete that names an item the library no longer lists sends nothing, reads the library again and says why', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();

    for (const selection of [
      { templates: ['plc', 'gone'] },
      { templates: ['gone'] },
      { collections: ['gone'] },
    ]) {
      const refused = await store
        .deleteLibraryItems(selection)
        .catch((error) => error);

      expect(refused).toBeInstanceOf(LibraryError);
      expect(store.describeLibraryError(refused, 'delete the template')).toBe(
        'Could not delete the template. Someone changed the library after Builder read the list. The list now shows the library as it is. Check it, and then delete again.',
      );
    }

    expect(api.deleteTemplates).not.toHaveBeenCalled();
    expect(api.listTemplates).toHaveBeenCalledTimes(3);
  });

  test('a delete of an item someone changed after the library was read deletes nothing, reads the library again and says why', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.deleteTemplates.mockRejectedValue(
      failure(412, 'template plc changed after you read it'),
    );

    const refused = await store
      .deleteLibraryItems({ templates: ['plc'] })
      .catch((error) => error);

    expect(refused).toBeInstanceOf(LibraryError);
    expect(store.describeLibraryError(refused, 'delete the template')).toBe(
      'Could not delete the template. Someone changed the library after Builder read the list. The list now shows the library as it is. Check it, and then delete again.',
    );
    expect(api.deleteTemplates).toHaveBeenCalledOnce();
    expect(api.listTemplates).toHaveBeenCalledOnce();
  });

  test('a change the server was too busy for is sent again, a moment later, twice at most', async () => {
    await store.fetchTemplates();
    vi.useFakeTimers();
    api.deleteTemplates
      .mockRejectedValueOnce(failure(503))
      .mockRejectedValueOnce(failure(503))
      .mockResolvedValueOnce({ templates: 1, collections: 0 });

    const deleting = store.deleteLibraryItems({ templates: ['plc'] });

    await vi.advanceTimersByTimeAsync(LIBRARY_RETRY_MS - 1);
    expect(api.deleteTemplates).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(api.deleteTemplates).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(LIBRARY_RETRY_MS);
    expect(await deleting).toEqual({ templates: 1, collections: 0 });
    expect(api.deleteTemplates).toHaveBeenCalledTimes(3);

    api.deleteTemplates.mockReset();
    api.deleteTemplates.mockRejectedValue(failure(503, 'unable to delete'));

    const refused = store
      .deleteLibraryItems({ templates: ['plc'] })
      .catch((error) => error);

    await vi.advanceTimersByTimeAsync(LIBRARY_RETRY_MS * 2);
    expect((await refused).response.status).toBe(503);
    expect(api.deleteTemplates).toHaveBeenCalledTimes(3);
  });

  test('a device made from a template of the library names its custom icon', async () => {
    const template = listed('plc');

    template.device.icon = 'plc-icon';
    api.listTemplates.mockResolvedValue(answer({ templates: [template] }));
    await store.fetchTemplates();
    store.setDocument(sampleDocument().doc);

    const node = store.addNode(paletteNode(store, 'device', 'library:plc'));

    expect(findNode(store.doc, node.id).device).toMatchObject({
      hostname: 'plc',
      icon: 'plc-icon',
    });
    // The icon library resolves the name: the diagram carries no copy.
    expect(store.doc).not.toHaveProperty('icons');
  });
});
