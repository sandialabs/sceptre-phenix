// The icon library calls of the API client, and the library the Builder
// keeps of the server's icons (iconLibrary.js).

import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import { createBuilderApi, ICONS_PATH, iconPath } from '@/builder/api.js';
import {
  createIconLibrary,
  iconLibraryFailure,
  ingestIcons,
  nameTaken,
} from '@/builder/iconLibrary.js';

import { ICON_DATA, ICON_KEY, base64Of, png } from './png.js';

function fakeHttp(responses = {}) {
  const calls = [];
  const handler = (method) => (url, body) => {
    calls.push({ method, url, body });

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
};

const OTHER_DATA = base64Of(png(2, 2, [9, 9, 9, 255]));

describe('icon library routes', () => {
  test('match the documented backend contract', () => {
    expect(ICONS_PATH).toBe('builder/icons');
    // The server names an icon by its name or an alias.
    expect(iconPath('plc')).toBe('builder/icons/plc');
    expect(iconPath('a@b.c')).toBe('builder/icons/a%40b.c');
    // Nothing of a name can name another route.
    expect(iconPath('../drafts')).toBe('builder/icons/..%2Fdrafts');
  });

  test('the library is listed with the caller’s usage', async () => {
    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: {
          icons: [stored],
          maxIcons: 64,
          maxBytes: 1048576,
          usedBytes: 70,
          usedIcons: 1,
        },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      icons: [stored],
      maxIcons: 64,
      maxBytes: 1048576,
      usedBytes: 70,
      usedIcons: 1,
    });
    expect(http.calls).toEqual([
      { method: 'get', url: 'builder/icons', body: undefined },
    ]);
  });

  test('a listing of another shape is an empty library, and rows that are no icons are left out', async () => {
    const empty = {
      icons: [],
      maxIcons: 0,
      maxBytes: 0,
      usedBytes: 0,
      usedIcons: 0,
    };

    for (const data of [undefined, null, 'forbidden', { icons: 'none' }]) {
      const http = fakeHttp({ 'get builder/icons': { status: 200, data } });

      await expect(createBuilderApi(http).listIcons()).resolves.toEqual(empty);
    }

    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: {
          icons: [
            { ...stored, aliases: ['old-plc', 7], canDelete: 'yes' },
            null,
            'icon',
            { name: 'plc' },
            { data: ICON_DATA },
            { name: 7, data: ICON_DATA },
            { name: 'bare', data: ICON_DATA },
          ],
          maxIcons: '64',
        },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      ...empty,
      icons: [
        stored,
        {
          name: 'bare',
          data: ICON_DATA,
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

  test('an icon is renamed and deleted by its name', async () => {
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
    await expect(api.deleteIcon('old-plc')).resolves.toBe(true);
    expect(http.calls).toEqual([
      { method: 'put', url: 'builder/icons/plc', body: { name: 'plc-2' } },
      { method: 'delete', url: 'builder/icons/old-plc', body: undefined },
    ]);
  });
});

// An API client whose library holds `icons`, which records its calls.
function fakeApi(icons = [], { refuse = {} } = {}) {
  const calls = [];
  const held = [...icons];

  return {
    calls,
    held,
    async listIcons() {
      calls.push('list');

      return {
        icons: held.map((icon) => ({ ...icon })),
        maxIcons: 64,
        maxBytes: 1048576,
        usedIcons: held.length,
        usedBytes: 70 * held.length,
      };
    },
    async uploadIcon(icon) {
      calls.push(['upload', icon.name]);

      if (refuse[icon.name]) {
        throw refuse[icon.name];
      }

      const added = { ...icon, aliases: [], canRename: true, canDelete: true };

      held.push(added);

      return { icon: added, created: true };
    },
    async renameIcon(name, newName) {
      calls.push(['rename', name, newName]);

      const icon = held.find((entry) => entry.name === name);

      icon.aliases = [...(icon.aliases || []), icon.name];
      icon.name = newName;

      return { ...icon };
    },
    async deleteIcon(name) {
      calls.push(['delete', name]);
      held.splice(
        held.findIndex((entry) => entry.name === name),
        1,
      );

      return true;
    },
  };
}

describe('the library the Builder keeps of the server’s icons', () => {
  test('reads the list, and finds an icon by its name or an alias, ignoring case', async () => {
    const api = fakeApi([stored, { name: 'Alarm', data: OTHER_DATA }]);
    const library = createIconLibrary(api);

    expect(library.state.status).toBe('idle');
    expect(library.lookup('plc')).toBeNull();

    await library.load();

    expect(library.state.status).toBe('ready');
    expect(library.state.icons.map((icon) => icon.name)).toEqual([
      'Alarm',
      'plc',
    ]);
    expect(library.state.usedIcons).toBe(2);
    expect(library.lookup('PLC').data).toBe(ICON_DATA);
    expect(library.lookup('OLD-plc').name).toBe('plc');
    expect(library.lookup('missing')).toBeNull();
    expect(library.lookup(undefined)).toBeNull();

    // A library read already is not read again for ensure().
    await library.ensure();
    expect(api.calls).toEqual(['list']);
    expect(library.failure).toBe(iconLibraryFailure);
  });

  test('reads the list again after an upload, a rename and a delete', async () => {
    const api = fakeApi([stored]);
    const library = createIconLibrary(api);

    await library.load();
    await library.upload({ name: 'hmi', data: OTHER_DATA });

    expect(library.lookup('hmi').data).toBe(OTHER_DATA);

    await library.rename('hmi', 'hmi-2');

    expect(library.lookup('hmi').name).toBe('hmi-2');

    await library.remove('hmi-2');

    expect(library.lookup('hmi')).toBeNull();
    expect(api.calls).toEqual([
      'list',
      ['upload', 'hmi'],
      'list',
      ['rename', 'hmi', 'hmi-2'],
      'list',
      ['delete', 'hmi-2'],
      'list',
    ]);
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
    expect(library.state.error).toBe(
      'The server could not be reached. Check the connection and try again.',
    );
    expect(library.lookup('plc').data).toBe(ICON_DATA);
  });

  test('says when a name is taken', () => {
    expect(nameTaken(refusal(409, 'taken'))).toBe(true);
    expect(nameTaken(refusal(422, 'bad'))).toBe(false);
    expect(nameTaken(new Error('x'))).toBe(false);
  });

  // The server's refusals of an icon are written to be shown.
  test.each([
    [422, 'icon is not a PNG image', 'Icon is not a PNG image.'],
    [
      422,
      'icon is 97 x 1 pixels; the limit is 96 x 96',
      'Icon is 97 x 1 pixels; the limit is 96 x 96.',
    ],
    [
      422,
      'icon library is full for you: each user may upload at most 64 icons',
      'Icon library is full for you: each user may upload at most 64 icons.',
    ],
    [
      409,
      'icon name "plc" is taken by an icon alice uploaded; choose another name',
      'Icon name "plc" is taken by an icon alice uploaded; choose another name.',
    ],
    [
      413,
      'icon is larger than 65536 bytes',
      'Icon is larger than 65536 bytes.',
    ],
    [400, 'icon data is not base64', 'Icon data is not base64.'],
    [404, 'icon not found', 'Icon not found.'],
    [
      403,
      'renaming a builder icon another user uploaded not allowed for bob',
      'Renaming a builder icon another user uploaded not allowed for bob.',
    ],
  ])('a %i says what the server said: %s', (status, message, said) => {
    expect(
      iconLibraryFailure(
        refusal(status, message, 'builder: invalid request: icon: no'),
      ),
    ).toBe(said);
  });

  test('a session that ended and a server out of reach say what they say elsewhere', () => {
    expect(iconLibraryFailure(refusal(401, 'unauthorized'))).toBe(
      'Your session has ended. Sign in again to continue.',
    );
    expect(iconLibraryFailure(new Error('Network Error'))).toBe(
      'The server could not be reached. Check the connection and try again.',
    );
  });

  test('a refusal that says nothing still says something', () => {
    expect(iconLibraryFailure(refusal(500, undefined))).toBe(
      'The server rejected the request.',
    );
    expect(
      iconLibraryFailure({ response: { status: 507, data: 'out of space' } }),
    ).toBe('The server rejected the request.');
  });
});

describe('an uploaded document’s icons go to the server', () => {
  const THIRD_DATA = base64Of(png(3, 1, [1, 2, 3, 255]));

  test('each of the four cases', async () => {
    const api = fakeApi([stored, { name: 'hmi', data: OTHER_DATA }], {
      refuse: { full: refusal(422, 'icon library is full for you: no') },
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
      "Custom icon full could not be added to the server's icon library: Icon library is full for you: no. The diagram keeps its own copy.",
      "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.",
    ]);
    expect(api.calls).toEqual([
      'list',
      ['upload', 'valve'],
      ['upload', 'full'],
      'list',
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
      throw refusal(403, 'listing builder icons not allowed for bob');
    };

    const doc = { nodes: [], icons: { valve: { data: THIRD_DATA } } };
    const { doc: ingested, warnings } = await ingestIcons(
      doc,
      createIconLibrary(api),
    );

    expect(ingested).toBe(doc);
    expect(warnings).toEqual([
      "The server's icon library could not be read, so the diagram keeps its custom icon. Listing builder icons not allowed for bob.",
    ]);
  });
});
