// @vitest-environment happy-dom
// The logouts that the user does not start: the idle timeout, a token the
// server calls invalid, and a navigation with an expired token.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const store = vi.hoisted(() => ({ logout: vi.fn() }));
const http = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: http }));
vi.mock('@/views/Experiments.vue', () => ({ default: { render: () => null } }));
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open() {
      return { close() {} };
    }
  },
  NotificationProgrammatic: class {
    open() {}
  },
}));

import { useErrorNotification } from '@/utils/errorNotif.js';
import { TimeoutTool } from '@/utils/timeout.js';

// Lets a promise chain finish.
const settled = () => new Promise((resolve) => setTimeout(resolve));

beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(store, {
    auth: true,
    token: 'token',
    role: { name: 'Global Admin' },
  });
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe('the idle timeout', () => {
  test('logs out at once when the server ends the session', async () => {
    http.get.mockResolvedValue({ status: 204 });

    new TimeoutTool().logoutUser();
    await settled();

    expect(http.get).toHaveBeenCalledWith('logout');
    expect(store.logout).toHaveBeenCalledTimes(1);
  });

  test('keeps the session when the server does not end it', async () => {
    http.get.mockResolvedValue({ status: 200 });

    new TimeoutTool().logoutUser();
    await settled();

    expect(store.logout).not.toHaveBeenCalled();
  });
});

test('a token the server calls invalid logs out at once', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => {});

  await useErrorNotification({
    response: {
      status: 401,
      data: 'invalid token',
      headers: new Map([['content-type', 'text/plain']]),
    },
  });

  expect(store.logout).toHaveBeenCalledTimes(1);
});

test('a navigation with an expired token logs out at once', async () => {
  vi.stubEnv('VITE_AUTH', 'enabled');
  store.token = `header.${btoa(JSON.stringify({ exp: 1 }))}.signature`;

  const { default: router } = await import('@/router');

  await router.push('/experiments');

  expect(store.logout).toHaveBeenCalledTimes(1);
});
