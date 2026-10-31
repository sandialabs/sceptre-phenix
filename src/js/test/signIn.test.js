import { beforeEach, expect, test, vi } from 'vitest';

import SignIn from '@/views/SignIn.vue';
import axiosInstance from '@/utils/axios.js';

// The real modules need a browser. Vitest hoists vi.mock above the imports.
const store = vi.hoisted(() => ({ login: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: { post: vi.fn() } }));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));

// The component as its methods and open watcher see it.
function signIn() {
  const ctx = {
    ...SignIn.data(),
    $buefy: { toast: { open: vi.fn() } },
    $refs: {},
    $nextTick: (fn) => fn(),
  };
  for (const [name, fn] of Object.entries(SignIn.methods)) {
    ctx[name] = fn.bind(ctx);
  }
  ctx.open = (open) => {
    ctx.signUpModal = open;
    SignIn.watch.signUpModal.call(ctx, open);
  };
  return ctx;
}

// Lets the request promise chains finish.
const settled = () => new Promise((resolve) => setTimeout(resolve));

beforeEach(() => {
  vi.clearAllMocks();
});

test('signing in after Create Account uses the account sent, even if the dialog reopens', async () => {
  let finishSignUp;
  axiosInstance.post.mockImplementation((url) =>
    url === 'signup'
      ? new Promise((resolve) => (finishSignUp = resolve))
      : Promise.resolve({ data: { token: 't' } }),
  );
  const ctx = signIn();

  ctx.open(true);
  Object.assign(ctx.signUp, {
    username: 'alice',
    first_name: 'Alice',
    last_name: 'Smith',
    password: 'secret123',
    confirmPassword: 'secret123',
  });
  ctx.create();

  // The user closes and reopens the dialog and starts a new entry.
  ctx.open(false);
  ctx.open(true);
  ctx.signUp.username = 'bob';

  finishSignUp({ status: 200 });
  await settled();

  expect(axiosInstance.post).toHaveBeenCalledWith('login', {
    user: 'alice',
    pass: 'secret123',
  });
  expect(store.login).toHaveBeenCalledOnce();
});

test('Create Account closes its own dialog but not one reopened while it was pending', async () => {
  const finish = [];
  axiosInstance.post.mockImplementation((url) =>
    url === 'signup'
      ? new Promise((resolve) => finish.push(resolve))
      : Promise.resolve({ data: { token: 't' } }),
  );
  const fill = (ctx, username) =>
    Object.assign(ctx.signUp, {
      username,
      first_name: 'A',
      last_name: 'B',
      password: 'secret123',
      confirmPassword: 'secret123',
    });

  const same = signIn();
  same.open(true);
  fill(same, 'alice');
  same.create();
  finish.pop()({ status: 200 });
  await settled();
  expect(same.signUpModal).toBe(false);

  const reopened = signIn();
  reopened.open(true);
  fill(reopened, 'alice');
  reopened.create();
  reopened.open(false);
  reopened.open(true);
  reopened.signUp.username = 'bob';
  finish.pop()({ status: 200 });
  await settled();
  expect(reopened.signUpModal).toBe(true);
  expect(reopened.signUp.username).toBe('bob');
});

test('a taken name marks the User Name field only in the dialog that sent it', async () => {
  const taken = () => Promise.reject({ response: { status: 409 } });
  const fill = (ctx) =>
    Object.assign(ctx.signUp, {
      username: 'alice',
      first_name: 'A',
      last_name: 'B',
      password: 'secret123',
      confirmPassword: 'secret123',
    });

  axiosInstance.post.mockImplementation(taken);
  const same = signIn();
  same.open(true);
  fill(same);
  same.create();
  await settled();
  expect(same.userExists).toBe(true);
  expect(same.signUpModal).toBe(true);

  let fail;
  axiosInstance.post.mockImplementation(
    () => new Promise((_, reject) => (fail = reject)),
  );
  const reopened = signIn();
  reopened.open(true);
  fill(reopened);
  reopened.create();
  reopened.open(false);
  reopened.open(true);
  fail({ response: { status: 409 } });
  await settled();
  expect(reopened.userExists).toBe(false);
  expect(reopened.$buefy.toast.open).toHaveBeenCalledWith(
    expect.objectContaining({ message: 'User already exists' }),
  );
});
