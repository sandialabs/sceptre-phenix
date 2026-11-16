// The API client: an answer from the server ends a shutdown it announced, and
// a refusal or silence holds back the UI's background requests.
import { AxiosError } from 'axios';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.unmock('@/utils/axios.js');
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));

import axiosInstance from '@/utils/axios.js';
import {
  BACK_SPREAD_MS,
  forgetServer,
  holdOffMs,
  serverShuttingDown,
  setWsDown,
  startPoll,
} from '@/utils/freshness.js';

// a request the server answers with status and headers, or the network fails
const request = (status, headers = {}) =>
  axiosInstance.get('experiments', {
    adapter: async (config) => {
      if (status === null) {
        throw new AxiosError('Network Error', 'ERR_NETWORK', config, {});
      }
      const response = {
        data: '',
        status,
        statusText: '',
        headers,
        config,
      };
      if (status < 400) return response;
      throw new AxiosError('failed', 'ERR_BAD_RESPONSE', config, {}, response);
    },
  });

beforeEach(() => vi.useFakeTimers());

afterEach(() => {
  forgetServer();
  vi.useRealTimers();
});

describe('a shutdown the server announced', () => {
  beforeEach(() => {
    setWsDown(true, 'shutdown');
    vi.advanceTimersByTime(10);
  });

  it.each([
    ['ends with a success', 200, false],
    ['ends with an error the server answered', 404, false],
    ['holds through the 503 a server shutting down answers', 503, true],
    ['holds through a request that got no answer', null, true],
  ])('%s', async (_, status, shuttingDown, headers) => {
    await request(status, headers).catch(() => {});
    expect(serverShuttingDown()).toBe(shuttingDown);
  });
});

describe('background requests', () => {
  it('wait out the Retry-After of a 503, as from a server shutting down', async () => {
    await request(503, { 'retry-after': '120' }).catch(() => {});
    expect(serverShuttingDown()).toBe(true);
    expect(holdOffMs()).toBe(120_000);
  });

  it('go on after a 503 without Retry-After, which is no shutdown', async () => {
    await request(503).catch(() => {});
    expect(serverShuttingDown()).toBe(false);
    expect(holdOffMs()).toBe(0);
  });

  it('back off while a proxy answers 502 for the server, and come back once it answers', async () => {
    const ticks = [];
    const stop = startPoll(() => ticks.push(Date.now()), 10_000);
    await request(502).catch(() => {});
    // its turn at 10 s is put off to 20 s
    await vi.advanceTimersByTimeAsync(10_000);
    expect(ticks).toEqual([]);

    await request(404).catch(() => {});
    await vi.advanceTimersByTimeAsync(BACK_SPREAD_MS);
    expect(ticks).toHaveLength(1);
    stop();
  });

  it('are not held back by a request canceled on purpose', async () => {
    const ticks = [];
    const stop = startPoll(() => ticks.push(Date.now()), 10_000);
    const controller = new AbortController();
    controller.abort();
    await axiosInstance
      .get('experiments', { signal: controller.signal })
      .catch(() => {});
    await vi.advanceTimersByTimeAsync(10_000);
    stop();
    expect(ticks).toHaveLength(1);
  });
});
