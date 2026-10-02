// The icon library calls of the API client, and the library the Custom
// icons dialog is given (iconLibrary.js).

import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import { createBuilderApi, ICONS_PATH, iconPath } from '@/builder/api.js';
import {
  createIconLibrary,
  iconLibraryFailure,
} from '@/builder/iconLibrary.js';

import { ICON_DATA, ICON_KEY } from './png.js';

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
    delete: handler('delete'),
  };
}

function refusal(status, message, cause = '') {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data: { message, cause } },
  });
}

const HEX = ICON_KEY.replace('sha256:', '');
const stored = {
  id: ICON_KEY,
  name: 'plc',
  width: 1,
  height: 1,
  bytes: 70,
  created: '2026-10-02T12:34:07.786286Z',
  data: ICON_DATA,
};

describe('icon library routes', () => {
  test('match the documented backend contract', () => {
    expect(ICONS_PATH).toBe('builder/icons');
    // The server names an icon by the 64 hex digits of its id.
    expect(iconPath(ICON_KEY)).toBe(`builder/icons/${HEX}`);
    expect(iconPath(HEX)).toBe(`builder/icons/${HEX}`);
    // Nothing of an id can name another route.
    expect(iconPath('sha256:../drafts')).toBe('builder/icons/..%2Fdrafts');
  });

  test('the library is listed with its limits', async () => {
    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: {
          icons: [stored],
          maxIcons: 64,
          maxBytes: 1048576,
          usedBytes: 70,
        },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      icons: [stored],
      maxIcons: 64,
      maxBytes: 1048576,
      usedBytes: 70,
    });
    expect(http.calls).toEqual([
      { method: 'get', url: 'builder/icons', body: undefined },
    ]);
  });

  test('a listing of another shape is an empty library, and rows that are no icons are left out', async () => {
    const empty = { icons: [], maxIcons: 0, maxBytes: 0, usedBytes: 0 };

    for (const data of [undefined, null, 'forbidden', { icons: 'none' }]) {
      const http = fakeHttp({ 'get builder/icons': { status: 200, data } });

      await expect(createBuilderApi(http).listIcons()).resolves.toEqual(empty);
    }

    const http = fakeHttp({
      'get builder/icons': {
        status: 200,
        data: {
          icons: [
            stored,
            null,
            'icon',
            { id: ICON_KEY },
            { data: ICON_DATA },
            { id: 7, data: ICON_DATA },
          ],
          maxIcons: '64',
        },
      },
    });

    await expect(createBuilderApi(http).listIcons()).resolves.toEqual({
      ...empty,
      icons: [stored],
    });
  });

  test('an upload sends the PNG and a name, and answers with the stored icon', async () => {
    const http = fakeHttp({
      'post builder/icons': { status: 201, data: stored },
    });
    const api = createBuilderApi(http);

    await expect(
      api.uploadIcon({ name: 'plc', data: ICON_DATA }),
    ).resolves.toEqual({ icon: stored, created: true });
    expect(http.calls[0]).toEqual({
      method: 'post',
      url: 'builder/icons',
      body: { name: 'plc', data: ICON_DATA },
    });

    // No name is sent for an icon without one: the server refuses an
    // unknown field, and takes a missing name.
    await api.uploadIcon({ name: '', data: ICON_DATA });
    await api.uploadIcon({ data: ICON_DATA });
    expect(http.calls[1].body).toEqual({ data: ICON_DATA });
    expect(http.calls[2].body).toEqual({ data: ICON_DATA });
  });

  test('bytes the library already holds answer with the icon it has', async () => {
    const http = fakeHttp({
      'post builder/icons': { status: 200, data: stored },
    });

    await expect(
      createBuilderApi(http).uploadIcon({ name: 'other', data: ICON_DATA }),
    ).resolves.toEqual({ icon: stored, created: false });
  });

  test('an icon is deleted by its id', async () => {
    const http = fakeHttp();

    await expect(createBuilderApi(http).deleteIcon(ICON_KEY)).resolves.toBe(
      true,
    );
    expect(http.calls).toEqual([
      { method: 'delete', url: `builder/icons/${HEX}`, body: undefined },
    ]);
  });
});

describe('the library the Custom icons dialog is given', () => {
  test('lists, adds and deletes through the API client', async () => {
    const calls = [];
    const api = {
      listIcons: async () => (calls.push('list'), { icons: [stored] }),
      uploadIcon: async (icon) => (
        calls.push(['upload', icon]),
        { icon: stored, created: true }
      ),
      deleteIcon: async (id) => (calls.push(['delete', id]), true),
    };
    const library = createIconLibrary(api);

    await expect(library.list()).resolves.toEqual({ icons: [stored] });
    await expect(
      library.upload({ name: 'plc', data: ICON_DATA }),
    ).resolves.toEqual({ icon: stored, created: true });
    await expect(library.remove(ICON_KEY)).resolves.toBe(true);
    expect(calls).toEqual([
      'list',
      ['upload', { name: 'plc', data: ICON_DATA }],
      ['delete', ICON_KEY],
    ]);
    expect(library.failure).toBe(iconLibraryFailure);
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
      'icon library is full: at most 64 icons',
      'Icon library is full: at most 64 icons.',
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
      'adding a builder icon not allowed for bob',
      'Adding a builder icon not allowed for bob.',
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
