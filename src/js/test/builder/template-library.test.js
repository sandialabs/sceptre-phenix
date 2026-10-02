// The user's template library: how the palette groups its templates, what
// the Node Templates tab says, and the store's reads and changes of it.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

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
  createBuilderApi,
  libraryErrorMessage,
  templateCollectionPath,
  templateCollectionsPath,
  templateDeletePath,
  templateItemPath,
  templateItemsPath,
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
  collectionDeleteQuestion,
  collectionProblem,
  diagramTemplateActions,
  libraryUse,
  membersMessage,
  paletteTemplateGroups,
  templateByKey,
  templateContent,
  templateIcons,
  templateKey,
  templatesDeleteQuestion,
  templatesDeletedMessage,
} from '@/builder/templates.js';
import { paletteNode } from '@/components/builder/paletteDnd.js';

import { libraryOf, sampleDocument } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

const ICON = { name: 'plc', data: ICON_DATA };

// A template as the server lists it.
function listed(id, init = {}) {
  return {
    id,
    owner: 'alice',
    source: 'own',
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
    serverWide: false,
    collections: [],
    shares: [],
    ...init,
  };
}

// The library as the API client answers a read of it.
function answer({ templates = [listed('plc')], ...rest } = {}) {
  return {
    owner: 'alice',
    templates,
    collections: [],
    icons: {},
    canShare: false,
    canPublish: false,
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
    expect(templateItemsPath('alice')).toBe('builder/templates/alice/items');
    expect(templateItemPath('alice', 'plc')).toBe(
      'builder/templates/alice/items/plc',
    );
    expect(templateCollectionsPath('alice')).toBe(
      'builder/templates/alice/collections',
    );
    expect(templateCollectionPath('alice', 'c1')).toBe(
      'builder/templates/alice/collections/c1',
    );
    expect(templateDeletePath('alice')).toBe('builder/templates/alice/delete');
    // Nothing of a user name or an id can name another route.
    expect(templateItemPath('ops/alice', '../x y')).toBe(
      'builder/templates/ops%2Falice/items/..%2Fx%20y',
    );
    expect(templateCollectionPath('a b', 'c/d')).toBe(
      'builder/templates/a%20b/collections/c%2Fd',
    );
    expect(templateDeletePath('a/b')).toBe('builder/templates/a%2Fb/delete');
  });

  test('a read gives the library as listed, and lists where the server sent none', async () => {
    const library = answer({
      collections: [{ id: 'c1', name: 'Plant' }],
      icons: { [ICON_KEY]: ICON },
      canPublish: true,
      limits: { templates: 200 },
    });
    const http = fakeHttp({ 'get builder/templates': { data: library } });

    expect(await createBuilderApi(http).listTemplates()).toEqual(library);
    expect(http.calls).toEqual([{ method: 'get', url: 'builder/templates' }]);

    for (const data of [
      undefined,
      {},
      {
        owner: 7,
        // Only what has an id is a template or a collection.
        templates: [null, {}, { id: '' }, { id: 7 }],
        collections: 'no',
        icons: [],
        canShare: 'yes',
        damaged: 1,
        limits: null,
      },
    ]) {
      expect(
        await createBuilderApi(
          fakeHttp({ 'get builder/templates': { data } }),
        ).listTemplates(),
      ).toEqual({
        owner: '',
        templates: [],
        collections: [],
        icons: {},
        canShare: false,
        canPublish: false,
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

  test('templates are added with the collection and the icons that go with them, when there are some', async () => {
    const created = { created: [{ id: 'n1', etag: '"1"' }] };
    const http = fakeHttp({
      'post builder/templates/alice/items': { status: 201, data: created },
    });
    const client = createBuilderApi(http);
    const templates = [templateContent(listed('plc'))];

    expect(await client.createTemplates('alice', { templates })).toEqual({
      created: created.created,
      collection: null,
    });
    // The server refuses a field it does not know, so none is sent empty.
    expect(http.calls[0].body).toEqual({ templates });

    await client.createTemplates('alice', {
      templates,
      collection: { name: 'Plant' },
      icons: { [ICON_KEY]: ICON },
    });
    expect(http.calls[1].body).toEqual({
      templates,
      collection: { name: 'Plant' },
      icons: { [ICON_KEY]: ICON },
    });

    await client.createTemplates('alice', {
      templates,
      collection: null,
      icons: {},
    });
    expect(http.calls[2].body).toEqual({ templates });
    expect(
      await createBuilderApi(fakeHttp()).createTemplates('alice', {
        templates,
      }),
    ).toEqual({ created: [], collection: null });
  });

  test('a template and a collection are replaced with If-Match', async () => {
    const http = fakeHttp({
      'put builder/templates/alice/items/plc': { data: { id: 'plc' } },
      'put builder/templates/alice/collections/c1': { data: { id: 'c1' } },
      'post builder/templates/alice/collections': { data: { id: 'c2' } },
    });
    const client = createBuilderApi(http);
    const content = templateContent(listed('plc'));

    expect(
      await client.updateTemplate(
        'alice',
        'plc',
        { ...content, icons: { [ICON_KEY]: ICON } },
        '"3"',
      ),
    ).toEqual({ id: 'plc' });
    await client.updateTemplate(
      'alice',
      'plc',
      { ...content, icons: null },
      '"4"',
    );

    const collection = { name: 'Plant', description: '', templateIds: ['plc'] };

    expect(
      await client.updateTemplateCollection('alice', 'c1', collection, '"2"'),
    ).toEqual({ id: 'c1' });
    expect(await client.createTemplateCollection('alice', collection)).toEqual({
      id: 'c2',
    });

    expect(http.calls).toEqual([
      {
        method: 'put',
        url: 'builder/templates/alice/items/plc',
        body: { ...content, icons: { [ICON_KEY]: ICON } },
        config: { headers: { 'If-Match': '"3"' } },
      },
      {
        method: 'put',
        url: 'builder/templates/alice/items/plc',
        body: content,
        config: { headers: { 'If-Match': '"4"' } },
      },
      {
        method: 'put',
        url: 'builder/templates/alice/collections/c1',
        body: collection,
        config: { headers: { 'If-Match': '"2"' } },
      },
      {
        method: 'post',
        url: 'builder/templates/alice/collections',
        body: collection,
        config: undefined,
      },
    ]);
  });

  test('a delete names what goes, and says how many went', async () => {
    const http = fakeHttp({
      'post builder/templates/alice/delete': {
        data: { deleted: { templates: 2, collections: 0 } },
      },
    });
    const client = createBuilderApi(http);

    expect(
      await client.deleteTemplates('alice', { templates: ['a', 'b'] }),
    ).toEqual({ templates: 2, collections: 0 });
    await client.deleteTemplates('alice', { collections: ['c1'] });

    expect(http.calls.map((call) => call.body)).toEqual([
      { templates: ['a', 'b'] },
      { collections: ['c1'] },
    ]);
    expect(
      await createBuilderApi(fakeHttp()).deleteTemplates('alice', {
        templates: ['a'],
      }),
    ).toEqual({ templates: 0, collections: 0 });
  });

  test('why a library request failed', () => {
    // The server's words for a refused change, without an id.
    expect(
      libraryErrorMessage(
        failure(404, 'template 0b5e6f0a-7c1d-4e2f-9a3b-5c6d7e8f9a0b not found'),
      ),
    ).toBe('Template not found.');
    expect(
      libraryErrorMessage(
        failure(409, 'this library cannot be read by this version of phenix'),
      ),
    ).toBe('This library cannot be read by this version of phenix.');
    // Never the words for a draft.
    expect(libraryErrorMessage(failure(409))).toBe(
      'The server rejected the request.',
    );
    expect(libraryErrorMessage(failure(403))).toBe(
      'Your role does not allow it.',
    );
    expect(
      libraryErrorMessage(failure(403, 'create not allowed for bob')),
    ).toBe('Create not allowed for bob.');
    // A session that ended is said as everywhere, whatever the server said.
    expect(libraryErrorMessage(failure(401, 'token expired'))).toBe(
      'Your session has ended. Sign in again to continue.',
    );
    expect(libraryErrorMessage(new Error('network'))).toBe(
      'The server could not be reached. Check the connection and try again.',
    );
  });
});

describe('the palette’s groups, with a library', () => {
  const doc = () => ({
    ...createDocument(),
    templates: [{ ...listed('rtu'), id: 'd1', name: 'RTU' }],
  });

  test('a library that was read is listed in place of the built-in templates', () => {
    const library = libraryOf({
      templates: [listed('server'), listed('plc', { description: 'Plant' })],
    });
    const groups = paletteTemplateGroups(createDocument(), library);

    expect(groups.map((group) => [group.id, group.label])).toEqual([
      ['own', 'My library'],
    ]);
    expect(groups[0].entries).toEqual([
      expect.objectContaining({
        key: 'own:server',
        source: 'own',
        id: 'server',
        // The id the built-in entry always had.
        testid: 'palette-template-server',
        name: 'SERVER',
        image: 'server.qc2',
      }),
      expect.objectContaining({
        key: 'own:plc',
        testid: 'palette-template-plc',
        description: 'Plant',
        template: library.items[1],
      }),
    ]);
  });

  test('the groups come in order, each named, and a template of the library once', () => {
    const library = libraryOf({
      templates: [
        listed('fw', { owner: 'carol', source: 'server', name: 'FW' }),
        listed('hmi', {
          owner: 'bob',
          source: 'shared',
          description: 'Operator panel',
        }),
        listed('plc'),
        // Listed twice by a server that should not: shown once, as shared.
        listed('hmi', { owner: 'bob', source: 'server' }),
        listed('hmi', { owner: 'carol', source: 'server' }),
      ],
    });
    const groups = paletteTemplateGroups(doc(), library);

    expect(groups.map((group) => group.label)).toEqual([
      'This diagram',
      'My library',
      'Shared with me',
      'Server-wide',
    ]);
    expect(
      groups.map((group) => group.entries.map((entry) => entry.key)),
    ).toEqual([
      ['diagram:d1'],
      ['own:plc'],
      ['shared:bob/hmi'],
      ['server:carol/fw', 'server:carol/hmi'],
    ]);
    expect(groups[2].entries[0]).toMatchObject({
      testid: 'palette-template-shared-bob-hmi',
      description: 'Operator panel Shared by bob.',
    });
    expect(groups[3].entries[0]).toMatchObject({
      testid: 'palette-template-server-carol-fw',
      description: 'Published by carol.',
    });
    expect(Object.keys(TEMPLATE_GROUP_LABELS)).toEqual([
      'diagram',
      'own',
      'shared',
      'server',
      'builtin',
    ]);
  });

  test('the built-in templates stand in for a library that is not there, never while it is first read', () => {
    const ids = (library) =>
      paletteTemplateGroups(doc(), library).map((group) => group.id);
    const unread = { ...libraryOf(), loaded: false };

    // None was asked for: a store before its first read.
    expect(ids({ ...unread, status: 'idle' })).toEqual(['diagram', 'builtin']);
    expect(libraryUse({ ...unread, status: 'idle' })).toBe('missing');
    expect(libraryUse(null)).toBe('missing');
    // Its first read is under way: nothing stands in, so nothing flashes.
    expect(ids({ ...unread, status: 'loading' })).toEqual(['diagram']);
    expect(libraryUse({ ...unread, status: 'loading' })).toBe('pending');
    // It could not be read; and while Retry reads it again.
    expect(ids({ ...unread, status: 'failed', error: 'No.' })).toEqual([
      'diagram',
      'builtin',
    ]);
    expect(ids({ ...unread, status: 'loading', error: 'No.' })).toEqual([
      'diagram',
      'builtin',
    ]);
    // An empty library is still the library: a user may delete every one.
    expect(ids(libraryOf())).toEqual(['diagram']);
    expect(libraryUse(libraryOf())).toBe('ready');
    // One the server cannot read lists nothing: they stand in for it too.
    expect(ids(libraryOf({ damaged: true }))).toEqual(['diagram', 'builtin']);
    expect(libraryUse(libraryOf({ damaged: true }))).toBe('missing');
    // A later read that failed keeps what the earlier one answered.
    expect(
      ids({
        ...libraryOf({ templates: [listed('plc')] }),
        status: 'failed',
        error: 'No.',
      }),
    ).toEqual(['diagram', 'own']);
  });

  test('a key names a template of the library by its source, and its owner', () => {
    const library = libraryOf({
      templates: [
        listed('plc'),
        listed('plc', { owner: 'bob', source: 'shared' }),
        listed('hmi', { owner: 'ops/carol', source: 'server' }),
      ],
    });
    const [own, shared, server] = library.items;

    expect(templateKey('own', 'plc')).toBe('own:plc');
    expect(templateKey('shared', 'plc', 'bob')).toBe('shared:bob/plc');
    expect(templateKey('builtin', 'router', 'bob')).toBe('builtin:router');
    expect(templateByKey(null, 'own:plc', library)).toBe(own);
    expect(templateByKey(null, 'shared:bob/plc', library)).toBe(shared);
    // A user name may hold a slash; an id never does.
    expect(templateByKey(null, 'server:ops/carol/hmi', library)).toBe(server);
    expect(templateByKey(null, 'builtin:router', library)).toBe(
      BUILTIN_TEMPLATES[2],
    );

    for (const key of [
      'own:hmi',
      'shared:plc',
      'shared:carol/plc',
      'server:bob/plc',
      'server:ops/carol',
      'own:',
    ]) {
      expect(templateByKey(null, key, library), key).toBeUndefined();
    }

    // Without a library there is no template of one.
    expect(templateByKey(null, 'own:plc')).toBeUndefined();
    expect(templateByKey(null, 'own:plc', { items: 'no' })).toBeUndefined();
  });
});

describe('what goes with a template to another place', () => {
  test('the one icon it names, from the icons kept beside it', () => {
    const template = listed('plc');

    template.device.icon = ICON_KEY;

    expect(templateIcons(template, { [ICON_KEY]: ICON, other: ICON })).toEqual({
      [ICON_KEY]: ICON,
    });
    expect(templateIcons(template, { other: ICON })).toEqual({});
    expect(templateIcons(template, null)).toEqual({});
    expect(templateIcons(listed('plc'), { [ICON_KEY]: ICON })).toEqual({});
  });

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
  test('offers Save to library to a role that may add to its library', () => {
    const labels = (rights) =>
      diagramTemplateActions(rights).map((item) => [item.id, item.label]);

    expect(labels({ create: true })).toEqual([
      ['edit', 'Edit'],
      ['library', 'Save to library'],
      ['delete', 'Delete'],
    ]);
    expect(labels({ create: false, update: true, delete: true })).toEqual([
      ['edit', 'Edit'],
      ['delete', 'Delete'],
    ]);
    expect(labels(undefined)).toHaveLength(2);
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

  test('what deleting one template does, and who loses it', () => {
    expect(templatesDeleteQuestion([listed('plc')])).toEqual({
      title: 'Delete template PLC?',
      message:
        'It is removed from your library and its collections. Diagrams that used it are not changed. This cannot be undone.',
      confirmLabel: 'Delete template',
    });
    expect(
      templatesDeleteQuestion([
        listed('plc', {
          shares: [
            { user: 'bob', stale: false },
            { user: 'carol', stale: false },
            // An account that is gone has nothing to lose.
            { user: 'dave', stale: true },
          ],
        }),
      ]).message,
    ).toBe(
      'It is removed from your library and its collections. bob and carol will lose it too. Diagrams that used it are not changed. This cannot be undone.',
    );
  });

  test('what deleting several templates does', () => {
    expect(templatesDeleteQuestion([listed('plc'), listed('hmi')])).toEqual({
      title: 'Delete 2 templates?',
      message:
        'They are removed from your library and its collections. Diagrams that used them are not changed. This cannot be undone.',
      confirmLabel: 'Delete templates',
    });
    expect(
      templatesDeleteQuestion([
        listed('plc'),
        listed('hmi', { shares: [{ user: 'bob', stale: false }] }),
        listed('rtu'),
      ]),
    ).toMatchObject({
      title: 'Delete 3 templates?',
      message:
        'They are removed from your library and its collections. People you shared them with lose them too. Diagrams that used them are not changed. This cannot be undone.',
    });
  });

  test('what deleting a collection does', () => {
    const collection = { id: 'c1', name: 'Plant floor', shares: [] };

    expect(collectionDeleteQuestion(collection)).toEqual({
      title: 'Delete collection Plant floor?',
      message: 'The collection is removed. Its templates stay in your library.',
      confirmLabel: 'Delete collection',
    });

    for (const shared of [
      { shares: [{ user: 'bob', stale: false }] },
      { serverWide: true },
    ]) {
      expect(
        collectionDeleteQuestion({ ...collection, ...shared }).message,
      ).toBe(
        'The collection is removed. Its templates stay in your library. People who had them only through this collection lose them.',
      );
    }
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

  test('before it is read there is none, and the built-in templates stand in', () => {
    expect(store.templates).toMatchObject({
      status: 'idle',
      error: '',
      loaded: false,
      owner: '',
      items: [],
      collections: [],
      icons: {},
      damaged: false,
    });
    expect(store.templates.limits).toEqual({
      templates: 200,
      collections: 50,
      shares: 25,
      nameBytes: 128,
      descriptionBytes: 1024,
      deviceBytes: 16384,
      icons: 32,
    });
    expect(store.ownTemplates).toEqual([]);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'builtin',
    ]);
  });

  test('a read keeps what the server answered, and the palette lists it', async () => {
    const collection = { id: 'c1', source: 'own', name: 'Plant' };

    api.listTemplates.mockResolvedValue(
      answer({
        templates: [
          listed('plc'),
          listed('hmi', { owner: 'bob', source: 'shared' }),
        ],
        collections: [collection],
        icons: { [ICON_KEY]: ICON },
        limits: { templates: 3 },
      }),
    );

    const reading = store.fetchTemplates();

    expect(store.templates.status).toBe('loading');
    // Nothing stands in while it is first read.
    expect(store.paletteTemplateGroups).toEqual([]);

    expect(await reading).toBe(store.templates);
    expect(store.templates).toMatchObject({
      status: 'ready',
      error: '',
      loaded: true,
      owner: 'alice',
      collections: [collection],
      icons: { [ICON_KEY]: ICON },
    });
    // What the server did not say keeps its default.
    expect(store.templates.limits).toMatchObject({
      templates: 3,
      collections: 50,
    });
    expect(store.ownTemplates.map((template) => template.id)).toEqual(['plc']);
    expect(store.ownCollections).toEqual([collection]);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'own',
      'shared',
    ]);
    expect(store.templateByKey('own:plc')).toEqual(listed('plc'));
    expect(store.templateByKey('shared:bob/hmi').owner).toBe('bob');
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
        'The server could not be reached. Check the connection and try again.',
      loaded: true,
      owner: 'alice',
    });
    expect(store.ownTemplates).toHaveLength(1);
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'own',
    ]);
  });

  test('a role that may not read the library is left with none', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockRejectedValueOnce(failure(403));
    await store.fetchTemplates();

    expect(store.templates).toMatchObject({
      status: 'failed',
      error: 'Your role does not allow it.',
      loaded: false,
      owner: '',
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
    expect(store.ownTemplates.map((template) => template.id)).toEqual(['new']);
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
    expect(store.ownTemplates).toHaveLength(1);
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
    expect(store.ownTemplates).toEqual([]);
  });

  test('templates are added as copies, with their icons, and the library is read again', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.createTemplates.mockResolvedValue({
      created: [{ id: 'new', etag: '"1"' }],
      collection: null,
    });

    const template = listed('rtu', { description: 'Remote' });
    const result = await store.createLibraryTemplates([template], {
      icons: { [ICON_KEY]: ICON },
    });

    expect(result.created).toEqual([{ id: 'new', etag: '"1"' }]);
    expect(api.createTemplates).toHaveBeenCalledExactlyOnceWith('alice', {
      // No id, owner or tag: the server refuses a template with any.
      templates: [
        { name: 'RTU', description: 'Remote', device: template.device },
      ],
      icons: { [ICON_KEY]: ICON },
      collection: null,
    });
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
  });

  test('a library not read yet is read first, for the owner its changes name', async () => {
    api.createTemplates.mockResolvedValue({ created: [], collection: null });
    await store.createLibraryTemplates([listed('rtu')], {
      collection: { name: 'Plant' },
    });

    expect(api.listTemplates).toHaveBeenCalledTimes(2);
    expect(api.createTemplates.mock.calls[0][0]).toBe('alice');
    expect(api.createTemplates.mock.calls[0][1].collection).toEqual({
      name: 'Plant',
    });
  });

  test('a library that cannot be read takes no change, and says why', async () => {
    api.listTemplates.mockRejectedValue(failure(500, 'store is down'));

    const refused = await store
      .createLibraryTemplates([listed('rtu')])
      .catch((error) => error);

    expect(refused).toBeInstanceOf(LibraryError);
    expect(api.createTemplates).not.toHaveBeenCalled();
    expect(store.describeLibraryError(refused, 'save the template')).toBe(
      'Could not save the template. Store is down.',
    );
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
      'Could not delete the template. Your session has ended. Sign in again to continue.',
    );
    expect(store.describeLibraryError(failure(500), 'save the template')).toBe(
      'Could not save the template. The server rejected the request.',
    );
    expect(
      store.describeLibraryError(
        new LibraryError('This collection is no longer in your library.'),
        'add to collection Plant',
      ),
    ).toBe(
      'Could not add to collection Plant. This collection is no longer in your library.',
    );
  });

  test('a template is replaced against the tag it was listed with', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.updateTemplate.mockResolvedValue({ ...listed('plc'), etag: '"2"' });

    const content = { name: 'PLC 2', device: listed('plc').device };
    const saved = await store.updateLibraryTemplate('plc', content, '"1"', {
      [ICON_KEY]: ICON,
    });

    expect(saved.etag).toBe('"2"');
    // An emptied description is sent, which removes it.
    expect(api.updateTemplate).toHaveBeenCalledExactlyOnceWith(
      'alice',
      'plc',
      {
        name: 'PLC 2',
        description: '',
        device: content.device,
        icons: { [ICON_KEY]: ICON },
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
    expect(api.createTemplateCollection).toHaveBeenCalledExactlyOnceWith(
      'alice',
      { name: 'Plant', description: '', templateIds: [] },
    );

    await store.updateCollection(
      'c1',
      // What else a listed collection holds is not sent.
      { id: 'c1', etag: '"1"', name: 'Floor', templateIds: ['plc'] },
      '"1"',
    );
    expect(api.updateTemplateCollection).toHaveBeenCalledExactlyOnceWith(
      'alice',
      'c1',
      { name: 'Floor', description: '', templateIds: ['plc'] },
      '"1"',
    );
    expect(api.listTemplates).toHaveBeenCalledTimes(3);
  });

  describe('the templates of a collection', () => {
    const collection = (templateIds, etag = '"1"') => ({
      id: 'c1',
      owner: 'alice',
      source: 'own',
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
        'alice',
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
        api.updateTemplateCollection.mock.calls.at(-1)[2].templateIds,
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
        'alice',
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
        'This collection is no longer in your library.',
      );
    });
  });

  test('templates and collections are deleted in one request', async () => {
    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.deleteTemplates.mockResolvedValue({ templates: 2, collections: 1 });

    expect(
      await store.deleteLibraryItems({
        templates: ['a', 'b'],
        collections: ['c1'],
      }),
    ).toEqual({ templates: 2, collections: 1 });
    expect(api.deleteTemplates).toHaveBeenCalledExactlyOnceWith('alice', {
      templates: ['a', 'b'],
      collections: ['c1'],
    });
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
  });

  test('a change the server was too busy for is sent again, a moment later, twice at most', async () => {
    await store.fetchTemplates();
    vi.useFakeTimers();
    api.deleteTemplates
      .mockRejectedValueOnce(failure(503))
      .mockRejectedValueOnce(failure(503))
      .mockResolvedValueOnce({ templates: 1, collections: 0 });

    const deleting = store.deleteLibraryItems({ templates: ['a'] });

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
      .deleteLibraryItems({ templates: ['a'] })
      .catch((error) => error);

    await vi.advanceTimersByTimeAsync(LIBRARY_RETRY_MS * 2);
    expect((await refused).response.status).toBe(503);
    expect(api.deleteTemplates).toHaveBeenCalledTimes(3);
  });

  test('what the role may do to its library', () => {
    expect(store.templateRights).toEqual({
      create: false,
      update: false,
      delete: false,
    });

    phenix.role = {
      name: 'template-maker',
      policies: [
        { resources: ['configs'], resourceNames: ['*'], verbs: ['create'] },
      ],
    };
    // A getter with no state to follow: read from a store of its own.
    setActivePinia(createPinia());
    expect(useBuilderStore().templateRights).toEqual({
      create: true,
      update: false,
      delete: false,
    });
  });

  test('a device made from a template of the library takes its custom icon along', async () => {
    const template = listed('plc');

    template.device.icon = ICON_KEY;
    api.listTemplates.mockResolvedValue(
      answer({ templates: [template], icons: { [ICON_KEY]: ICON } }),
    );
    await store.fetchTemplates();
    store.setDocument(sampleDocument().doc);

    const node = store.addNode(paletteNode(store, 'device', 'own:plc'));

    expect(findNode(store.doc, node.id).device).toMatchObject({
      hostname: 'plc',
      icon: ICON_KEY,
    });
    // The diagram has its own copy now, from the library's.
    expect(store.doc.icons).toEqual({ [ICON_KEY]: ICON });
  });
});
