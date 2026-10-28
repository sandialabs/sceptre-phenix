// @vitest-environment happy-dom
// The logouts that the user does not start: a token the server calls invalid,
// and a navigation with an expired token. timeout.test.js tests the idle one.

import { afterEach, beforeEach, expect, test, vi } from 'vitest';

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
