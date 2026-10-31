import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { logoutSchedule, TimeoutTool } from '@/utils/timeout.js';
import axiosInstance from '@/utils/axios.js';

// Vitest hoists vi.mock above the imports.
const store = vi.hoisted(() => ({ auth: true, logout: vi.fn() }));
const toast = vi.hoisted(() => ({ close: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));
vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open() {
      return toast;
    }
  },
}));

const MINUTE = 60 * 1000;

describe('logoutSchedule', () => {
  test.each([
    ['a warning before the timeout', 30, 3, 27, 3, 30],
    ['no warning', 30, 0, 30, 0, 30],
    ['a negative warning, as no warning', 30, -5, 30, 0, 30],
    ['a timeout of 0, as 30 minutes', 0, 3, 27, 3, 30],
    ['a missing timeout, as 30 minutes', undefined, 3, 27, 3, 30],
    ['half minutes', 1.5, 0.5, 1, 0.5, 1.5],
    ['a warning equal to the timeout, at once', 10, 10, 0, 10, 10],
    ['a warning longer than the timeout, at once', 10, 15, 0, 10, 10],
    [
      'a timeout longer than the timer limit, at the limit',
      60 * 24 * 30,
      0,
      (2 ** 31 - 1) / MINUTE,
      0,
      (2 ** 31 - 1) / MINUTE,
    ],
  ])(
    'schedules %s',
    (_, timeout_min, warning_min, warnAt, warnMinutes, logoutAt) => {
      expect(logoutSchedule({ timeout_min, warning_min })).toEqual({
        warnAfterMs: warnAt * MINUTE,
        warnMinutes,
        logoutAfterMs: logoutAt * MINUTE,
      });
    },
  );

  test('never schedules a warning in the past', () => {
    for (let warning_min = 0; warning_min <= 40; warning_min += 0.5) {
      const s = logoutSchedule({ timeout_min: 20, warning_min });
      expect(s.warnAfterMs).toBeGreaterThanOrEqual(0);
      expect(s.warnAfterMs + s.warnMinutes * MINUTE).toBe(s.logoutAfterMs);
    }
  });
});

describe('TimeoutTool', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    store.auth = true;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function tool(data) {
    const t = new TimeoutTool();
    t.data = { enabled: true, ...data };
    return t;
  }

  test('a warning longer than the timeout does not log out at once', async () => {
    axiosInstance.get.mockResolvedValue({ status: 204 });
    const t = tool({ timeout_min: 10, warning_min: 15 });

    t.startLogoutTimer();
    await vi.advanceTimersByTimeAsync(0);
    expect(t.warnToast).toBe(toast);
    expect(store.logout).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(10 * MINUTE - 1);
    expect(store.logout).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(axiosInstance.get).toHaveBeenCalledWith('logout', {
      timeout: 10000,
    });
    expect(store.logout).toHaveBeenCalledOnce();
    expect(toast.close).toHaveBeenCalledOnce();
  });

  test('warns, then logs out at the timeout', async () => {
    axiosInstance.get.mockResolvedValue({ status: 204 });
    const t = tool({ timeout_min: 30, warning_min: 3 });

    t.startLogoutTimer();
    await vi.advanceTimersByTimeAsync(27 * MINUTE - 1);
    expect(t.warnToast).toBeNull();

    await vi.advanceTimersByTimeAsync(1);
    expect(t.warnToast).toBe(toast);

    await vi.advanceTimersByTimeAsync(3 * MINUTE);
    expect(store.logout).toHaveBeenCalledOnce();
  });

  test('ends the session even if the server does not answer the logout', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    axiosInstance.get.mockRejectedValue(new Error('Network Error'));
    const t = tool({ timeout_min: 1, warning_min: 0 });

    t.startLogoutTimer();
    await vi.advanceTimersByTimeAsync(MINUTE);

    expect(store.logout).toHaveBeenCalledOnce();
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  test('does nothing while signed out or turned off', async () => {
    store.auth = false;
    tool({ timeout_min: 1 }).startLogoutTimer();
    store.auth = true;
    tool({ enabled: false, timeout_min: 1 }).startLogoutTimer();

    await vi.advanceTimersByTimeAsync(2 * MINUTE);
    expect(axiosInstance.get).not.toHaveBeenCalled();
  });
});
