// How errors are shown: a notification naming the failure, then its cause in
// bold, all as text.
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/notify.js', () => ({ openNotification: vi.fn() }));
const store = vi.hoisted(() => ({ logout: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

import { openNotification } from '@/utils/notify.js';
import { AxiosError } from 'axios';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { setWsDown } from '@/utils/freshness.js';
import { SHUTTING_DOWN, UNREACHABLE } from '@/utils/unreachable.js';

beforeEach(() => {
  openNotification.mockClear();
  store.logout.mockClear();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
  setWsDown(false);
  vi.restoreAllMocks();
});

// the body of the notification last shown
const message = () => openNotification.mock.lastCall[0].message;

// its lines as HTML: the title, then each step
const lines = () =>
  [
    ...message().matchAll(/<span class="error-(?:title|step)">(.*?)<\/span>/g),
  ].map(([, html]) => html);

// a request error with a response from the server
const failed = ({
  status = 500,
  statusText = '',
  type = 'text/plain',
  data,
}) => ({
  message: `Request failed with status code ${status}`,
  response: {
    status,
    statusText,
    data,
    headers: new Map([['content-type', type]]),
  },
});

describe('useErrorNotification', () => {
  it.each([
    [
      'shows the message of an error that is not a request',
      new Error('Network Error'),
      ['<b>Error:</b> Network Error'],
    ],
    [
      "shows a JSON error's message and cause as text",
      failed({
        type: 'application/json',
        data: { message: 'starting <b>web</b>', cause: 'disk <missing>' },
      }),
      [
        '<b>Error:</b> starting &lt;b&gt;web&lt;/b&gt;',
        '<b>disk &lt;missing&gt;</b>',
      ],
    ],
    [
      'shows a text error body',
      failed({ status: 404, data: 'experiment not found' }),
      ['<b>Error:</b> experiment not found'],
    ],
  ])('%s', async (_, error, want) => {
    await useErrorNotification(error);
    expect(lines()).toEqual(want);
  });

  it('names the status of an error without a body', async () => {
    await useErrorNotification(
      failed({ status: 502, statusText: 'Bad <Gateway>' }),
    );
    expect(message()).toContain(
      '<b>Unknown Error Occurred: Bad &lt;Gateway&gt;</b>',
    );
  });

  it.each([
    ['text', failed({ status: 401, data: 'Invalid token' })],
    [
      'a JSON Error',
      failed({
        status: 401,
        type: 'application/json',
        data: { message: 'signature is invalid', code: 'unauthorized' },
      }),
    ],
  ])(
    'signs the user out when the server says in %s that the token is invalid',
    async (_, error) => {
      await useErrorNotification(error);
      expect(store.logout).toHaveBeenCalled();
      expect(message()).toContain('Token was invalid. Logging out');
    },
  );

  it.each([
    ['another 401', 401, 'user token error'],
    ['a 403 about an invalid name', 403, 'invalid name'],
  ])('keeps the user signed in for %s', async (_, status, text) => {
    await useErrorNotification(
      failed({ status, type: 'application/json', data: { message: text } }),
    );
    expect(store.logout).not.toHaveBeenCalled();
    expect(lines()).toEqual([`<b>Error:</b> ${text}`]);
  });

  it('reads a JSON Error whose Content-Type names a charset', async () => {
    await useErrorNotification(
      failed({
        status: 404,
        type: 'application/json; charset=utf-8',
        data: { message: 'job not found', code: 'not_found' },
      }),
    );
    expect(lines()).toEqual(['<b>Error:</b> job not found']);
  });

  it('shows a message given as a string', async () => {
    await useErrorNotification('Error uploading: disk <full>');
    expect(lines()).toEqual([
      '<b>Error:</b> Error uploading: disk &lt;full&gt;',
    ]);
  });

  it('shows any error, in each shape a request or caller gives one', async () => {
    const text = fc.string({ unit: 'binary' });
    const status = fc.integer({ min: 400, max: 599 });
    const errorArb = fc.oneof(
      text,
      fc.constantFrom(null, undefined),
      fc.record({ message: text }, { requiredKeys: [] }),
      fc
        .oneof(
          fc.record({
            status,
            statusText: text,
            type: fc.constantFrom('text/plain', 'application/json'),
            data: fc.oneof(
              text,
              fc.record(
                { message: text, cause: text },
                { requiredKeys: [], noNullPrototype: true },
              ),
            ),
          }),
          // no body
          fc.record({
            status,
            statusText: text,
            type: fc.constantFrom('text/html', 'application/json'),
            data: fc.constantFrom(undefined, null),
          }),
        )
        .map(failed),
    );
    await fc.assert(
      fc.asyncProperty(errorArb, async (error) => {
        openNotification.mockClear();
        await useErrorNotification(error);
        expect(openNotification).toHaveBeenCalledOnce();
        expect(message()).toMatch(/^<span role="alert">.*<\/span>$/s);
      }),
    );
  });

  it.each([undefined, null])(
    'shows a JSON error with a %s body by its request message',
    async (data) => {
      await useErrorNotification(failed({ type: 'application/json', data }));
      expect(openNotification).toHaveBeenCalledOnce();
      expect(lines()).toEqual([
        '<b>Error:</b> Request failed with status code 500',
      ]);
    },
  );

  it.each([
    ['the live connection is up', null, UNREACHABLE],
    ['the live connection was lost', 'lost', UNREACHABLE],
    ['the server said it is shutting down', 'shutdown', SHUTTING_DOWN],
  ])(
    'explains a request that got no response while %s',
    async (_, cause, want) => {
      if (cause) setWsDown(true, cause);
      await useErrorNotification(
        new AxiosError('Network Error', 'ERR_NETWORK', {}, {}),
      );
      expect(lines()).toEqual([`<b>Error:</b> ${want}`]);
    },
  );

  it("shows the server's own words for a job it refused as it shuts down", async () => {
    await useErrorNotification(
      failed({
        status: 503,
        data: 'the phēnix server is shutting down. Try again when it is available',
      }),
    );
    expect(lines()).toEqual([
      '<b>Error:</b> the phēnix server is shutting down. Try again when it is available',
    ]);
  });

  it('says nothing of requests canceled on leaving a page', async () => {
    await useErrorNotification({ code: 'ERR_CANCELED', message: 'canceled' });
    expect(console.warn).not.toHaveBeenCalled();
    expect(openNotification).not.toHaveBeenCalled();
  });
});
