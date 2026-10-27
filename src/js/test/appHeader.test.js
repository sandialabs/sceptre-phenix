import { beforeEach, expect, test, vi } from 'vitest';

import AppHeader from '@/components/AppHeader.vue';
import axiosInstance from '@/utils/axios.js';

// The real modules need a browser. Vitest hoists vi.mock above the imports.
const store = vi.hoisted(() => ({ logout: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: vi.fn() }));

// The component as Logout's click handler sees it, with its button shown or
// hidden in a collapsed menu.
function header({ shown = true, proxyAuth = false } = {}) {
  const burger = { focus: vi.fn() };
  const button = {
    focus: vi.fn(),
    getClientRects: () => (shown ? [{}] : []),
  };
  const ctx = {
    ...AppHeader.data(),
    proxyAuth,
    $buefy: { toast: { open: vi.fn() } },
    $el: {
      querySelector: (selector) =>
        selector === '.navbar-burger' ? burger : null,
    },
  };

  return {
    ctx,
    button,
    burger,
    click: () => AppHeader.methods.logout.call(ctx, { currentTarget: button }),
  };
}

// Lets the logout request's promise chain finish.
const settled = () => new Promise((resolve) => setTimeout(resolve));

beforeEach(() => {
  vi.clearAllMocks();
});

test.each([
  ['the server forgets the token', { status: 204 }],
  ['the server no longer knows an expired token', { status: 401 }],
])('Logout ends the session when %s', async (_, answer) => {
  axiosInstance.get.mockImplementation(() =>
    answer.status < 400
      ? Promise.resolve(answer)
      : Promise.reject({ response: answer }),
  );
  const { ctx, click } = header();

  click();
  expect(ctx.loggingOut).toBe(true);
  await settled();

  expect(axiosInstance.get).toHaveBeenCalledWith('logout');
  expect(store.logout).toHaveBeenCalledOnce();
  expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
  expect(ctx.loggingOut).toBe(false);
});

test('a failed logout keeps the session, says so, and returns focus to Logout', async () => {
  axiosInstance.get.mockRejectedValue(new Error('Network Error'));
  const { ctx, button, burger, click } = header();

  click();
  await settled();

  expect(store.logout).not.toHaveBeenCalled();
  expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
    expect.objectContaining({ type: 'is-danger' }),
  );
  expect(button.focus).toHaveBeenCalledOnce();
  expect(burger.focus).not.toHaveBeenCalled();
  expect(ctx.loggingOut).toBe(false);
});

test('a failed logout from a closed narrow-window menu focuses the menu button', async () => {
  axiosInstance.get.mockResolvedValue({ status: 500 });
  const { burger, click } = header({ shown: false });

  click();
  await settled();

  expect(store.logout).not.toHaveBeenCalled();
  expect(burger.focus).toHaveBeenCalledOnce();
});

test('a second press while the logout waits sends no second request', async () => {
  axiosInstance.get.mockResolvedValue({ status: 204 });
  const { click } = header();

  click();
  click();
  await settled();

  expect(axiosInstance.get).toHaveBeenCalledOnce();
  expect(store.logout).toHaveBeenCalledOnce();
});

test('Reauthorize says the account was reauthorized', async () => {
  axiosInstance.get.mockResolvedValue({ status: 204 });
  const { ctx, click } = header({ proxyAuth: true });

  click();
  await settled();

  expect(store.logout).toHaveBeenCalledOnce();
  expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
    expect.objectContaining({ type: 'is-success' }),
  );
});
