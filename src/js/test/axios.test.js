// The API client: an answer from the server ends a shutdown it announced.
import { AxiosError } from 'axios';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));

import axiosInstance from '@/utils/axios.js';
import { serverShuttingDown, setWsDown } from '@/utils/freshness.js';

// a request the server answers with status, or the network fails
const request = (status) =>
  axiosInstance.get('experiments', {
    adapter: async (config) => {
      if (status === null) {
        throw new AxiosError('Network Error', 'ERR_NETWORK', config, {});
      }
      const response = {
        data: '',
        status,
        statusText: '',
        headers: {},
        config,
      };
      if (status < 400) return response;
      throw new AxiosError('failed', 'ERR_BAD_RESPONSE', config, {}, response);
    },
  });

beforeEach(() => {
  vi.useFakeTimers();
  setWsDown(true, 'shutdown');
  vi.advanceTimersByTime(10);
});

afterEach(() => {
  setWsDown(false);
  vi.useRealTimers();
});

describe('a shutdown the server announced', () => {
  it.each([
    ['ends with a success', 200, false],
    ['ends with an error the server answered', 404, false],
    ['holds through the 503 a server shutting down answers', 503, true],
    ['holds through a request that got no answer', null, true],
  ])('%s', async (_, status, shuttingDown) => {
    await request(status).catch(() => {});
    expect(serverShuttingDown()).toBe(shuttingDown);
  });
});
