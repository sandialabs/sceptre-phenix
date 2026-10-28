// The icon library calls of the API client, and the library the Builder
// keeps of the server's icons (iconLibrary.js).

import { describe, expect, test } from 'vitest';

import { createBuilderApi, iconPath } from '@/builder/api.js';
import {
  createIconLibrary,
  iconLibraryFailure,
  ingestIcons,
} from '@/builder/iconLibrary.js';
import { iconImageId, iconSrc } from '@/builder/icons.js';

import { ICON_DATA, ICON_KEY, base64Of, png } from './png.js';

function fakeHttp(responses = {}) {
  const calls = [];
  const handler = (method) => (url, body) => {
    // A read's second argument is its config: its headers are recorded.
    calls.push(
      method === 'get'
        ? { method, url, headers: body?.headers }
        : { method, url, body },
    );

    const response = responses[`${method} ${url}`];

    return response instanceof Error
      ? Promise.reject(response)
      : Promise.resolve(response ?? { status: 200, data: {} });
  };

  return {
    calls,
    get: handler('get'),
    post: handler('post'),
    put: handler('put'),
    delete: handler('delete'),
  };
}

function refusal(status, message, cause = '') {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data: { message, cause } },
  });
}

const stored = {
  name: 'plc',
  id: ICON_KEY,
  owner: 'alice',
  width: 1,
  height: 1,
  bytes: 70,
  created: '2026-10-02T12:34:07.786286Z',
  updated: '2026-10-02T12:34:07.786286Z',
  aliases: ['old-plc'],
  data: ICON_DATA,
  canRename: true,
  canDelete: false,
  revision: 3,
  etag: '"3"',
};

// The icon as the list gives it: without its image.
function listedOf(icon) {
  const row = { ...icon };

  delete row.data;

  return row;
}

const OTHER_DATA = base64Of(png(2, 2, [9, 9, 9, 255]));

describe('a failure in a batch of library requests', () => {
  test('ends the batch when the session ended or the server cannot be reached, as in a batch of drafts', () => {
    const library = createIconLibrary({});

    expect(library.ends(refusal(401, 'token expired'))).toBe(true);
    expect(library.ends(new Error('Network Error'))).toBe(true);
    // The other icons may still go: a missing, taken or refused icon, or a
    // failing server, ends nothing.
    expect(library.ends(refusal(404, 'icon plc not found'))).toBe(false);
    expect(library.ends(refusal(409, 'icon name "plc" is taken'))).toBe(false);
    expect(library.ends(refusal(500, 'storage down'))).toBe(false);
  });
});

describe('icon library routes', () => {
  test('match the documented backend contract', () => {
    // The server names an icon by its name or an alias.
    expect(iconPath('plc')).toBe('builder/icons/plc');
    expect(iconPath('a@b.c')).toBe('builder/icons/a%40b.c');
    // Nothing of a name can name another route.
    expect(iconPath('../drafts')).toBe('builder/icons/..%2Fdrafts');
  });

  test('the library is listed without images, with its limit and its tag', async () => {
    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: { icons: [listedOf(stored)], maxIcons: 2000, etag: '"L1"' },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      icons: [listedOf(stored)],
      maxIcons: 2000,
      etag: '"L1"',
    });
    expect(http.calls).toEqual([
      { method: 'get', url: 'builder/icons', headers: undefined },
    ]);
  });

  test('a list that is still the one of its tag is not sent again', async () => {
    const http = fakeHttp({ 'get builder/icons': { status: 304 } });

    await expect(createBuilderApi(http).listIcons('"L1"')).resolves.toEqual({
      notModified: true,
    });
    expect(http.calls).toEqual([
      {
        method: 'get',
        url: 'builder/icons',
        headers: { 'If-None-Match': '"L1"' },
      },
    ]);
  });

  test('one icon is read with its image', async () => {
    const http = fakeHttp({
      'get builder/icons/old-plc': { status: 200, data: stored },
    });

    await expect(createBuilderApi(http).getIcon('old-plc')).resolves.toEqual(
      stored,
    );
  });

  test('a listing of another shape is an empty library, and rows that are no icons are left out', async () => {
    const empty = { icons: [], maxIcons: 0, etag: '' };

    for (const data of [undefined, null, 'forbidden', { icons: 'none' }]) {
      const http = fakeHttp({ 'get builder/icons': { status: 200, data } });

      await expect(createBuilderApi(http).listIcons()).resolves.toEqual(empty);
    }

    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: {
          icons: [
            { ...listedOf(stored), aliases: ['old-plc', 7], canDelete: 'yes' },
            null,
            'icon',
            { name: 'plc' },
            { id: ICON_KEY },
            { name: 7, id: ICON_KEY },
            { name: 'bare', id: ICON_KEY },
          ],
          maxIcons: '64',
          etag: 7,
        },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      ...empty,
      icons: [
        listedOf(stored),
        {
          name: 'bare',
          id: ICON_KEY,
          aliases: [],
          canRename: false,
          canDelete: false,
        },
      ],
    });
  });

  test('an upload sends the name and the PNG, and answers with the stored icon', async () => {
    const http = fakeHttp({
      'post builder/icons': { status: 201, data: stored },
    });

    await expect(
      createBuilderApi(http).uploadIcon({ name: 'plc', data: ICON_DATA }),
    ).resolves.toEqual({ icon: stored, created: true });
    expect(http.calls[0]).toEqual({
      method: 'post',
      url: 'builder/icons',
      body: { name: 'plc', data: ICON_DATA },
    });
  });

  test('a name that already names the same bytes answers with that icon', async () => {
    const http = fakeHttp({
      'post builder/icons': { status: 200, data: stored },
    });

    await expect(
      createBuilderApi(http).uploadIcon({ name: 'PLC', data: ICON_DATA }),
    ).resolves.toEqual({ icon: stored, created: false });
  });

  test('an icon is renamed by its name, and deleted by its name at its tag', async () => {
    const http = fakeHttp({
      'put builder/icons/plc': {
        status: 200,
        data: { ...stored, name: 'plc-2' },
      },
    });
    const api = createBuilderApi(http);

    await expect(api.renameIcon('plc', 'plc-2')).resolves.toMatchObject({
      name: 'plc-2',
    });
    await expect(api.deleteIcon('old-plc', '"4"')).resolves.toBe(true);
    expect(http.calls).toEqual([
      { method: 'put', url: 'builder/icons/plc', body: { name: 'plc-2' } },
      {
        method: 'delete',
        url: 'builder/icons/old-plc',
        body: { headers: { 'If-Match': '"4"' } },
      },
    ]);
  });
});

// An API client whose library holds `icons`, each with its image, which
// records its calls. It lists the icons without their images, as the server
// does, and reads one icon with its image. `refuse` names the icons whose
// change, or image, it refuses with that error.
function fakeApi(icons = [], { refuse = {} } = {}) {
  const calls = [];
  const held = icons.map((icon) => ({ ...icon, id: iconImageId(icon.data) }));
  const named = (name) =>
    held.find((entry) =>
      [entry.name, ...(entry.aliases || [])].some(
        (each) => each.toLowerCase() === String(name).toLowerCase(),
      ),
    );

  return {
    calls,
    held,
    async listIcons() {
      calls.push('list');

      return {
        icons: held.map(listedOf),
        maxIcons: 2000,
        etag: `"${held.length}"`,
      };
    },
    async getIcon(name) {
      calls.push(['get', name]);

      if (refuse[name]) {
        throw refuse[name];
      }

      const icon = named(name);

      if (!icon) {
        throw refusal(404, 'phenix cannot find the icon');
      }

      return { ...icon };
    },
    async uploadIcon(icon) {
      calls.push(['upload', icon.name]);

      if (refuse[icon.name]) {
        throw refuse[icon.name];
      }

      const added = {
        ...icon,
        id: iconImageId(icon.data),
        aliases: [],
        canRename: true,
        canDelete: true,
      };

      held.push(added);

      return { icon: added, created: true };
    },
    async renameIcon(name, newName) {
      calls.push(['rename', name, newName]);

      const icon = named(name);

      icon.aliases = [...(icon.aliases || []), icon.name];
      icon.name = newName;

      return { ...icon };
    },
    async deleteIcon(name, etag) {
      calls.push(['delete', name, etag]);

      if (refuse[name]) {
        throw refuse[name];
      }

      held.splice(held.indexOf(named(name)), 1);

      return true;
    },
  };
}

describe('the library the Builder keeps of the server’s icons', () => {
  test('reads the list, and finds an icon by its name or an alias, ignoring case', async () => {
    const api = fakeApi([stored, { name: 'Alarm', data: OTHER_DATA }]);
    const library = createIconLibrary(api);

    expect(library.state.status).toBe('idle');
    expect(library.state.loaded).toBe(false);
    expect(library.lookup('plc')).toBeNull();

    await library.load();

    expect(library.state.status).toBe('ready');
    expect(library.state.loaded).toBe(true);
    expect(library.state.icons.map((icon) => icon.name)).toEqual([
      'Alarm',
      'plc',
    ]);
    // The list holds no image.
    expect(library.state.icons.some((icon) => 'data' in icon)).toBe(false);
    expect(library.state.maxIcons).toBe(2000);
    expect(library.lookup('OLD-plc').name).toBe('plc');
    expect(library.lookup('missing')).toBeNull();
    expect(library.lookup(undefined)).toBeNull();

    // A library read already is not read again for ensure().
    await library.ensure();
    expect(api.calls.filter((call) => call === 'list')).toHaveLength(1);
    expect(library.failure).toBe(iconLibraryFailure);
  });

  test('reads an image when an icon is looked up, once for each image, and draws an empty square until then', async () => {
    const api = fakeApi([
      stored,
      // Another icon of the same image.
      { name: 'plc-copy', data: ICON_DATA },
      { name: 'Alarm', data: OTHER_DATA },
    ]);
    const library = createIconLibrary(api);

    await library.load();

    const first = library.lookup('PLC');

    expect(first).toMatchObject({ name: 'plc', id: ICON_KEY, pending: true });
    expect(first.data).toBeUndefined();
    // The same object until the image comes.
    expect(library.lookup('plc')).toBe(first);
    expect(iconSrc('plc', null, library)).toMatch(/^data:image\/png;base64,/);
    expect(iconSrc('plc', null, library)).not.toContain(ICON_DATA);

    library.lookup('plc-copy');
    await library.ensureImages(['plc', 'plc-copy', 'alarm', 'none']);

    expect(library.lookup('plc').data).toBe(ICON_DATA);
    expect(library.lookup('plc').pending).toBeUndefined();
    expect(library.lookup('plc-copy').data).toBe(ICON_DATA);
    expect(library.lookup('alarm').data).toBe(OTHER_DATA);
    expect(iconSrc('plc', null, library)).toBe(
      `data:image/png;base64,${ICON_DATA}`,
    );
    // One read for each image, by the name of the icon that asked first.
    expect(api.calls.filter((call) => call[0] === 'get')).toEqual([
      ['get', 'plc'],
      ['get', 'Alarm'],
    ]);

    // An image read is kept: a lookup reads nothing more.
    library.lookup('plc');
    await library.ensureImages(['plc']);
    expect(api.calls.filter((call) => call[0] === 'get')).toHaveLength(2);
  });

  test('reads at most four images at once', async () => {
    const icons = Array.from({ length: 6 }, (_, index) => ({
      name: `icon-${index}`,
      data: base64Of(png(1, 1, [index, 0, 0, 255])),
    }));
    const api = fakeApi(icons);
    const library = createIconLibrary(api);
    const held = [];
    let reading = 0;
    let most = 0;

    api.getIcon = async (name) => {
      reading += 1;
      most = Math.max(most, reading);
      await new Promise((resolve) => held.push(resolve));
      reading -= 1;

      return { ...api.held.find((icon) => icon.name === name) };
    };

    await library.load();

    const all = library.ensureImages(icons.map((icon) => icon.name));

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(reading).toBe(4);

    while (held.length) {
      held.shift()();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }

    await all;
    expect(most).toBe(4);
    expect(
      icons.every((icon) => library.lookup(icon.name).data === icon.data),
    ).toBe(true);
  });

  test('an image that cannot be read draws the built-in icon, and is read again after the list is read again', async () => {
    const api = fakeApi([stored], {
      refuse: { plc: refusal(500, 'storage down') },
    });
    const library = createIconLibrary(api);

    await library.load();
    await library.ensureImages(['plc']);

    expect(library.lookup('plc').pending).toBeUndefined();
    expect(library.lookup('plc').data).toBeUndefined();
    expect(iconSrc('plc', null, library)).toBe('');

    // The image of another ID than the icon's is not kept.
    api.getIcon = async () => ({ ...stored, data: OTHER_DATA });
    await library.load();
    await library.ensureImages(['plc']);
    expect(library.lookup('plc').data).toBeUndefined();

    api.getIcon = async () => ({ ...stored });
    await library.load();
    await library.ensureImages(['plc']);
    expect(library.lookup('plc').data).toBe(ICON_DATA);
  });

  test('an image that cannot be read is read again after a list read that answers not modified', async () => {
    const api = fakeApi([stored], {
      refuse: { plc: refusal(500, 'storage down') },
    });
    const library = createIconLibrary(api);
    const list = api.listIcons;

    api.listIcons = async (etag) =>
      etag === '"1"' ? { notModified: true } : list();

    await library.load();
    await library.ensureImages(['plc']);
    expect(library.lookup('plc').data).toBeUndefined();

    api.getIcon = async () => ({ ...stored });
    await library.load();
    await library.ensureImages(['plc']);
    expect(library.lookup('plc').data).toBe(ICON_DATA);
  });

  test('a list that did not change is not read again, and keeps the icons', async () => {
    const api = fakeApi([stored]);
    const library = createIconLibrary(api);
    const tags = [];
    const list = api.listIcons;

    api.listIcons = async (etag) => {
      tags.push(etag);

      return etag === '"1"' ? { notModified: true } : list();
    };

    await library.load();
    await library.load();

    expect(tags).toEqual(['', '"1"']);
    expect(library.state.status).toBe('ready');
    expect(library.state.icons.map((icon) => icon.name)).toEqual(['plc']);
  });

  // The list tag is a hash of the server's list, so it can name an older
  // list again: another user deleted the icon that this one uploaded.
  test('reads the list in full after a change of its own', async () => {
    const api = fakeApi([stored]);
    const library = createIconLibrary(api);
    const tags = [];
    const list = api.listIcons;

    api.listIcons = async (etag) => {
      tags.push(etag);

      return etag === `"${api.held.length}"` ? { notModified: true } : list();
    };

    await library.load();
    await library.upload({ name: 'hmi', data: OTHER_DATA });
    api.held.pop();
    await library.load();

    expect(tags).toEqual(['', '']);
    expect(library.lookup('hmi')).toBeNull();

    await library.remove({ ...library.lookup('plc'), etag: '"7"' });
    await library.load();

    expect(tags).toEqual(['', '', '']);
  });

  test('takes an upload, a rename and a delete into the list from the answers, and reads it no more', async () => {
    const api = fakeApi([stored]);
    const library = createIconLibrary(api);

    await library.load();
    await library.upload({ name: 'hmi', data: OTHER_DATA });

    // The answer carries the image: it is drawn at once.
    expect(library.lookup('hmi').data).toBe(OTHER_DATA);
    expect(library.state.icons.some((icon) => 'data' in icon)).toBe(false);

    await library.rename('hmi', 'hmi-2');

    expect(library.lookup('hmi').name).toBe('hmi-2');
    expect(library.state.icons.map((icon) => icon.name)).toEqual([
      'hmi-2',
      'plc',
    ]);

    await library.remove({ ...library.lookup('hmi'), etag: '"7"' });

    expect(library.lookup('hmi')).toBeNull();
    expect(library.lookup('hmi-2')).toBeNull();
    expect(api.calls).toEqual([
      'list',
      ['upload', 'hmi'],
      ['rename', 'hmi', 'hmi-2'],
      ['delete', 'hmi-2', '"7"'],
    ]);
  });

  test('a delete of an icon that changed after the list was read reads the list again, and says why', async () => {
    const changed = refusal(412, 'icon plc changed after you last read it');
    const api = fakeApi([{ ...stored, etag: '"3"' }], {
      refuse: { plc: changed },
    });
    const library = createIconLibrary(api);

    await library.load();
    await expect(library.remove(library.state.icons[0])).rejects.toBe(changed);
    expect(api.calls).toEqual(['list', ['delete', 'plc', '"3"'], 'list']);
    expect(library.failure(changed)).toBe(
      'The icon changed after the Builder read the list. The list now shows it as it is. Check it, and then try again.',
    );
  });

  test('a delete of an icon that is gone already takes it out of the list', async () => {
    const gone = refusal(404, 'phenix cannot find the icon');
    const api = fakeApi([stored], { refuse: { plc: gone } });
    const library = createIconLibrary(api);

    await library.load();
    await expect(library.remove(library.state.icons[0])).rejects.toBe(gone);
    expect(library.lookup('plc')).toBeNull();
    expect(api.calls).toEqual(['list', ['delete', 'plc', '"3"']]);
  });

  test('a read that fails says why, and keeps the icons read before', async () => {
    const api = fakeApi([stored]);
    const library = createIconLibrary(api);

    await library.load();
    api.listIcons = async () => {
      throw new Error('Network Error');
    };

    await expect(library.load()).rejects.toThrow('Network Error');
    expect(library.state.status).toBe('failed');
    // The icons read before are still the server's list.
    expect(library.state.loaded).toBe(true);
    expect(library.state.error).toBe(
      'The Builder could not reach the server. Check the connection and try again.',
    );
    expect(library.lookup('plc').name).toBe('plc');
  });

  // The server's refusals of an icon are written to be shown.
  test.each([
    [422, 'icon is not a PNG image', 'Icon is not a PNG image.'],
    [
      422,
      'icon is 97 x 1 pixels: the limit is 96 x 96',
      'Icon is 97 x 1 pixels: the limit is 96 x 96.',
    ],
    [
      422,
      'icon library is full: it holds at most 2000 icons',
      'Icon library is full: it holds at most 2000 icons.',
    ],
    [
      409,
      'icon name "plc" belongs to an icon alice uploaded. Choose another name',
      'Icon name "plc" belongs to an icon alice uploaded. Choose another name.',
    ],
    [
      413,
      'icon is larger than 65536 bytes',
      'Icon is larger than 65536 bytes.',
    ],
    [400, 'icon data is not base64', 'Icon data is not base64.'],
    [404, 'phenix cannot find the icon', 'Phenix cannot find the icon.'],
  ])('a %i says what the server said: %s', (status, message, said) => {
    expect(
      iconLibraryFailure(
        refusal(status, message, 'builder: invalid request: icon: no'),
      ),
    ).toBe(said);
  });

  test('a session that ended and a server out of reach say what they say elsewhere', () => {
    expect(iconLibraryFailure(refusal(401, 'unauthorized'))).toBe(
      'Your session has ended. Log in again.',
    );
    expect(iconLibraryFailure(new Error('Network Error'))).toBe(
      'The Builder could not reach the server. Check the connection and try again.',
    );
  });

  test('a refusal that says nothing still says something', () => {
    expect(iconLibraryFailure(refusal(500, undefined))).toBe(
      'The server rejected the request.',
    );
    expect(
      iconLibraryFailure({ response: { status: 502, data: 'Bad Gateway' } }),
    ).toBe('The server rejected the request.');
  });
});

describe('an uploaded document’s icons go to the server', () => {
  const THIRD_DATA = base64Of(png(3, 1, [1, 2, 3, 255]));

  test('each of the four cases', async () => {
    const api = fakeApi([stored, { name: 'hmi', data: OTHER_DATA }], {
      refuse: { full: refusal(422, 'icon library is full: no') },
    });
    const library = createIconLibrary(api);
    const doc = {
      nodes: [],
      icons: {
        // The server has it as it is, under an alias: the copy goes.
        'OLD-PLC': { data: ICON_DATA },
        // The server lacks the name: it is uploaded, and the copy goes.
        valve: { data: THIRD_DATA },
        // The upload is refused: the copy stays, with a warning.
        full: { data: THIRD_DATA },
        // The server has the name with other bytes: the copy stays, and
        // wins in the draft, with a warning that names it.
        hmi: { data: THIRD_DATA },
      },
    };

    const { doc: ingested, warnings } = await ingestIcons(doc, library);

    expect(ingested.icons).toEqual({
      full: { data: THIRD_DATA },
      hmi: { data: THIRD_DATA },
    });
    expect(warnings).toEqual([
      "The Builder could not add custom icon full to the server's icon library: Icon library is full: no. The diagram keeps its own copy.",
      "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.",
    ]);
    // The copies are compared by their image IDs, and the list is not read
    // again after the uploads. A lookup reads the image it finds, for the
    // diagram that draws it.
    expect(api.calls.filter((call) => call[0] !== 'get')).toEqual([
      'list',
      ['upload', 'valve'],
      ['upload', 'full'],
    ]);
    expect(library.lookup('valve').data).toBe(THIRD_DATA);
    // The document given is not changed.
    expect(Object.keys(doc.icons)).toHaveLength(4);
  });

  test('a document without copies is the document it is, and nothing is read', async () => {
    const api = fakeApi([stored]);
    const doc = { nodes: [] };

    await expect(ingestIcons(doc, createIconLibrary(api))).resolves.toEqual({
      doc,
      warnings: [],
    });
    expect(api.calls).toEqual([]);
  });

  test('a library that cannot be read keeps every copy, and says so', async () => {
    const api = fakeApi();

    api.listIcons = async () => {
      throw refusal(
        403,
        'listing Builder icons: user bob does not have permission',
      );
    };

    const doc = { nodes: [], icons: { valve: { data: THIRD_DATA } } };
    const { doc: ingested, warnings } = await ingestIcons(
      doc,
      createIconLibrary(api),
    );

    expect(ingested).toBe(doc);
    expect(warnings).toEqual([
      "The Builder could not read the server's icon library. The diagram keeps its custom icon. Listing Builder icons: user bob does not have permission.",
    ]);
  });
});
