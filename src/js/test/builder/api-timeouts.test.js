// The time limits of Builder requests (withTimeouts in api.js), and how the
// save queue continues after a request that never got an answer.

import axios from 'axios';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import {
  classifyError,
  createBuilderApi,
  errorMessage,
  REQUEST_TIMEOUTS,
  requestTimeout,
  unanswered,
  withTimeouts,
} from '@/builder/api.js';
import { createAutosave } from '@/builder/autosave.js';

import { sampleDocument } from './fixtures.js';
import { createMemoryStore } from './memory-store.js';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

// A request that never settles. `seen` holds the config of each request.
function silentHttp() {
  const seen = [];
  const request =
    (method) =>
    (url, ...rest) => {
      seen.push({ method, url, rest });

      return new Promise(() => {});
    };

  return {
    seen,
    get: request('get'),
    post: request('post'),
    put: request('put'),
    patch: request('patch'),
    delete: request('delete'),
  };
}

describe('requestTimeout', () => {
  test.each([
    ['post', 'builder/drafts/d1/snapshots', REQUEST_TIMEOUTS.write],
    ['patch', 'builder/drafts/d1/cursor', REQUEST_TIMEOUTS.write],
    ['post', 'builder/drafts', REQUEST_TIMEOUTS.write],
    ['delete', 'builder/drafts/d1', REQUEST_TIMEOUTS.write],
    ['put', 'builder/layersets/default', REQUEST_TIMEOUTS.write],
    ['get', 'builder/drafts/d1', REQUEST_TIMEOUTS.read],
    ['get', 'builder/drafts/d1/snapshots/s2', REQUEST_TIMEOUTS.read],
    ['get', 'builder/sources', REQUEST_TIMEOUTS.read],
    ['post', 'builder/drafts/d1/publish', REQUEST_TIMEOUTS.long],
    ['post', 'builder/drafts/d1/preflight', REQUEST_TIMEOUTS.long],
    ['post', 'builder/generate', REQUEST_TIMEOUTS.long],
    ['post', 'builder/legacy', REQUEST_TIMEOUTS.long],
    ['post', 'builder/export/topology', REQUEST_TIMEOUTS.long],
    ['post', 'builder/package', REQUEST_TIMEOUTS.long],
    ['post', 'builder/package/resolve', REQUEST_TIMEOUTS.long],
    ['post', 'builder/cleanup?dryRun=true', REQUEST_TIMEOUTS.long],
  ])('%s %s waits %i ms', (method, path, want) => {
    expect(requestTimeout(method, path)).toBe(want);
  });

  test('reads are shorter than writes, and writes than server operations', () => {
    expect(REQUEST_TIMEOUTS.read).toBeLessThan(REQUEST_TIMEOUTS.write);
    expect(REQUEST_TIMEOUTS.write).toBeLessThan(REQUEST_TIMEOUTS.long);
  });
});

describe('withTimeouts', () => {
  test('a request with no answer fails as offline at its limit', async () => {
    const http = silentHttp();
    const request = withTimeouts(http).post('builder/drafts/d1/snapshots', {
      document: {},
    });
    const failure = request.catch((error) => error);

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUTS.write - 1);
    expect(http.seen[0].rest[1].signal.aborted).toBe(false);

    await vi.advanceTimersByTimeAsync(1);
    const error = await failure;

    expect(error.code).toBe('ETIMEDOUT');
    expect(error.response).toBeUndefined();
    expect(classifyError(error)).toBe('offline');
    expect(errorMessage('offline', error)).toBe(
      'The server did not answer in time. Check the connection and try again.',
    );
    // The request is aborted, so it does not hold a connection.
    expect(http.seen[0].rest[1].signal.aborted).toBe(true);
  });

  test('a read waits for the read limit', async () => {
    const http = silentHttp();
    const failure = withTimeouts(http)
      .get('builder/drafts/d1')
      .catch((error) => error);

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUTS.read);

    expect(classifyError(await failure)).toBe('offline');
  });

  test('an answer in time is the result, and stops the timer', async () => {
    const response = { data: { ok: true }, headers: {} };
    const http = { patch: vi.fn(async () => response) };

    await expect(
      withTimeouts(http).patch(
        'builder/drafts/d1/cursor',
        { index: 0 },
        { headers: { 'If-Match': '"1"' } },
      ),
    ).resolves.toBe(response);
    expect(vi.getTimerCount()).toBe(0);
    // The body and the request config pass through, with the abort signal.
    expect(http.patch).toHaveBeenCalledWith(
      'builder/drafts/d1/cursor',
      { index: 0 },
      {
        headers: { 'If-Match': '"1"' },
        signal: expect.any(AbortSignal),
      },
    );
  });

  test('a refusal in time is the failure, as the client gives it', async () => {
    const refusal = Object.assign(new Error('status 412'), {
      response: { status: 412 },
    });
    const http = { delete: vi.fn(async () => Promise.reject(refusal)) };

    await expect(withTimeouts(http).delete('builder/drafts/d1')).rejects.toBe(
      refusal,
    );
    expect(vi.getTimerCount()).toBe(0);
  });

  test('a request without a body sends no config in its place', async () => {
    const http = { post: vi.fn(async () => ({ data: {} })) };

    await withTimeouts(http).post('builder/cleanup');

    expect(http.post).toHaveBeenCalledWith('builder/cleanup', undefined, {
      signal: expect.any(AbortSignal),
    });
  });

  test('an axios request that never settles is aborted at its limit', async () => {
    const aborted = vi.fn();
    const instance = axios.create({
      adapter: (config) =>
        new Promise((_, reject) => {
          config.signal.addEventListener('abort', () => {
            aborted();
            reject(new axios.CanceledError());
          });
        }),
    });
    const failure = createBuilderApi(withTimeouts(instance))
      .getDraft('d1')
      .catch((error) => error);

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUTS.read);
    const error = await failure;

    expect(aborted).toHaveBeenCalledOnce();
    expect(error.code).toBe('ETIMEDOUT');
    expect(classifyError(error)).toBe('offline');
  });
});

describe('unanswered', () => {
  test.each([
    ['a request that timed out', { code: 'ETIMEDOUT' }, true],
    ['a failed connection', { code: 'ERR_NETWORK' }, true],
    [
      'a refusal',
      { code: 'ERR_BAD_REQUEST', response: { status: 412 } },
      false,
    ],
    ['an error of the client', new TypeError('not a function'), false],
  ])('unanswered(%s) is %s', (_, error, want) => {
    expect(unanswered(error)).toBe(want);
  });
});

describe('the save queue after a request with no answer', () => {
  // A server whose first snapshot append stores the snapshot but never
  // answers. The append sent again is refused (412), because the draft
  // changed, and the read after it finds the snapshot.
  function server() {
    const draft = (revision, history) => ({
      data: {
        draft: {
          id: 'd1',
          snapshotId: history.at(-1).id,
          cursor: history.length - 1,
          snapshots: history.length,
        },
        history,
        etag: `"${revision}"`,
      },
      headers: {},
    });
    const state = { revision: 1, history: [{ id: 's1' }], silent: 1 };
    const http = {
      post: vi.fn((_, body, config) => {
        if (config.headers['If-Match'] !== `"${state.revision}"`) {
          return Promise.reject(
            Object.assign(new Error('status 412'), {
              response: { status: 412, data: {} },
            }),
          );
        }

        state.revision += 1;
        state.history = [
          ...state.history,
          { id: `s${state.history.length + 1}`, opId: body.opId },
        ];

        if (state.silent > 0) {
          state.silent -= 1;

          return new Promise(() => {});
        }

        return Promise.resolve(draft(state.revision, state.history));
      }),
      get: vi.fn(() => Promise.resolve(draft(state.revision, state.history))),
    };

    return { state, http };
  }

  test('says offline, then finds its save stored and sends it once', async () => {
    const { state, http } = server();
    const queue = createAutosave({
      api: createBuilderApi(withTimeouts(http)),
      store: createMemoryStore(),
      actor: 'alice',
      isOnline: () => true,
    });

    await queue.attach({
      draftId: 'd1',
      etag: '"1"',
      serverHead: { snapshotId: 's1', cursor: 0, snapshots: 1 },
    });

    const saved = queue.commit({
      id: 'c1',
      label: 'Added a device',
      snapshot: sampleDocument().doc,
    });

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUTS.write);
    expect(queue.state).toMatchObject({ status: 'offline', pending: 1 });
    expect(await saved).toMatchObject({ status: 'offline' });

    // The first retry gets the refusal, reads the draft and finds the save.
    await vi.advanceTimersByTimeAsync(1000);

    expect(queue.state).toMatchObject({ status: 'saved', pending: 0 });
    expect(queue.record.etag).toBe('"2"');
    expect(queue.record.entries[0].serverSnapshotId).toBe('s2');
    expect(state.history.map((snapshot) => snapshot.id)).toEqual(['s1', 's2']);
    expect(http.post).toHaveBeenCalledTimes(2);
    expect(http.get).toHaveBeenCalledOnce();
  });

  test('a read after the refusal with no answer retries, not a conflict', async () => {
    const { http } = server();
    const read = http.get.getMockImplementation();
    const onConflict = vi.fn();

    http.get.mockImplementationOnce(() => new Promise(() => {}));

    const queue = createAutosave({
      api: createBuilderApi(withTimeouts(http)),
      store: createMemoryStore(),
      actor: 'alice',
      onConflict,
      isOnline: () => true,
    });

    await queue.attach({
      draftId: 'd1',
      etag: '"1"',
      serverHead: { snapshotId: 's1', cursor: 0, snapshots: 1 },
    });
    queue.commit({ id: 'c1', label: 'one', snapshot: sampleDocument().doc });

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUTS.write);
    // The retry is refused, and its read gets no answer.
    await vi.advanceTimersByTimeAsync(1000 + REQUEST_TIMEOUTS.read);
    expect(queue.state).toMatchObject({ status: 'offline', pending: 1 });

    http.get.mockImplementation(read);
    await vi.advanceTimersByTimeAsync(2000);

    expect(queue.state).toMatchObject({ status: 'saved', pending: 0 });
    expect(onConflict).not.toHaveBeenCalled();
  });
});
