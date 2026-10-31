// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';

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

enableAutoUnmount(afterEach);

function mountSignIn() {
  return mount(SignIn, {
    attachTo: document.body,
    global: { plugins: [Buefy] },
  });
}

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

test('the sign-in fields are a form that Enter submits, with autocomplete tokens', async () => {
  axiosInstance.post.mockResolvedValue({ data: { token: 't' } });
  const wrapper = mountSignIn();

  expect(wrapper.findAll('h1').map((h) => h.text())).toEqual(['Sign in']);
  const form = wrapper.get('form.signin-form');
  const username = form.get('#signin-username');
  const password = form.get('#signin-password');
  expect(username.attributes('autocomplete')).toBe('username');
  expect(password.attributes('autocomplete')).toBe('current-password');
  expect(form.get('label[for="signin-username"]').text()).toBe('Username');
  expect(form.get('button[type="submit"]').text()).toBe('Submit');
  expect(
    form.findAll('button').filter((b) => b.attributes('type') !== 'submit'),
  ).toHaveLength(1);

  await username.setValue('alice');
  await password.setValue('secret123');
  await form.trigger('submit');
  await flushPromises();

  expect(axiosInstance.post).toHaveBeenCalledOnce();
  expect(axiosInstance.post).toHaveBeenCalledWith('login', {
    user: 'alice',
    pass: 'secret123',
  });
});

test('empty fields get inline errors, focus, and no request', async () => {
  const wrapper = mountSignIn();
  await wrapper.get('form.signin-form').trigger('submit');

  const username = wrapper.get('#signin-username');
  expect(username.attributes('aria-invalid')).toBe('true');
  expect(username.attributes('aria-describedby')).toBe('signin-username-error');
  expect(wrapper.get('#signin-username-error').text()).toBe(
    'You must include a username',
  );
  expect(wrapper.get('#signin-password-error').text()).toBe(
    'You must include a password',
  );
  expect(document.activeElement).toBe(username.element);
  expect(axiosInstance.post).not.toHaveBeenCalled();

  await username.setValue('alice');
  await wrapper.get('form.signin-form').trigger('submit');
  expect(wrapper.find('#signin-username-error').exists()).toBe(false);
  expect(document.activeElement).toBe(wrapper.get('#signin-password').element);
});

test.each([
  [{ status: 401 }, 'The username and/or password is incorrect'],
  [{ message: 'Network Error' }, 'The data server is not available.'],
  [{ status: 500 }, 'Getting the user information failed.'],
])(
  'a failed sign-in keeps the username and says why inline (%o)',
  async (err, message) => {
    axiosInstance.post.mockRejectedValue(err);
    const wrapper = mountSignIn();

    await wrapper.get('#signin-username').setValue('alice');
    await wrapper.get('#signin-password').setValue('wrong');
    await wrapper.get('form.signin-form').trigger('submit');
    await flushPromises();

    expect(wrapper.get('#signin-username').element.value).toBe('alice');
    expect(wrapper.get('#signin-password').element.value).toBe('');
    expect(wrapper.get('[role="alert"]').text()).toBe(message);
    expect(document.activeElement).toBe(
      wrapper.get('#signin-password').element,
    );
    expect(store.login).not.toHaveBeenCalled();
  },
);

test('Create Account shows each missing field inline and focuses the first', () => {
  const ctx = signIn();
  ctx.$refs.signupFirstName = { focus: vi.fn() };
  ctx.open(true);
  ctx.signUp.username = 'alice';
  ctx.signUp.password = 'secret123';
  ctx.signUp.confirmPassword = 'other123';

  ctx.create();

  expect(ctx.signUpErrors).toEqual({
    username: null,
    first_name: 'You must include a first name',
    last_name: 'You must include a last name',
    password: null,
    confirmPassword: 'Your passwords do not match',
  });
  expect(ctx.$refs.signupFirstName.focus).toHaveBeenCalledOnce();
  expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
  expect(axiosInstance.post).not.toHaveBeenCalled();

  // Each open starts without errors.
  ctx.open(false);
  ctx.open(true);
  expect(Object.values(ctx.signUpErrors).every((e) => e === null)).toBe(true);
});

test('Create Account uses new-password for both password fields', async () => {
  const wrapper = mountSignIn();
  await wrapper.get('button[type="button"]').trigger('click');
  await flushPromises();

  const dialog = wrapper.get('form.modal-card');
  expect(dialog.get('h2').text()).toBe('Create a New Account');
  expect(dialog.get('#signup-password').attributes('autocomplete')).toBe(
    'new-password',
  );
  expect(
    dialog.get('#signup-confirm-password').attributes('autocomplete'),
  ).toBe('new-password');
  expect(dialog.get('button[type="submit"]').text()).toBe('Create User');
});
