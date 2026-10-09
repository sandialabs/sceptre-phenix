// Every way the app logs out goes through the warning when Builder
// holds changes the server does not have: the header's Logout (the app
// store), the idle timeout, and a token the server refuses. On the
// Builder's page, the user can sign in again there instead.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const http = vi.hoisted(() => ({ get: vi.fn() }));
const router = vi.hoisted(() => ({ replace: vi.fn() }));

vi.mock('@/utils/axios.js', () => ({ default: http }));
vi.mock('@/router', () => ({ default: router }));
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

import { registerOpenDraft, resumeBuilderSaves } from '@/builder/session.js';
import {
  declineSignIn,
  expiredNavigation,
  hostSignIn,
  requestSignIn,
  sessionEnded,
  signIn,
  signInAgain,
  signInError,
  signedIn,
} from '@/builder/signin.js';
import { useBuilderStore } from '@/builder/store.js';
import { tokenExpired, usePhenixStore } from '@/store.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { TimeoutTool } from '@/utils/timeout.js';

import { memoryStorage } from './fixtures.js';

let store;

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  vi.clearAllMocks();
  setActivePinia(createPinia());
  store = usePhenixStore();
  store.login(
    { token: 'token', user: { username: 'alice', role: { name: 'Admin' } } },
    false,
    false,
  );
  http.get.mockResolvedValue({ status: 204 });
});

// The Builder open on a draft with `pending` changes not sent, which no
// save can send.
function openDraft(pending, saveNow = vi.fn(async () => {})) {
  return registerOpenDraft({
    store: {
      readOnly: false,
      doc: { metadata: { name: 'Lab' } },
      autosave: { record: { key: 'alice::alice::d1', queue: Array(pending) } },
      saveNow,
    },
    editing: () => true,
    saveUnapplied: () => null,
    describe: () => '',
  });
}

describe('the app store', () => {
  test('with nothing unsent, Logout asks the server, then ends the session', async () => {
    await expect(store.requestLogout('manual')).resolves.toBe('logged-out');
    expect(http.get).toHaveBeenCalledWith('logout');
    expect(store.auth).toBe(false);
    expect(store.username).toBeNull();
    expect(router.replace).toHaveBeenCalledWith('/signin');
    expect(store.logoutWarning).toBeNull();
    expect(store.loggingOut).toBe(false);
  });

  test('an expired token ends the session without asking the server', async () => {
    await expect(store.requestLogout('expired')).resolves.toBe('logged-out');
    expect(http.get).not.toHaveBeenCalled();
    expect(store.auth).toBe(false);
  });

  test('a logout the server refuses keeps the session', async () => {
    http.get.mockResolvedValueOnce({ status: 200 });
    await expect(store.requestLogout('manual')).resolves.toBe('failed');
    expect(store.auth).toBe(true);

    http.get.mockRejectedValueOnce(new Error('Network Error'));
    await expect(store.requestLogout('manual')).resolves.toBe('failed');
    expect(store.auth).toBe(true);
    expect(router.replace).not.toHaveBeenCalled();
  });

  test('a token the server no longer knows ends the session too', async () => {
    http.get.mockRejectedValueOnce(
      Object.assign(new Error('Request failed with status code 401'), {
        response: { status: 401 },
      }),
    );
    await expect(store.requestLogout('manual')).resolves.toBe('logged-out');
    expect(store.auth).toBe(false);
  });

  test('with a change unsent, the warning waits in the store for an answer', async () => {
    const unregister = openDraft(1);

    try {
      const outcome = store.requestLogout('manual');

      await vi.waitFor(() => expect(store.logoutWarning).not.toBeNull());
      expect(store.logoutWarning).toMatchObject({
        reason: 'manual',
        changes: 1,
        canStay: true,
      });
      expect(store.loggingOut).toBe(true);
      expect(http.get).not.toHaveBeenCalled();

      store.answerLogoutWarning('logout');
      await expect(outcome).resolves.toBe('logged-out');
      expect(store.logoutWarning).toBeNull();
      expect(store.loggingOut).toBe(false);
      expect(http.get).toHaveBeenCalledWith('logout');
      expect(store.auth).toBe(false);
    } finally {
      unregister();
    }
  });
});

// A JWT that expires at `exp`, in seconds.
const jwt = (exp) => `header.${btoa(JSON.stringify({ exp }))}.signature`;

test('a token has expired when its claims say so; one that is no JWT never has', () => {
  expect(tokenExpired(jwt(1))).toBe(true);
  expect(tokenExpired(jwt(Date.now() / 1000 + 60))).toBe(false);
  expect(tokenExpired('authorized')).toBe(false);
  expect(tokenExpired(null)).toBe(false);
});

test('a token whose claims encode to base64url characters still expires', () => {
  // The server signs {exp, sub} as UTF-8 JSON. A name such as chloé then
  // encodes with _, which atob refuses until it is made base64 again.
  const utf8 = String.fromCharCode(
    ...new TextEncoder().encode(
      JSON.stringify({ exp: 1000000000, sub: 'chloé' }),
    ),
  );
  const claims = btoa(utf8)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
  expect(claims).toMatch(/[-_]/);
  expect(tokenExpired(`header.${claims}.signature`)).toBe(true);
});

test('an idle logout whose token has expired offers no way to stay', async () => {
  const unregister = openDraft(1);

  store.token = jwt(1);

  try {
    store.requestLogout('idle');
    await vi.waitFor(() => expect(store.logoutWarning).not.toBeNull());
    expect(store.logoutWarning).toMatchObject({
      reason: 'expired',
      canStay: false,
    });
    store.answerLogoutWarning('logout');
    await vi.waitFor(() => expect(store.auth).toBe(false));
    expect(http.get).not.toHaveBeenCalled();
  } finally {
    unregister();
  }
});

test('a logout the user asks for once the token expired sends nothing, offers no Stay and waits', async () => {
  vi.useFakeTimers();

  const saveNow = vi.fn(async () => {});
  const unregister = openDraft(1, saveNow);

  store.token = jwt(1);

  try {
    const outcome = store.requestLogout('manual');

    await vi.advanceTimersByTimeAsync(0);
    expect(store.logoutWarning).toMatchObject({
      reason: 'expired',
      canStay: false,
      secondsLeft: null,
      drafts: [{ key: 'alice::alice::d1', name: 'lab.json' }],
    });
    expect(saveNow).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    expect(store.auth).toBe(true);

    store.answerLogoutWarning('logout');
    await expect(outcome).resolves.toBe('logged-out');
    expect(http.get).not.toHaveBeenCalled();
    expect(store.auth).toBe(false);
  } finally {
    unregister();
    vi.useRealTimers();
  }
});

describe('the idle timeout', () => {
  function tool() {
    const timeout = new TimeoutTool();

    timeout.data = { enabled: true, timeout_min: 30, warning_min: 0 };

    return timeout;
  }

  test('logs out through the warning, and only Stay signed in or a failed logout starts it again', async () => {
    vi.useFakeTimers();

    try {
      let answer;
      const request = vi
        .spyOn(store, 'requestLogout')
        .mockReturnValue(new Promise((resolve) => (answer = resolve)));
      const timeout = tool();

      timeout.logoutUser();
      expect(request).toHaveBeenCalledWith('idle');

      // A click or a key in the warning does not start it again.
      timeout.resetTimer();
      expect(vi.getTimerCount()).toBe(0);

      answer('stayed');
      await vi.advanceTimersByTimeAsync(0);
      expect(vi.getTimerCount()).toBe(1);

      // A logout the server did not answer leaves the session; the next
      // timeout tries again.
      request.mockResolvedValue('failed');
      timeout.logoutUser();
      await vi.advanceTimersByTimeAsync(0);
      expect(vi.getTimerCount()).toBe(1);

      // After a logout it waits for the next sign-in.
      request.mockResolvedValue('logged-out');
      timeout.logoutUser();
      await vi.advanceTimersByTimeAsync(0);
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  test('does nothing once signed out', () => {
    const request = vi.spyOn(store, 'requestLogout');

    store.logout();
    tool().logoutUser();
    expect(request).not.toHaveBeenCalled();
  });
});

test('a token the server calls invalid logs out as an expired one does', async () => {
  const request = vi.spyOn(store, 'requestLogout').mockResolvedValue('stayed');

  await useErrorNotification({
    message: 'Request failed with status code 401',
    response: {
      status: 401,
      data: 'invalid token',
      headers: { get: () => 'text/plain' },
    },
  });
  expect(request).toHaveBeenCalledWith('expired');
});

// Builder asks for the password again in place when the session ends
// (see builder/signin.js): nothing logs out, and nothing is cleared.
describe('signing in again on the Builder page', () => {
  let unhost = () => {};

  // The Builder's page, open; `busy` while a logout is under way.
  function host({ available = true, busy = false } = {}) {
    unhost = hostSignIn({ available: () => available, busy: () => busy });
  }

  afterEach(() => {
    unhost();
  });

  test('a refused request opens it; once declined, only the user opens it again', () => {
    expect(sessionEnded()).toBe(false);
    expect(signIn.open).toBe(false);

    host();
    expect(sessionEnded()).toBe(true);
    expect(signIn).toMatchObject({ open: true, needed: true });

    declineSignIn();
    expect(sessionEnded()).toBe(false);
    expect(signIn).toMatchObject({ open: false, needed: true });

    expect(requestSignIn()).toBe(true);
    expect(signIn.open).toBe(true);

    // Leaving the Builder closes it.
    unhost();
    expect(signIn).toMatchObject({ open: false, needed: false });

    // Not while a logout is under way, nor without a password sign-in.
    host({ busy: true });
    expect(sessionEnded()).toBe(false);
    expect(requestSignIn()).toBe(false);
    unhost();
    host({ available: false });
    expect(requestSignIn()).toBe(false);
  });

  test('the router neither logs out nor leaves the page while it is open', () => {
    const builder = { name: 'builder' };
    const configs = { name: 'configs' };

    // Without the Builder, an expired token logs out as before.
    expect(expiredNavigation(builder, builder)).toBe('logout');

    host();
    // Within the Builder: the address changes, and the sign-in opens.
    expect(expiredNavigation(builder, builder)).toBe('go');
    expect(signIn.open).toBe(true);
    expect(expiredNavigation(configs, builder)).toBe('stay');

    // Declined: the Builder's own navigation still goes; leaving it logs
    // out, through the warning, which offers to sign in again.
    declineSignIn();
    expect(expiredNavigation(builder, builder)).toBe('go');
    expect(signIn.open).toBe(false);
    expect(expiredNavigation(configs, builder)).toBe('logout');
  });

  test('signing in keeps the new token where the last was kept, resumes the unsent queue at once, and clears nothing', async () => {
    localStorage.setItem('phenix.builder.user', 'alice');
    localStorage.setItem('phenix.builder.recent', '["palette.open"]');

    const retrySave = vi.fn(async () => {});
    const unregister = registerOpenDraft({
      store: {
        readOnly: false,
        saveState: { status: 'error', signInNeeded: true },
        autosave: { record: { key: 'k', queue: [{}, {}] } },
        retrySave,
      },
      editing: () => true,
      saveUnapplied: () => null,
      describe: () => '',
    });
    const post = vi.fn(async () => ({
      data: {
        token: 'renewed',
        user: { username: 'alice', role: { name: 'Admin' } },
      },
    }));

    host();
    sessionEnded();

    try {
      const { changes } = await signInAgain({
        username: store.username,
        password: 'secret',
        renew: (response) => store.renewLogin(response),
        resume: resumeBuilderSaves,
        post,
      });

      expect(post).toHaveBeenCalledWith({ user: 'alice', pass: 'secret' });
      expect(changes).toBe(2);
      expect(retrySave).toHaveBeenCalledOnce();
      expect(signIn).toMatchObject({ open: false, needed: false });
      expect(store).toMatchObject({ auth: true, token: 'renewed' });
      expect(sessionStorage.getItem('phenix.token')).toBe('renewed');
      // Remember me was not chosen: localStorage keeps no token.
      expect(localStorage.getItem('phenix.token')).toBeNull();
      expect(localStorage.getItem('phenix.builder.user')).toBe('alice');
      expect(localStorage.getItem('phenix.builder.recent')).not.toBeNull();
      expect(router.replace).not.toHaveBeenCalled();
    } finally {
      unregister();
    }
  });

  test('a wrong password, or a token for someone else, changes nothing', async () => {
    host();
    sessionEnded();

    const wrong = Object.assign(new Error('401'), {
      response: { status: 401, data: 'invalid creds' },
    });
    const refused = signInAgain({
      username: 'alice',
      password: 'wrong',
      renew: (response) => store.renewLogin(response),
      resume: vi.fn(),
      post: async () => {
        throw wrong;
      },
    });

    await expect(refused).rejects.toBe(wrong);
    expect(signInError(wrong)).toBe('The password is incorrect.');

    const bob = signInAgain({
      username: 'alice',
      password: 'secret',
      renew: (response) => store.renewLogin(response),
      resume: vi.fn(),
      post: async () => ({ data: { token: 'b', user: { username: 'bob' } } }),
    });

    await expect(bob).rejects.toMatchObject({ anotherUser: true });
    expect(store).toMatchObject({ username: 'alice', token: 'token' });
    expect(signIn).toMatchObject({ open: true, needed: true });
    expect(signInError(new Error('Network Error'))).toBe(
      'The server could not be reached. Check the connection and try again.',
    );
  });

  test('Retry saving opens it while the session is over, rather than sending the same token again', async () => {
    const builder = useBuilderStore();
    const retry = vi.fn(async () => ({ status: 'saved' }));

    builder.autosave = { retry, dispose() {} };
    builder.saveState = {
      ...builder.saveState,
      status: 'error',
      signInNeeded: true,
    };
    host();
    sessionEnded();
    declineSignIn();

    await builder.retrySave({ announce: true });
    expect(signIn.open).toBe(true);
    expect(retry).not.toHaveBeenCalled();

    // Signed in again: it sends.
    signedIn();
    await builder.retrySave();
    expect(retry).toHaveBeenCalledOnce();
  });
});
