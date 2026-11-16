// @vitest-environment happy-dom
// Where the global navigation guard sends a user in each VITE_AUTH mode.

import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const store = vi.hoisted(() => ({}));
const http = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: http }));
vi.mock('@/views/Experiments.vue', () => ({ default: { render: () => null } }));
vi.mock('@/views/SignIn.vue', () => ({ default: { render: () => null } }));
vi.mock('@/views/ProxySignUp.vue', () => ({
  default: { props: ['username'], render: () => null },
}));

async function freshRouter(auth) {
  vi.stubEnv('VITE_AUTH', auth);
  vi.resetModules();

  return (await import('@/router.js')).default;
}

beforeEach(() => {
  vi.clearAllMocks();
  for (const key of Object.keys(store)) {
    delete store[key];
  }
  Object.assign(store, {
    auth: false,
    next: null,
    login: vi.fn(),
    logout: vi.fn(),
  });
});

afterEach(() => {
  vi.unstubAllEnvs();
});

test('with auth disabled, a visit signs in as Global Admin and goes on', async () => {
  const router = await freshRouter('disabled');

  await router.push('/experiments');

  expect(store.login).toHaveBeenCalledTimes(1);
  expect(store.login.mock.calls[0][0].user.role.name).toBe('Global Admin');
  expect(router.currentRoute.value.name).toBe('experiments');
});

test('with auth enabled, a visit without a session goes to sign in', async () => {
  const router = await freshRouter('enabled');

  await router.push('/experiments');

  expect(router.currentRoute.value.name).toBe('signin');
  expect(store.next.name).toBe('experiments');
});

test('with proxy auth, a known user goes on to the page', async () => {
  http.get.mockResolvedValue({ data: { token: 't' } });
  const router = await freshRouter('proxy');

  await router.push('/experiments');

  expect(http.get).toHaveBeenCalledWith('login');
  expect(store.login).toHaveBeenCalledWith({ token: 't' }, false);
  expect(router.currentRoute.value.name).toBe('experiments');
});

test('with proxy auth, an unknown user goes to sign up with their name', async () => {
  http.get.mockRejectedValue({ response: { data: ' bob \n' } });
  const router = await freshRouter('proxy');

  await router.push('/experiments');

  expect(router.currentRoute.value.name).toBe('proxysignup');
  expect(router.currentRoute.value.params.username).toBe('bob');
});
