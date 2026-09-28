// Every way the app logs out goes through the warning when Builder Flow
// holds changes the server does not have: the header's Logout (the app
// store), the idle timeout, and a token the server refuses.

import { beforeEach, describe, expect, test, vi } from 'vitest';
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

import { registerOpenDraft } from '@/builder/session.js';
import { tokenExpired, usePhenixStore } from '@/store.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { TimeoutTool } from '@/utils/timeout.js';

// A Storage for the app store's session keys.
function memoryStorage() {
  const map = new Map();

  return {
    get length() {
      return map.size;
    },
    key: (index) => [...map.keys()][index] ?? null,
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    removeItem: (key) => map.delete(key),
  };
}

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
      doc: { name: 'Lab' },
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
