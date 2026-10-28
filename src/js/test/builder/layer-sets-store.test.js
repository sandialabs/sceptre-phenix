// The layer set library in the API client and the store: reads, the server's
// default set, changes, uploads, and a diagram's copy of a set.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const api = vi.hoisted(() => ({
  listLayerSets: vi.fn(),
  createLayerSet: vi.fn(),
  updateLayerSet: vi.fn(),
  deleteLayerSet: vi.fn(),
  setDefaultLayerSet: vi.fn(),
  listIcons: vi.fn(),
  uploadIcon: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import {
  DEFAULT_LAYER_SET_PATH,
  LAYER_SETS_PATH,
  createBuilderApi,
  layerSetPath,
  readLayerSet,
} from '@/builder/api.js';
import { endBuilderSession } from '@/builder/session.js';
import { LAYER_SET_FILE_SCHEMA_URI } from '@/builder/layerSetFile.js';
import {
  findNode,
  layerSetOptions,
  nodeLayer,
  setLayers,
  setNodeLayer,
} from '@/builder/model.js';
import { NOT_ATTEMPTED } from '@/builder/bulk.js';
import { BUILTIN_NOT_DELETED, LayerSetError } from '@/builder/layerSetStore.js';
import { useBuilderStore } from '@/builder/store.js';

import { memoryStorage, sampleDocument } from './fixtures.js';
import { ICON_DATA } from './png.js';

const PURDUE = readLayerSet({
  id: 'builtin:purdue',
  name: 'Purdue Model',
  description: 'The levels of the Purdue model.',
  iconKey: 'plc',
  matchConflict: 'lowest',
  layers: [
    { id: 1, label: 'L1 Basic control', iconKey: 'plc', color: '#db2777' },
    { id: 0, label: 'L0 Process', iconKey: 'transmission', color: '#dc2626' },
  ],
  builtin: true,
  version: 1,
  etag: '"1"',
});

const PLANT = readLayerSet({
  id: 'plant',
  name: 'Plant',
  matchConflict: 'highest',
  layerOutlines: true,
  layers: [
    { id: 3, label: 'Operations', matchers: { 'general.hostname': '^alpha' } },
    { id: 1, label: 'Control', color: '#15803d' },
  ],
  version: 2,
  etag: '"2"',
  createdBy: 'bob',
});

function library(sets = [PURDUE, PLANT], defaultId = null) {
  return { layerSets: sets, defaultId, defaultEtag: '"0"', damaged: 0 };
}

function fakeHttp(responses = {}) {
  const calls = [];
  const handler =
    (method) =>
    (url, ...rest) => {
      calls.push({ method, url, body: rest[0], config: rest[1] });

      const response = responses[`${method} ${url}`];

      return response instanceof Error
        ? Promise.reject(response)
        : Promise.resolve(response ?? { data: {}, headers: {} });
    };

  return {
    calls,
    get: handler('get'),
    post: handler('post'),
    put: handler('put'),
    delete: handler('delete'),
  };
}

function httpError(status, code) {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data: { code, message: 'refused' } },
  });
}

describe('the API client', () => {
  test('names the routes of the library', () => {
    expect(LAYER_SETS_PATH).toBe('builder/layersets');
    expect(DEFAULT_LAYER_SET_PATH).toBe('builder/layersets/default');
    expect(layerSetPath('builtin:purdue')).toBe(
      'builder/layersets/builtin%3Apurdue',
    );
  });

  test('reads the library, each set with fields of the right kind', async () => {
    const http = fakeHttp({
      [`get ${LAYER_SETS_PATH}`]: {
        data: {
          layerSets: [
            { ...PLANT, matchConflict: undefined, layers: null },
            { name: 'no id' },
          ],
          defaultId: 'plant',
          defaultEtag: '"9"',
          damaged: 1,
          limits: { layerSets: 200 },
        },
      },
    });
    const listed = await createBuilderApi(http).listLayerSets();

    expect(listed).toEqual({
      layerSets: [{ ...PLANT, matchConflict: 'lowest', layers: [] }],
      defaultId: 'plant',
      defaultEtag: '"9"',
      damaged: 1,
    });
    expect(readLayerSet(null)).toBeNull();
  });

  test('adds, replaces and deletes a set, with its ETag', async () => {
    const http = fakeHttp({
      [`post ${LAYER_SETS_PATH}`]: {
        data: { ...PLANT, etag: '' },
        headers: { etag: '"1"' },
      },
      [`put ${layerSetPath('plant')}`]: { data: { ...PLANT, version: 3 } },
    });
    const client = createBuilderApi(http);
    const content = { name: 'Plant', layers: PLANT.layers };

    expect((await client.createLayerSet(content)).etag).toBe('"1"');
    expect((await client.updateLayerSet('plant', content, '"2"')).version).toBe(
      3,
    );
    await client.deleteLayerSet('plant', '"3"');
    expect(http.calls.map((call) => `${call.method} ${call.url}`)).toEqual([
      `post ${LAYER_SETS_PATH}`,
      `put ${layerSetPath('plant')}`,
      `delete ${layerSetPath('plant')}`,
    ]);
    expect(http.calls[1].config).toEqual({ headers: { 'If-Match': '"2"' } });
    // A delete has no body: its second argument is the request's config.
    expect(http.calls[2].body).toEqual({ headers: { 'If-Match': '"3"' } });
  });

  test('chooses the default set, null for none, at the tag of the current choice', async () => {
    const http = fakeHttp({
      [`put ${DEFAULT_LAYER_SET_PATH}`]: { data: { id: 'plant', etag: '"5"' } },
    });
    const client = createBuilderApi(http);

    expect(await client.setDefaultLayerSet('plant', '"0"')).toEqual({
      defaultId: 'plant',
      defaultEtag: '"5"',
    });
    expect(http.calls.at(-1).config).toEqual({
      headers: { 'If-Match': '"0"' },
    });
    await client.setDefaultLayerSet('', '"5"');
    expect(http.calls.at(-1).body).toEqual({ id: null });
  });
});

describe('the layer set library in the store', () => {
  let store;
  let announced;

  beforeEach(() => {
    vi.resetAllMocks();
    setActivePinia(createPinia());
    store = useBuilderStore();
    store.setDocument(sampleDocument().doc);
    api.listLayerSets.mockResolvedValue(library());
    api.listIcons.mockResolvedValue({ icons: [] });
    announced = [];
    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        announced.push(args[0]);
      }
    });
  });

  test('is read once for what waits for it, and keeps a failed read’s lists', async () => {
    expect(store.layerSets.loaded).toBe(false);
    await store.layerSetsReady();
    await store.layerSetsReady();
    expect(api.listLayerSets).toHaveBeenCalledOnce();
    expect(store.layerSets).toMatchObject({
      status: 'ready',
      loaded: true,
      items: [PURDUE, PLANT],
      defaultId: null,
    });

    api.listLayerSets.mockRejectedValue(httpError(500));
    await store.fetchLayerSets();
    expect(store.layerSets.status).toBe('failed');
    expect(store.layerSets.items).toEqual([PURDUE, PLANT]);
  });

  test('a read the session ended before is dropped', async () => {
    let answer;

    api.listLayerSets.mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );

    const read = store.fetchLayerSets();

    await endBuilderSession({
      localStorage: memoryStorage(),
      sessionStorage: memoryStorage(),
      clearDatabase: async () => {},
    });
    answer(library());
    await read;
    expect(store.layerSets.items).toEqual([]);
  });

  test('adds, changes, duplicates and deletes sets, then reads the library again', async () => {
    await store.fetchLayerSets();
    api.createLayerSet.mockImplementation(async (content) => ({
      ...PLANT,
      ...content,
      id: 'new',
      version: 1,
    }));
    api.updateLayerSet.mockResolvedValue({ ...PLANT, version: 3 });

    const { set } = await store.createLayerSet({
      name: 'New',
      layers: [{ id: 2, label: 'Two', matchers: {} }],
    });

    expect(api.createLayerSet).toHaveBeenLastCalledWith({
      name: 'New',
      matchConflict: 'lowest',
      layers: [{ id: 2, label: 'Two' }],
    });
    expect(set.id).toBe('new');

    await store.saveLayerSet('plant', { ...PLANT, name: 'Plant 2' });
    expect(api.updateLayerSet).toHaveBeenLastCalledWith(
      'plant',
      expect.objectContaining({ name: 'Plant 2', matchConflict: 'highest' }),
      '"2"',
    );

    // An editor sends the ETag of the version it opened, not the one of a
    // later read of the library, so the server refuses a stale change.
    await store.saveLayerSet('plant', PLANT, '"1"');
    expect(api.updateLayerSet).toHaveBeenLastCalledWith(
      'plant',
      expect.any(Object),
      '"1"',
    );

    // A built-in set is duplicated to change it, under a new name.
    await expect(
      store.saveLayerSet('builtin:purdue', PURDUE),
    ).rejects.toBeInstanceOf(LayerSetError);
    await store.duplicateLayerSet('builtin:purdue');
    expect(api.createLayerSet).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'Purdue Model (2)', iconKey: 'plc' }),
    );

    await store.deleteLayerSet('plant');
    expect(api.deleteLayerSet).toHaveBeenCalledWith('plant', '"2"');
    expect(api.listLayerSets).toHaveBeenCalledTimes(6);
  });

  test('a delete or a default choice that the library changed under reads it again, and says so', async () => {
    await store.fetchLayerSets();
    api.deleteLayerSet.mockRejectedValue(httpError(412, 'layerset.stale'));
    api.setDefaultLayerSet.mockRejectedValue(httpError(412, 'layerset.stale'));

    const changed =
      'Someone changed it after the Builder read the list. The list now shows it as it is. Check it, and then try again.';

    for (const [action, sent] of [
      [() => store.deleteLayerSet('plant'), 'delete Plant'],
      [() => store.setDefaultLayerSet('plant'), 'make Plant the default'],
    ]) {
      const reads = api.listLayerSets.mock.calls.length;
      const refusal = await action().catch((error) => error);

      expect(refusal).toBeInstanceOf(LayerSetError);
      expect(store.describeLayerSetError(refusal, sent)).toBe(
        `Could not ${sent}. ${changed}`,
      );
      expect(api.listLayerSets).toHaveBeenCalledTimes(reads + 1);
    }

    expect(api.setDefaultLayerSet).toHaveBeenCalledWith('plant', '"0"');
    expect(store.layerSets.defaultId).toBeNull();

    // A set the library does not list is not sent.
    await expect(store.deleteLayerSet('gone')).rejects.toBeInstanceOf(
      LayerSetError,
    );
    expect(api.deleteLayerSet).toHaveBeenCalledTimes(1);
  });

  test('Delete selected deletes each set one at a time, reads the library once, and keeps a built-in set', async () => {
    const OFFICE = readLayerSet({
      id: 'office',
      name: 'Office',
      layers: [{ id: 1, label: 'Desk' }],
      version: 1,
      etag: '"1"',
    });

    api.listLayerSets.mockResolvedValue(library([PURDUE, PLANT, OFFICE]));
    await store.fetchLayerSets();
    api.deleteLayerSet
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(httpError(412, 'layerset.stale'));

    const progress = [];
    const reads = api.listLayerSets.mock.calls.length;
    const { done, failures } = await store.deleteLayerSets(
      [PURDUE, PLANT, OFFICE],
      { onProgress: (ended, total) => progress.push(`${ended}/${total}`) },
    );

    // The built-in set is not sent, and the others go one at a time.
    expect(api.deleteLayerSet.mock.calls).toEqual([
      ['plant', '"2"'],
      ['office', '"1"'],
    ]);
    expect(progress).toEqual(['1/2', '2/2']);
    expect(done).toEqual([PLANT]);
    expect(failures).toEqual([
      { item: PURDUE, reason: BUILTIN_NOT_DELETED },
      {
        item: OFFICE,
        reason:
          'Someone changed it after the Builder read the list. The list now shows it as it is. Check it, and then try again.',
      },
    ]);
    // Once after the 412, and once at the end.
    expect(api.listLayerSets).toHaveBeenCalledTimes(reads + 2);
  });

  test('Delete selected stops when the session ends, and lists the rest as not attempted', async () => {
    const OFFICE = readLayerSet({
      id: 'office',
      name: 'Office',
      layers: [{ id: 1, label: 'Desk' }],
      version: 1,
      etag: '"1"',
    });

    api.listLayerSets.mockResolvedValue(library([PURDUE, PLANT, OFFICE]));
    await store.fetchLayerSets();
    api.deleteLayerSet.mockRejectedValueOnce(httpError(401));

    const { done, failures } = await store.deleteLayerSets([PLANT, OFFICE]);

    expect(api.deleteLayerSet).toHaveBeenCalledOnce();
    expect(done).toEqual([]);
    expect(failures.map(({ item }) => item.id)).toEqual(['plant', 'office']);
    expect(failures[1].reason).toBe(NOT_ATTEMPTED);
  });

  test('says why a change failed', () => {
    expect(
      store.describeLayerSetError(
        httpError(412, 'layerset.stale'),
        'save the layer set',
      ),
    ).toMatch(/^Could not save the layer set\. Someone changed the layer set/);
    expect(
      store.describeLayerSetError(new LayerSetError('No.'), 'save it'),
    ).toBe('Could not save it. No.');
  });

  test('keeps the server’s default set, which new blank diagrams copy', async () => {
    await store.fetchLayerSets();
    api.setDefaultLayerSet.mockResolvedValue({
      defaultId: 'plant',
      defaultEtag: '"4"',
    });
    await store.setDefaultLayerSet('plant');
    expect(api.setDefaultLayerSet).toHaveBeenLastCalledWith('plant', '"0"');
    expect(store.defaultLayerSet).toEqual(PLANT);
    expect(store.layerSets.defaultEtag).toBe('"4"');

    const doc = store.newDocument({ name: 'Blank' });

    expect(doc.layers.map((layer) => layer.id)).toEqual([3, 1]);
    expect(layerSetOptions(doc).source).toEqual({ id: 'plant', version: 2 });
    // The copy is where the diagram starts, not an undo step.
    expect(store.canUndo).toBe(false);
    // Layers given take the place of the default.
    expect('layers' in store.newDocument({ name: 'Other', layers: [] })).toBe(
      false,
    );
    expect(store.defaultLayers().map((layer) => layer.id)).toEqual([3, 1]);

    api.setDefaultLayerSet.mockResolvedValue({
      defaultId: null,
      defaultEtag: '"0"',
    });
    await store.setDefaultLayerSet(null);
    expect(api.setDefaultLayerSet).toHaveBeenLastCalledWith(null, '"4"');
    expect(store.defaultLayerSet).toBeNull();
    expect('layers' in store.newDocument({ name: 'Blank' })).toBe(false);
    expect(store.defaultLayers()).toEqual([]);
  });

  test('uploads a layer set file as a new set, with its custom icons', async () => {
    await store.fetchLayerSets();
    api.createLayerSet.mockImplementation(async (content) => ({
      ...content,
      id: 'up',
      version: 1,
    }));
    api.uploadIcon.mockResolvedValue({
      icon: { name: 'ops', data: ICON_DATA },
      created: true,
    });

    const text = JSON.stringify({
      $schema: LAYER_SET_FILE_SCHEMA_URI,
      name: 'plant',
      layers: [{ id: 1, label: 'Control', icon: 'ops' }],
      icons: { ops: { data: ICON_DATA } },
    });
    const { set, warnings } = await store.uploadLayerSetFile(text);

    expect(warnings).toEqual([]);
    expect(api.uploadIcon).toHaveBeenCalledWith({
      name: 'ops',
      data: ICON_DATA,
    });
    expect(set.name).toBe('plant (2)');
    expect(api.createLayerSet).toHaveBeenLastCalledWith({
      name: 'plant (2)',
      matchConflict: 'lowest',
      layers: [{ id: 1, label: 'Control', icon: 'ops' }],
    });

    await expect(store.uploadLayerSetFile('{"name": 1}')).rejects.toThrow(
      /not a layer set file/,
    );

    try {
      await store.uploadLayerSetFile(
        JSON.stringify({ $schema: LAYER_SET_FILE_SCHEMA_URI, name: 'A' }),
      );
    } catch (error) {
      expect(error).toBeInstanceOf(LayerSetError);
      expect(error.issues.map((entry) => entry.code)).toEqual([
        'layerset.layers.empty',
      ]);
    }
  });
});

describe('the open diagram’s copy of a layer set', () => {
  let store;
  let sample;
  let announced;

  beforeEach(async () => {
    vi.resetAllMocks();
    setActivePinia(createPinia());
    store = useBuilderStore();
    sample = sampleDocument();
    store.setDocument(setNodeLayer(sample.doc, sample.bravo.id, 7));
    api.listLayerSets.mockResolvedValue(library());
    api.listIcons.mockResolvedValue({ icons: [] });
    await store.fetchLayerSets();
    announced = [];
    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        announced.push(args[0]);
      }
    });
  });

  test('is applied in one undo step, and nodes keep their chosen layers', () => {
    expect(store.layerSetLink.status).toBe('unlinked');
    expect(store.applyLayerSet('plant')).not.toBeNull();
    expect(announced.at(-1)).toBe('Applied layer set Plant');
    expect(store.layerSetLink.status).toBe('linked');
    expect(nodeLayer(findNode(store.doc, sample.bravo.id))).toBe(7);
    expect(store.applyLayerSet('plant')).toBeNull();
    expect(store.applyLayerSet('missing')).toBeNull();

    store.undo();
    expect('layers' in store.doc).toBe(false);
    expect('layerSet' in store.doc).toBe(false);
  });

  test('offers the update of a later version, in one undo step', async () => {
    store.applyLayerSet('plant');
    api.listLayerSets.mockResolvedValue(
      library([PURDUE, { ...PLANT, version: 5, layers: [PLANT.layers[1]] }]),
    );
    await store.fetchLayerSets();
    expect(store.layerSetLink.status).toBe('update');
    expect(store.updateFromLayerSet()).not.toBeNull();
    expect(announced.at(-1)).toBe(
      'Updated the layers from layer set Plant, version 5',
    );
    expect(store.doc.layers).toEqual([
      { id: 1, label: 'Control', color: '#15803d' },
    ]);
    expect(store.layerSetLink.status).toBe('linked');
  });

  test('a link reads as unknown until the library was read', () => {
    store.applyLayerSet('plant');
    store.layerSets = { ...store.layerSets, loaded: false };
    expect(store.layerSetLink.status).toBe('unknown');
  });

  test('changes of the copy are steps of the diagram, and show as changed', () => {
    store.applyLayerSet('plant');
    expect(
      store.updateLayer(1, { color: '#2f6fbf', iconKey: 'plc' }),
    ).not.toBeNull();
    expect(announced.at(-1)).toBe(
      'Changed the color and icon of layer Control',
    );
    expect(store.layerSetLink.status).toBe('changed');
    expect(store.updateLayer(1, { color: 'green' })).toBeNull();

    expect(store.setLayerSetOptions({ layerOutlines: false })).not.toBeNull();
    expect(announced.at(-1)).toBe('Turned off layer colors for node outlines');
    expect(
      store.setLayerSetOptions({ matchConflict: 'lowest' }),
    ).not.toBeNull();
    expect(announced.at(-1)).toBe('Matched nodes go to the lowest layer ID');
    expect(store.setLayerSetOptions({ matchConflict: 'lowest' })).toBeNull();

    // An update copies the set again, over the changes.
    store.updateFromLayerSet();
    expect(store.layerSetLink.status).toBe('linked');
  });

  test('is unlinked in one step, and keeps its layers', () => {
    store.applyLayerSet('plant');
    expect(store.unlinkLayerSet()).not.toBeNull();
    expect(announced.at(-1)).toBe('Removed the link to layer set Plant');
    expect(store.layerSetLink.status).toBe('unlinked');
    expect(store.doc.layers).toHaveLength(2);
    expect(store.unlinkLayerSet()).toBeNull();
  });

  test('saves to its set, which takes the next version, and links to it', async () => {
    store.applyLayerSet('plant');
    store.updateLayer(1, { label: 'Basic control' });
    let saved = PLANT;

    api.updateLayerSet.mockImplementation(async (_, content) => {
      saved = readLayerSet({ ...PLANT, ...content, version: 3, etag: '"3"' });

      return saved;
    });
    api.listLayerSets.mockImplementation(async () => library([PURDUE, saved]));

    const { set, warnings } = await store.saveToLayerSet();

    expect(warnings).toEqual([]);
    expect(api.updateLayerSet).toHaveBeenCalledWith(
      'plant',
      {
        name: 'Plant',
        matchConflict: 'highest',
        layerOutlines: true,
        layers: [
          PLANT.layers[0],
          { id: 1, label: 'Basic control', color: '#15803d' },
        ],
      },
      '"2"',
    );
    expect(set.version).toBe(3);
    expect(layerSetOptions(store.doc).source).toEqual({
      id: 'plant',
      version: 3,
    });
    expect(announced.at(-1)).toBe(
      'Saved the layers to layer set Plant, version 3',
    );
    expect(store.layerSetLink.status).toBe('linked');
  });

  test('does not save over a later version of its set', async () => {
    store.applyLayerSet('plant');
    store.updateLayer(1, { label: 'Basic control' });
    api.listLayerSets.mockResolvedValue(
      library([PURDUE, { ...PLANT, version: 5, etag: '"5"' }]),
    );
    await store.fetchLayerSets();

    await expect(store.saveToLayerSet()).rejects.toThrow(/newer version/);
    expect(api.updateLayerSet).not.toHaveBeenCalled();
  });

  test('cannot save to no set, or to a built-in set', async () => {
    await expect(store.saveToLayerSet()).rejects.toThrow(/not linked/);
    store.applyLayerSet('builtin:purdue');
    await expect(store.saveToLayerSet()).rejects.toThrow(/built-in/);
    expect(api.updateLayerSet).not.toHaveBeenCalled();
  });

  test('saves as a new set, and links to it', async () => {
    store.commit(
      setLayers(store.doc, [{ id: 2, label: 'Two', icon: 'ops' }]),
      'Layers',
    );
    store.commit({ ...store.doc, icons: { ops: { data: ICON_DATA } } }, 'Icon');
    api.uploadIcon.mockResolvedValue({
      icon: { name: 'ops', data: ICON_DATA },
      created: true,
    });
    api.createLayerSet.mockImplementation(async (content) => ({
      ...content,
      id: 'mine',
      version: 1,
      etag: '"1"',
    }));

    const { set } = await store.saveAsNewLayerSet({
      name: 'Mine',
      description: 'My layers',
    });

    expect(api.uploadIcon).toHaveBeenCalledWith({
      name: 'ops',
      data: ICON_DATA,
    });
    expect(api.createLayerSet).toHaveBeenCalledWith({
      name: 'Mine',
      description: 'My layers',
      matchConflict: 'lowest',
      layers: [{ id: 2, label: 'Two', icon: 'ops' }],
    });
    expect(set.id).toBe('mine');
    expect(announced.at(-1)).toBe(
      'Added layer set Mine, and linked the diagram to it',
    );
    expect(store.doc.layerSet).toEqual({
      name: 'Mine',
      source: { id: 'mine', version: 1 },
    });
  });

  test('a node goes back to the layer its fields match', () => {
    store.applyLayerSet('plant');
    store.setNodeLayer(sample.alpha.id, 1);
    expect(store.setNodeLayer(sample.alpha.id, null)).not.toBeNull();
    expect(announced.at(-1)).toBe(
      'Changed the layer of Device alpha to the matched layer Operations',
    );
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBeNull();
  });
});
