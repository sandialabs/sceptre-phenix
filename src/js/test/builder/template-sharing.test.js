// Sharing the template library: the API client's calls, what the Node
// Templates tab lists of other users' templates, what the Share dialog
// checks, changes and says, and the store's actions.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

const api = vi.hoisted(() => ({
  listTemplates: vi.fn(),
  listTemplateShareCandidates: vi.fn(),
  shareTemplates: vi.fn(),
  publishTemplates: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import {
  TEMPLATE_CANDIDATES_PATH,
  createBuilderApi,
  readLibraryResult,
  templatePublishPath,
  templateSharePath,
} from '@/builder/api.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  SHOW_SERVER,
  SHOW_SHARED,
  collectionNames,
  copiedMessage,
  copyProblem,
  paletteTemplateGroups,
  reachedThroughCollection,
  shareRows,
  shareSubject,
  showChoices,
  shownList,
  templateOrigin,
  templateShareChange,
  templateShareFailures,
  templateShareReason,
  templateSharedMessage,
  unpublishQuestion,
  validateTemplateShareAdd,
} from '@/builder/templates.js';

import { libraryOf } from './fixtures.js';

// A template as the server lists it, of `owner`'s library.
function listed(id, init = {}) {
  return {
    id,
    owner: 'alice',
    source: 'own',
    name: id.toUpperCase(),
    device: { iconKey: 'server', spec: { general: { hostname: id } } },
    serverWide: false,
    collections: [],
    shares: [],
    ...init,
  };
}

// An HTTP client that records each request and answers with `responses`,
// by "<method> <url>".
function fakeHttp(responses = {}) {
  const calls = [];
  const handler =
    (method) =>
    (url, ...rest) => {
      calls.push(
        method === 'get' ? { method, url } : { method, url, body: rest[0] },
      );

      return Promise.resolve(responses[`${method} ${url}`] ?? { data: {} });
    };

  return { calls, get: handler('get'), post: handler('post') };
}

// A library with templates of its user's, and of bob's and of "c/d" (a
// user name with a slash), shared with the user or published server-wide.
function mixedLibrary() {
  return libraryOf({
    templates: [
      listed('plc'),
      listed('hmi'),
      listed('rtu', { owner: 'bob', source: 'shared' }),
      listed('plc', { owner: 'bob', source: 'shared', name: 'Bob PLC' }),
      listed('fw', { owner: 'c/d', source: 'server', serverWide: true }),
      listed('ids', { owner: 'c/d', source: 'server' }),
    ],
    collections: [
      { id: 'mine', name: 'Plant', templateIds: ['hmi', 'plc'] },
      {
        id: 'kit',
        owner: 'bob',
        source: 'shared',
        name: 'Kit',
        templateIds: ['plc', 'rtu', 'gone'],
      },
      {
        id: 'edge',
        owner: 'c/d',
        source: 'server',
        name: 'Edge',
        serverWide: true,
        templateIds: ['ids'],
      },
    ],
  });
}

describe('the sharing calls of the API client', () => {
  test('the routes match the documented backend contract', () => {
    expect(TEMPLATE_CANDIDATES_PATH).toBe('builder/templates/candidates');
    expect(templateSharePath('a b')).toBe('builder/templates/a%20b/share');
    expect(templatePublishPath('c/d')).toBe('builder/templates/c%2Fd/publish');
  });

  test('a share names the items and the people, and leaves out what is empty', async () => {
    const http = fakeHttp({
      'post builder/templates/alice/share': {
        data: {
          failed: [
            { kind: 'template', id: 'plc', reason: 'too-many' },
            { kind: 'collection', id: 'kit', reason: 'not-found' },
          ],
        },
      },
    });
    const client = createBuilderApi(http);

    const result = await client.shareTemplates('alice', {
      templates: ['plc'],
      collections: ['kit'],
      add: ['bob'],
    });

    expect(http.calls).toEqual([
      {
        method: 'post',
        url: 'builder/templates/alice/share',
        body: { templates: ['plc'], collections: ['kit'], add: ['bob'] },
      },
    ]);
    expect(result.failed).toEqual([
      { kind: 'template', id: 'plc', reason: 'too-many' },
      { kind: 'collection', id: 'kit', reason: 'not-found' },
    ]);

    await client.shareTemplates('alice', {
      collections: ['kit'],
      remove: ['carol'],
    });
    expect(http.calls[1].body).toEqual({
      collections: ['kit'],
      remove: ['carol'],
    });
  });

  test('a publication says whether the items are to be server-wide, always', async () => {
    const http = fakeHttp();
    const client = createBuilderApi(http);

    expect(
      await client.publishTemplates('bob', {
        templates: ['rtu'],
        serverWide: false,
      }),
    ).toEqual({ failed: [] });
    await client.publishTemplates('alice', {
      collections: ['kit'],
      serverWide: true,
    });

    expect(http.calls.map(({ url, body }) => ({ url, body }))).toEqual([
      {
        url: 'builder/templates/bob/publish',
        body: { templates: ['rtu'], serverWide: false },
      },
      {
        url: 'builder/templates/alice/publish',
        body: { collections: ['kit'], serverWide: true },
      },
    ]);
  });

  test('the users to share with are read as the draft dialog reads them', async () => {
    const http = fakeHttp({
      [`get ${TEMPLATE_CANDIDATES_PATH}`]: {
        data: {
          users: [
            { username: 'bob', name: ' Bob Stone ' },
            { username: '' },
            { name: 'no username' },
            { username: 'carol' },
          ],
        },
      },
    });

    expect(await createBuilderApi(http).listTemplateShareCandidates()).toEqual([
      { username: 'bob', name: 'Bob Stone' },
      { username: 'carol', name: '' },
    ]);
    await expect(
      createBuilderApi(fakeHttp()).listTemplateShareCandidates(),
    ).rejects.toThrow('unexpected user listing');
  });

  test('what a change left undone is read tolerantly', () => {
    expect(readLibraryResult({ data: {} })).toEqual({ failed: [] });
    expect(
      readLibraryResult({
        data: {
          failed: [
            { kind: 'collection', id: 'kit', reason: 'too-many' },
            { kind: 'odd', id: 'plc' },
            { id: '' },
            null,
          ],
        },
      }),
    ).toEqual({
      failed: [
        { kind: 'collection', id: 'kit', reason: 'too-many' },
        { kind: 'template', id: 'plc', reason: '' },
      ],
    });
  });
});

describe('what the Node Templates tab lists of other users', () => {
  test('Show offers the user’s collections, then what is shared, then what is server-wide', () => {
    expect(showChoices(mixedLibrary())).toEqual({
      own: [{ value: 'mine', label: 'Plant' }],
      shared: true,
      sharedCollections: [{ value: 'shared:bob/kit', label: 'Kit (bob)' }],
      server: true,
      serverCollections: [{ value: 'server:c/d/edge', label: 'Edge (c/d)' }],
    });

    // Nothing of other users: only the user's own collections.
    expect(
      showChoices(libraryOf({ templates: [listed('plc')] })),
    ).toMatchObject({
      own: [],
      shared: false,
      sharedCollections: [],
      server: false,
      serverCollections: [],
    });
    expect(showChoices(null).own).toEqual([]);
  });

  test('each value names a list: whose templates, which collection, in its order', () => {
    const library = mixedLibrary();
    const ids = (list) =>
      list.templates.map((template) => `${template.owner}/${template.id}`);

    const own = shownList(library, '');
    expect(own.source).toBe('own');
    expect(own.collection).toBeNull();
    expect(ids(own)).toEqual(['alice/plc', 'alice/hmi']);

    const plant = shownList(library, 'mine');
    expect(plant.collection.name).toBe('Plant');
    expect(ids(plant)).toEqual(['alice/hmi', 'alice/plc']);

    expect(shownList(library, SHOW_SHARED).source).toBe('shared');
    expect(ids(shownList(library, SHOW_SHARED))).toEqual([
      'bob/rtu',
      'bob/plc',
    ]);
    expect(ids(shownList(library, SHOW_SERVER))).toEqual(['c/d/fw', 'c/d/ids']);

    // Another user's collection holds their templates, never the user's
    // of the same id; one it lists that is not there is left out.
    const kit = shownList(library, 'shared:bob/kit');
    expect(kit.source).toBe('shared');
    expect(ids(kit)).toEqual(['bob/plc', 'bob/rtu']);
    // An owner with a slash in their name.
    expect(ids(shownList(library, 'server:c/d/edge'))).toEqual(['c/d/ids']);

    // A collection the library no longer lists names nothing.
    expect(shownList(library, 'gone')).toBeNull();
    expect(shownList(library, 'shared:bob/mine')).toBeNull();
    expect(shownList(library, 'server:bob/kit')).toBeNull();
  });

  test('a card names the collections of its own library that hold it', () => {
    const library = mixedLibrary();
    const [plc, , , bobPlc] = library.items;

    expect(collectionNames(library, plc)).toEqual(['Plant']);
    expect(collectionNames(library, bobPlc)).toEqual(['Kit']);
    expect(collectionNames(library, library.items[5])).toEqual(['Edge']);
    expect(collectionNames(library, library.items[4])).toEqual([]);
  });

  test('a library the server cannot read still lists other users’ templates', () => {
    const damaged = libraryOf({
      damaged: true,
      templates: [
        listed('rtu', { owner: 'bob', source: 'shared' }),
        listed('fw', { owner: 'c/d', source: 'server', serverWide: true }),
      ],
    });

    // The built-in templates stand in for the user's own, beside them.
    expect(
      paletteTemplateGroups(createDocument({ name: 'Plant' }), damaged).map(
        (group) => [group.id, group.entries.map((entry) => entry.key)],
      ),
    ).toEqual([
      ['shared', ['shared:bob/rtu']],
      ['server', ['server:c/d/fw']],
      ['builtin', BUILTIN_TEMPLATES.map(({ id }) => `builtin:${id}`)],
    ]);
    expect(showChoices(damaged)).toMatchObject({ shared: true, server: true });
    expect(shownList(damaged, '').templates).toEqual([]);
  });

  test('the editor says whose another user’s template is', () => {
    expect(
      templateOrigin(listed('a', { source: 'shared', owner: 'bob' })),
    ).toBe('Shared by bob');
    expect(templateOrigin(listed('a', { source: 'server', owner: 'c' }))).toBe(
      'Published by c',
    );
    expect(templateOrigin(listed('a'))).toBe('');
  });
});

describe('what the Share dialog checks, changes and says', () => {
  const plc = listed('plc', {
    shares: [
      { user: 'bob', stale: false },
      { user: 'carol', stale: true },
    ],
  });
  const one = [{ kind: 'template', ...plc }];
  const several = [
    { kind: 'template', ...listed('plc') },
    { kind: 'template', ...listed('hmi') },
  ];
  const check = (name, options) =>
    validateTemplateShareAdd(name, {
      targets: one,
      people: [],
      rows: shareRows(plc),
      owner: 'alice',
      knownUsers: ['bob', 'carol', 'dave', 'erin'],
      ...options,
    });

  test('the items are named, one by its name and several counted', () => {
    expect(shareSubject(one)).toBe('PLC');
    expect(shareSubject(several)).toBe('2 templates');
    expect(
      shareSubject([
        { kind: 'collection', name: 'A' },
        { kind: 'collection', name: 'B' },
      ]),
    ).toBe('2 collections');
    expect(shareSubject([...several, { kind: 'collection', name: 'A' }])).toBe(
      '3 items',
    );
  });

  test('a share whose account was removed starts marked for removal', () => {
    expect(shareRows(plc)).toEqual([
      { user: 'bob', stale: false, removed: false },
      { user: 'carol', stale: true, removed: true },
    ]);
    expect(shareRows({})).toEqual([]);
  });

  test('a person is checked before they are added', () => {
    expect(check('  ').error).toBe('Enter a username.');
    expect(check('alice').error).toBe('You own this template.');
    expect(check('alice', { targets: several }).error).toBe(
      'You own these templates.',
    );
    expect(check('dave', { people: ['dave'] }).error).toBe(
      'dave is already in the list.',
    );
    expect(check('bob')).toMatchObject({
      error: 'bob already has access.',
      row: { user: 'bob' },
    });
    expect(check('a/b').error).toBe('a/b is not a valid username.');
    expect(check('zed').error).toBe('No user named zed.');
    // Until the users are read, any valid name is taken.
    expect(check('zed', { knownUsers: null })).toEqual({
      user: 'zed',
      error: '',
    });
    // Someone whose account was removed is added again, for the account
    // the name has now.
    expect(check(' carol ')).toEqual({ user: 'carol', error: '' });
    expect(check('dave')).toEqual({ user: 'dave', error: '' });
  });

  test('someone marked for removal is kept rather than added', () => {
    const rows = shareRows(plc);

    rows[0].removed = true;
    expect(check('bob', { rows })).toEqual({
      user: 'bob',
      error: '',
      row: rows[0],
    });
  });

  test('no item passes the most people allowed', () => {
    // bob and erin make two.
    expect(check('dave', { people: ['erin'], max: 2 }).error).toBe(
      'PLC can be shared with at most 2 people.',
    );
    // A share marked for removal leaves a place.
    const rows = shareRows(plc);

    rows[0].removed = true;
    expect(check('dave', { people: ['erin'], rows, max: 2 }).error).toBe('');
    expect(
      check('dave', { targets: several, rows: [], people: ['erin'], max: 1 })
        .error,
    ).toBe('At most 1 people can be added at once.');
  });

  test('a save sends who is added and removed, and whether server-wide changes', () => {
    const rows = shareRows(plc);

    expect(
      templateShareChange({ targets: one, people: [], rows, serverWide: null }),
    ).toEqual({
      add: [],
      remove: ['carol'],
      publish: null,
      count: 1,
      asked: 0,
    });

    rows[0].removed = true;
    expect(
      templateShareChange({
        targets: one,
        people: ['dave'],
        rows,
        serverWide: true,
      }),
    ).toEqual({
      add: ['dave'],
      remove: ['bob', 'carol'],
      publish: true,
      count: 4,
      asked: 3,
    });

    // carol added again: her share is replaced, not removed.
    expect(
      templateShareChange({
        targets: one,
        people: ['carol'],
        rows: shareRows(plc),
        serverWide: false,
      }),
    ).toEqual({
      add: ['carol'],
      remove: [],
      publish: null,
      count: 1,
      asked: 1,
    });

    // Several items: server-wide changes whenever it is asked to.
    expect(
      templateShareChange({
        targets: several,
        people: [],
        rows: [],
        serverWide: false,
      }),
    ).toEqual({ add: [], remove: [], publish: false, count: 1, asked: 1 });
  });

  test('a template reached through a shared or server-wide collection', () => {
    const library = libraryOf({
      templates: [listed('plc'), listed('hmi')],
      collections: [
        { id: 'a', templateIds: ['plc'], shares: [{ user: 'bob' }] },
        { id: 'b', templateIds: ['hmi'], shares: [{ user: 'x', stale: true }] },
      ],
    });

    expect(reachedThroughCollection(library, library.items[0])).toBe(true);
    expect(reachedThroughCollection(library, library.items[1])).toBe(false);
    library.collections[1].serverWide = true;
    expect(reachedThroughCollection(library, library.items[1])).toBe(true);
    expect(
      reachedThroughCollection(library, { kind: 'collection', id: 'a' }),
    ).toBe(false);
  });

  test('what the page says once a share is saved', () => {
    const rows = shareRows(plc);
    const change = (init) => ({ add: [], remove: [], publish: null, ...init });

    expect(
      templateSharedMessage(one, change({ add: ['dave', 'erin'] }), { rows }),
    ).toBe('Shared PLC with dave and erin.');
    expect(
      templateSharedMessage(several, change({ add: ['dave'], publish: true })),
    ).toBe('Shared 2 templates with dave. Published 2 templates server-wide.');
    expect(templateSharedMessage(several, change({ publish: false }))).toBe(
      'Removed 2 templates from server-wide.',
    );

    rows[0].removed = true;
    // Nobody left, and not server-wide.
    expect(
      templateSharedMessage(one, change({ remove: ['bob', 'carol'] }), {
        rows,
      }),
    ).toBe('Only you can use PLC now.');
    expect(
      templateSharedMessage(
        [{ ...one[0], serverWide: true }],
        change({ publish: false }),
        { rows: [] },
      ),
    ).toBe('Removed PLC from server-wide. Only you can use PLC now.');
    // Still server-wide, or reached through a collection.
    expect(
      templateSharedMessage(
        [{ ...one[0], serverWide: true }],
        change({ remove: ['bob'] }),
        { rows },
      ),
    ).toBe('Stopped sharing PLC with bob.');
    expect(
      templateSharedMessage(one, change({ remove: ['bob'] }), {
        rows,
        reached: true,
      }),
    ).toBe('Stopped sharing PLC with bob.');
    // A share whose account was removed gave no one anything.
    expect(
      templateSharedMessage(one, change({ remove: ['carol'] }), {
        rows: shareRows(plc),
      }),
    ).toBe('Saved sharing for PLC.');
  });

  test('why items were left as they were, and why people were refused', () => {
    const full = {
      ...one[0],
      shares: Array.from({ length: 25 }, (_, i) => ({ user: `u${i}` })),
    };

    expect(
      templateShareFailures(
        [full, { kind: 'collection', id: 'kit', name: 'Kit' }],
        [
          { kind: 'template', id: 'plc', reason: 'too-many' },
          { kind: 'collection', id: 'kit', reason: 'too-many' },
          { kind: 'template', id: 'odd', reason: 'not-found' },
          { kind: 'collection', id: 'kit', reason: 'other' },
        ],
      ),
    ).toEqual([
      'PLC is already shared with 25 people, the most allowed.',
      'Adding these people would share Kit with more than 25 people, the most allowed.',
      'odd is no longer in your library.',
      'Kit could not be changed.',
    ]);

    expect(templateShareReason('owner', 'alice', one)).toBe(
      'You own this template.',
    );
    expect(templateShareReason('too-many', '', several, 25)).toBe(
      'At most 25 people can be added at once.',
    );
    expect(templateShareReason('unknown-user', 'zed', one)).toBe(
      'No user named zed. People must have signed in to phēnix at least once.',
    );
    expect(templateShareReason('duplicate', 'bob', one)).toBe(
      'bob is listed twice.',
    );
  });
});

describe('copying and taking back other users’ templates', () => {
  test('a library at its most templates takes no copy', () => {
    const library = libraryOf({
      templates: [listed('a'), listed('b'), listed('c', { source: 'shared' })],
    });

    library.limits.templates = 3;
    expect(copyProblem(library, 1)).toBe('');
    expect(copyProblem(library, 2)).toBe(
      'Your library holds 3 templates, the most it can. Delete some first.',
    );
  });

  test('what the page says once templates were copied', () => {
    expect(copiedMessage([listed('plc')])).toBe('Copied PLC to your library.');
    expect(copiedMessage([listed('a'), listed('b')])).toBe(
      'Copied 2 templates to your library.',
    );
    expect(copiedMessage([listed('a')], { name: 'Kit' })).toBe(
      'Copied collection Kit to your library.',
    );
  });

  test('taking items back from server-wide asks first', () => {
    expect(unpublishQuestion([listed('fw', { owner: 'bob' })])).toEqual({
      title: 'Remove FW from server-wide?',
      message:
        "It stays in bob's library. Other people can no longer use it unless it is shared with them.",
      confirmLabel: 'Remove',
    });
    expect(
      unpublishQuestion([listed('a'), listed('b', { owner: 'c' })]),
    ).toMatchObject({
      title: 'Remove 2 templates from server-wide?',
      message:
        "They stay in their owners' libraries. Other people can no longer use them unless they are shared with them.",
    });
  });
});

describe('sharing in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();

    for (const call of Object.values(api)) {
      call.mockReset();
    }

    api.listTemplates.mockResolvedValue({
      owner: 'alice',
      templates: [],
      collections: [],
      icons: {},
      canShare: true,
      canPublish: true,
      damaged: false,
      limits: {},
    });
  });

  test('a share names the user’s library, and the library is read again', async () => {
    api.shareTemplates.mockResolvedValue({ failed: [] });
    await store.fetchTemplates();
    api.listTemplates.mockClear();

    expect(
      await store.shareLibraryItems(
        { templates: ['plc'], collections: ['kit'] },
        { add: ['bob'], remove: ['carol'] },
      ),
    ).toEqual({ failed: [] });
    expect(api.shareTemplates).toHaveBeenCalledExactlyOnceWith('alice', {
      templates: ['plc'],
      collections: ['kit'],
      add: ['bob'],
      remove: ['carol'],
    });
    expect(api.listTemplates).toHaveBeenCalledTimes(1);
    expect(store.templates).toMatchObject({ canShare: true, canPublish: true });
  });

  test('items are published in the user’s library, or taken back in another user’s', async () => {
    api.publishTemplates.mockResolvedValue({
      failed: [{ kind: 'template', id: 'x', reason: 'not-found' }],
    });

    expect(
      (await store.publishLibraryItems({ templates: ['plc'] }, true)).failed,
    ).toHaveLength(1);
    await store.publishLibraryItems({ collections: ['kit'] }, false, 'bob');

    expect(api.publishTemplates.mock.calls).toEqual([
      ['alice', { templates: ['plc'], collections: [], serverWide: true }],
      ['bob', { templates: [], collections: ['kit'], serverWide: false }],
    ]);
    // Read once for the owner, and once after each change.
    expect(api.listTemplates).toHaveBeenCalledTimes(3);
  });

  test('a refused share rejects with the refusal, and reads nothing again', async () => {
    const refusal = Object.assign(new Error('HTTP 422'), {
      response: {
        status: 422,
        data: { errors: [{ user: 'zed', reason: 'unknown-user' }] },
      },
    });

    await store.fetchTemplates();
    api.listTemplates.mockClear();
    api.shareTemplates.mockRejectedValue(refusal);

    await expect(
      store.shareLibraryItems({ templates: ['plc'] }, { add: ['zed'] }),
    ).rejects.toBe(refusal);
    expect(api.listTemplates).not.toHaveBeenCalled();
  });

  test('the users to share with come from the library’s own route', async () => {
    api.listTemplateShareCandidates.mockResolvedValue([{ username: 'bob' }]);

    expect(await store.loadTemplateShareCandidates()).toEqual([
      { username: 'bob' },
    ]);
  });
});
