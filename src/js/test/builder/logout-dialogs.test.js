// @vitest-environment happy-dom
// The logout warning and the Builder's sign-in, mounted and driven as a user
// would; logout.test.js holds the logout flow and the session's storage.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { reactive } from 'vue';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

// The app store: the signed-in user, which each test sets.
const phenix = vi.hoisted(() => ({ state: null }));

vi.mock('@/router', () => ({ default: { replace: vi.fn() } }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix.state }));
// The sign-in posts the password with the plain client.
vi.mock('axios', () => ({ default: { post: vi.fn() } }));
vi.mock('@/builder/session.js', async (importOriginal) => ({
  ...(await importOriginal()),
  draftExport: vi.fn(),
  resumeBuilderSaves: vi.fn(async () => ({ changes: 0 })),
}));

import axios from 'axios';

import LogoutWarning from '@/components/LogoutWarning.vue';
import BuilderSignIn from '@/components/builder/BuilderSignIn.vue';
import { draftExport, resumeBuilderSaves } from '@/builder/session.js';
import { signIn } from '@/builder/signin.js';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.clearAllMocks();
  document.body.innerHTML = '';
});

const byId = (wrapper, testId) => wrapper.find(`[data-testid="${testId}"]`);

describe('the warning', () => {
  const idle = {
    reason: 'idle',
    changes: 2,
    unapplied: '',
    drafts: [{ key: 'k1', name: 'lab.json' }],
    canStay: true,
    secondsLeft: 60,
    notice: '',
  };

  // Mounts the warning with none shown; the app store's answer closes it.
  function mountWarning() {
    phenix.state = reactive({
      username: 'alice',
      logoutWarning: null,
      answerLogoutWarning: vi.fn(() => {
        phenix.state.logoutWarning = null;
      }),
    });

    return mount(LogoutWarning, { attachTo: document.body });
  }

  // Shows `warning`, as the logout flow does, and lets it open.
  async function show(warning) {
    phenix.state.logoutWarning = warning;
    await flushPromises();
  }

  // The warning, shown after focus was on a page button.
  async function opened(warning) {
    const opener = document.createElement('button');

    document.body.append(opener);
    opener.focus();

    const wrapper = mountWarning();

    await show(warning);

    return { wrapper, opener };
  }

  const buttons = (wrapper) =>
    wrapper.findAll('button').map((button) => button.text());

  test('is an alert dialog whose description holds the message and the countdown', async () => {
    const wrapper = mountWarning();

    await show(idle);

    const dialog = wrapper.get('dialog');

    expect(dialog.attributes('role')).toBe('alertdialog');
    expect(dialog.attributes('aria-modal')).toBe('true');
    expect(dialog.attributes('aria-labelledby')).toBe('logout-warning-title');
    expect(dialog.attributes('aria-describedby')).toBe(
      'logout-warning-message logout-warning-countdown',
    );
    expect(dialog.attributes('data-theme')).toBe('light');
    expect(dialog.element.open).toBe(true);
    expect(wrapper.get('h2#logout-warning-title').text()).toBe(
      'You will be logged out',
    );
    expect(wrapper.get('p#logout-warning-message').text()).toMatch(
      /^You have been inactive for a while\. 2 changes/,
    );
    expect(wrapper.get('#logout-warning-countdown').text()).toBe(
      'Logging out in 60 seconds.',
    );
    // The regions that speak later are there, empty, from the start.
    expect(wrapper.get('[role="status"]').text()).toBe('');
    expect(
      wrapper.get('[aria-live="assertive"]').attributes('aria-atomic'),
    ).toBe('true');
    expect(buttons(wrapper)).toEqual([
      'Download',
      'Stay signed in',
      'Log out now',
    ]);
  });

  test('offers one Download per draft, named when there are several, and none without a draft to save', async () => {
    const wrapper = mountWarning();

    await show({
      ...idle,
      drafts: [
        { key: 'k1', name: 'lab.json' },
        { key: 'k2', name: 'lab-2.json' },
      ],
    });
    expect(buttons(wrapper)).toEqual([
      'Download lab.json',
      'Download lab-2.json',
      'Stay signed in',
      'Log out now',
    ]);

    await show({
      ...idle,
      changes: 0,
      unapplied: 'Your changes to Device web01 cannot be saved.',
      drafts: [],
    });
    expect(buttons(wrapper)).toEqual(['Stay signed in', 'Log out now']);
    expect(wrapper.text()).toContain('Stay signed in to fix them.');
    expect(wrapper.text()).not.toContain('Download');
  });

  test('says the near-end notice, and offers no Stay once the session has expired', async () => {
    const wrapper = mountWarning();

    await show({
      ...idle,
      reason: 'expired',
      canStay: false,
      secondsLeft: 10,
      notice: 'Logging out in 10 seconds.',
    });

    expect(byId(wrapper, 'logout-warning-notice').text()).toBe(
      'Logging out in 10 seconds.',
    );
    expect(wrapper.get('#logout-warning-countdown').text()).toBe(
      'Logging out in 10 seconds.',
    );
    expect(wrapper.text()).toContain('To sign in again, you must log out.');
    expect(wrapper.text()).not.toContain('Stay signed in');
    expect(wrapper.text()).not.toContain('Sign in again');
  });

  test("offers Sign in again, before Log out now, when the Builder's page can sign in again", async () => {
    const wrapper = mountWarning();

    await show({ ...idle, reason: 'expired', canStay: false, canSignIn: true });

    expect(buttons(wrapper)).toEqual([
      'Download',
      'Sign in again',
      'Log out now',
    ]);
    expect(byId(wrapper, 'logout-warning-signin').attributes('type')).toBe(
      'button',
    );
    expect(wrapper.text()).toContain('Sign in again to save them.');
    expect(wrapper.text()).not.toContain('you must log out');
  });

  test('a logout the user asked for has no countdown', async () => {
    const wrapper = mountWarning();

    await show({ ...idle, reason: 'manual', secondsLeft: null });

    expect(wrapper.get('dialog').attributes('aria-describedby')).toBe(
      'logout-warning-message',
    );
    expect(byId(wrapper, 'logout-warning-countdown').exists()).toBe(false);
    expect(wrapper.text()).toContain('Log out anyway');
  });

  test('is not there without a warning', () => {
    expect(mountWarning().find('dialog').exists()).toBe(false);
  });

  test('opens with focus on Stay signed in, and Stay gives focus back where it was', async () => {
    const { wrapper, opener } = await opened(idle);

    expect(document.activeElement).toBe(
      byId(wrapper, 'logout-warning-stay').element,
    );

    await byId(wrapper, 'logout-warning-stay').trigger('click');
    await flushPromises();

    expect(phenix.state.answerLogoutWarning).toHaveBeenCalledWith('stay');
    expect(wrapper.find('dialog').exists()).toBe(false);
    expect(document.activeElement).toBe(opener);
  });

  test('Escape stays as Stay signed in does, and Sign in again gives focus back too', async () => {
    const first = await opened(idle);

    await first.wrapper.get('dialog').trigger('keydown', { key: 'Escape' });
    await flushPromises();

    expect(phenix.state.answerLogoutWarning).toHaveBeenCalledWith('stay');
    expect(document.activeElement).toBe(first.opener);
    first.wrapper.unmount();

    const expired = { ...idle, reason: 'expired', canStay: false };
    const second = await opened({ ...expired, canSignIn: true });
    const again = byId(second.wrapper, 'logout-warning-signin');

    expect(document.activeElement).toBe(again.element);
    await again.trigger('click');
    await flushPromises();

    expect(phenix.state.answerLogoutWarning).toHaveBeenCalledWith('signin');
    expect(document.activeElement).toBe(second.opener);
  });

  test('Log out now logs out, and focus goes to the main region', async () => {
    const main = document.createElement('main');

    main.id = 'main';
    document.body.append(main);

    const { wrapper, opener } = await opened(idle);

    await byId(wrapper, 'logout-warning-confirm').trigger('click');
    await flushPromises();

    expect(phenix.state.answerLogoutWarning).toHaveBeenCalledWith('logout');
    expect(document.activeElement).not.toBe(opener);
    expect(document.activeElement).toBe(main);
    expect(main.getAttribute('tabindex')).toBe('-1');
  });

  test('Download saves the draft from inside the dialog and says so, or says the browser no longer holds it', async () => {
    const createObjectURL = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue('blob:lab');
    const { wrapper } = await opened(idle);
    const download = byId(wrapper, 'logout-warning-download');

    draftExport.mockResolvedValueOnce({ name: 'lab.json', text: '{}' });
    await download.trigger('click');
    await flushPromises();

    expect(draftExport).toHaveBeenCalledWith({
      username: 'alice',
      key: 'k1',
      name: 'lab.json',
    });
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await createObjectURL.mock.calls[0][0].text()).toBe('{}');
    expect(wrapper.get('[role="status"]').text()).toBe('Saved lab.json.');
    // The link it followed is gone, and the warning still waits.
    expect(wrapper.find('a').exists()).toBe(false);
    expect(phenix.state.answerLogoutWarning).not.toHaveBeenCalled();

    draftExport.mockResolvedValueOnce(null);
    await download.trigger('click');
    await flushPromises();

    expect(wrapper.get('[role="status"]').text()).toBe(
      'This browser no longer holds these changes.',
    );
  });
});

// Signing in again without leaving the Builder (see builder/signin.js).
describe("the Builder's sign-in", () => {
  // Mounts the sign-in on a page whose session has a JWT, with `state`.
  function mountSignIn(state) {
    phenix.state = reactive({
      username: 'alice',
      auth: true,
      token: `h.${btoa(JSON.stringify({ exp: 4102444800 }))}.s`,
      loggingOut: false,
      renewLogin: vi.fn(() => true),
    });
    Object.assign(signIn, { open: false, needed: false, declined: false });
    Object.assign(signIn, state);

    const pinia = createPinia();
    const wrapper = mount(BuilderSignIn, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });

    return { wrapper, builder: useBuilderStore(pinia) };
  }

  // Opens the dialog from the notice's Sign in again, as the user does.
  async function openFromNotice(wrapper) {
    const again = byId(wrapper, 'signin-again');

    again.element.focus();
    await again.trigger('click');
    await flushPromises();
  }

  const password = (wrapper) => byId(wrapper, 'signin-password');

  test("asks for the same user's password in a labelled modal form, the username shown, not typed", () => {
    const { wrapper } = mountSignIn({ open: true, needed: true });
    const dialog = byId(wrapper, 'builder-signin');

    expect(dialog.element.tagName).toBe('DIALOG');
    expect(dialog.attributes('aria-modal')).toBe('true');
    expect(dialog.attributes('aria-labelledby')).toBe('builder-signin-title');
    expect(dialog.attributes('aria-describedby')).toBe(
      'builder-signin-message',
    );
    expect(wrapper.get('h2#builder-signin-title').text()).toBe('Sign in again');

    const user = wrapper.get('#builder-signin-user');

    expect(user.element.value).toBe('alice');
    expect(user.attributes()).toHaveProperty('readonly');
    expect(user.attributes('autocomplete')).toBe('username');
    expect(wrapper.get('label[for="builder-signin-user"]').text()).toBe(
      'Username',
    );

    const field = wrapper.get('#builder-signin-password');

    expect(field.attributes('type')).toBe('password');
    expect(field.attributes('autocomplete')).toBe('current-password');
    expect(wrapper.get('label[for="builder-signin-password"]').text()).toBe(
      'Password',
    );
    // Rendered, empty, from the start, so it is known before its first
    // message.
    expect(wrapper.get('#builder-signin-error').attributes('role')).toBe(
      'alert',
    );
    expect(byId(wrapper, 'signin-submit').attributes('type')).toBe('submit');
    expect(byId(wrapper, 'builder-signin-notice').exists()).toBe(false);
  });

  test('once declined, a notice offers it again; nothing shows while the session goes on', () => {
    const declined = mountSignIn({ needed: true, declined: true }).wrapper;

    expect(byId(declined, 'builder-signin-notice').exists()).toBe(true);
    expect(byId(declined, 'signin-again').attributes('aria-haspopup')).toBe(
      'dialog',
    );
    expect(declined.find('dialog').exists()).toBe(false);
    declined.unmount();

    const going = mountSignIn({}).wrapper;

    expect(going.find('dialog').exists()).toBe(false);
    expect(going.find('div').exists()).toBe(false);
  });

  test('the password field takes focus as it opens, and a wrong password leaves focus there with the error', async () => {
    const { wrapper } = mountSignIn({ needed: true, declined: true });

    await openFromNotice(wrapper);

    expect(signIn.open).toBe(true);
    expect(document.activeElement).toBe(password(wrapper).element);

    // No password: the field says so and keeps focus.
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(byId(wrapper, 'signin-error').text()).toBe('Enter your password.');
    expect(axios.post).not.toHaveBeenCalled();

    axios.post.mockRejectedValueOnce({ response: { status: 401 } });
    await password(wrapper).setValue('wrong');
    byId(wrapper, 'signin-submit').element.focus();
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(axios.post).toHaveBeenCalledWith(
      expect.stringMatching(/api\/v1\/login$/),
      { user: 'alice', pass: 'wrong' },
    );
    expect(byId(wrapper, 'signin-error').text()).toBe(
      'The password is incorrect.',
    );
    expect(password(wrapper).attributes('aria-invalid')).toBe('true');
    expect(password(wrapper).attributes('aria-describedby')).toBe(
      'builder-signin-error',
    );
    expect(document.activeElement).toBe(password(wrapper).element);
    expect(byId(wrapper, 'signin-submit').text()).toBe('Sign in');
  });

  test('Cancel keeps the notice, and its Sign in again takes the focus the dialog leaves', async () => {
    const { wrapper, builder } = mountSignIn({ needed: true, declined: true });

    await openFromNotice(wrapper);
    await password(wrapper).setValue('typed');
    await byId(wrapper, 'signin-cancel').trigger('click');
    await flushPromises();

    expect(byId(wrapper, 'builder-signin').exists()).toBe(false);
    expect(signIn).toMatchObject({ open: false, declined: true, needed: true });
    expect(builder.announcement).toBe(
      'Not signed in. Changes not saved yet stay in this browser.',
    );
    // The button that opened it was replaced while the dialog was open.
    expect(document.activeElement).toBe(byId(wrapper, 'signin-again').element);

    // Opened again, the password typed before is gone.
    await openFromNotice(wrapper);
    expect(password(wrapper).element.value).toBe('');
  });

  test('the right password signs in again, says so, and closes', async () => {
    const { wrapper, builder } = mountSignIn({ needed: true, declined: true });

    await openFromNotice(wrapper);
    axios.post.mockResolvedValueOnce({ data: { token: 'next' } });
    resumeBuilderSaves.mockResolvedValueOnce({ changes: 2 });
    await password(wrapper).setValue('right');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(phenix.state.renewLogin).toHaveBeenCalledWith({ token: 'next' });
    expect(resumeBuilderSaves).toHaveBeenCalledTimes(1);
    expect(builder.announcement).toBe('Signed in again. Saving your changes.');
    expect(wrapper.emitted('signed-in')).toHaveLength(1);
    expect(signIn).toMatchObject({ open: false, needed: false });
    expect(byId(wrapper, 'builder-signin').exists()).toBe(false);
    expect(byId(wrapper, 'builder-signin-notice').exists()).toBe(false);
  });
});
