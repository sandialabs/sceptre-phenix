import axiosInstance from '@/utils/axios.js';
import { usePhenixStore } from '@/store.js';
import { ToastProgrammatic as Toast } from 'buefy';

const MINUTE = 60 * 1000;
// setTimeout runs a longer delay at once, so longer timeouts use this limit.
const MAX_TIMER_MS = 2 ** 31 - 1;
const LOGOUT_REQUEST_MS = 10 * 1000;

/**
 * When the idle timer warns and logs out, from the timeout settings. The
 * timeout defaults to 30 minutes. A warning longer than the timeout shows at
 * once, so the warning never comes late and logout never comes early. A
 * timeout longer than about 24.8 days uses that limit.
 */
export function logoutSchedule({ timeout_min, warning_min }) {
  let timeout = Number(timeout_min);
  if (!(timeout > 0)) {
    timeout = 30;
  }
  timeout = Math.min(timeout, MAX_TIMER_MS / MINUTE);

  let warning = Number(warning_min);
  if (!(warning > 0)) {
    warning = 0;
  }
  warning = Math.min(warning, timeout);

  return {
    warnAfterMs: (timeout - warning) * MINUTE,
    warnMinutes: warning,
    logoutAfterMs: timeout * MINUTE,
  };
}

export class TimeoutTool {
  constructor() {
    this.data = {
      enabled: false,
      timeout_min: 0,
      warning_min: 0,
    };
    this.time_set = 0; //keep track of last time set, update every 30 minutes

    this.logoutTimer = null;
    this.warnToast = null;
  }

  fetchAndStart() {
    let now = new Date();

    // don't re-fetch if we already updated within the past hour
    if (this.time_set && now - this.time_set < 60 * 60 * 1000) {
      return;
    }

    axiosInstance
      .get('settings/timeout')
      .then((resp) => {
        this.data = resp.data;
        this.time_set = new Date();
        this.startLogoutTimer();
      })
      .catch((err) => {
        // leave the timeout disabled if the settings can't be fetched
        console.warn('failed to fetch timeout settings', err);
      });
  }

  startLogoutTimer() {
    const store = usePhenixStore();
    if (!this.data.enabled || !store.auth) {
      return;
    }

    const { warnAfterMs, warnMinutes, logoutAfterMs } = logoutSchedule(
      this.data,
    );

    if (warnMinutes > 0) {
      this.logoutTimer = setTimeout(
        () => this.warnUser(warnMinutes),
        warnAfterMs,
      );
    } else {
      this.logoutTimer = setTimeout(() => this.logoutUser(), logoutAfterMs);
    }
  }

  warnUser(timeLeft) {
    var message = `Still there? Inactive auto log out in ${timeLeft} minutes.`;
    if (timeLeft == 1) {
      message = `Still there? Inactive auto log out in ${timeLeft} minute.`;
    }

    this.warnToast = new Toast().open({
      message: message,
      type: 'is-warning',
      indefinite: true,
    });

    this.logoutTimer = setTimeout(() => this.logoutUser(), timeLeft * MINUTE);
  }
  logoutUser() {
    if (this.warnToast) {
      this.warnToast.close();
      this.warnToast = null;
    }
    // The session ends here even if the server does not answer, because the
    // user was idle for the full timeout.
    return axiosInstance
      .get('logout', { timeout: LOGOUT_REQUEST_MS })
      .catch((err) => {
        console.warn('failed to log out on the server', err);
      })
      .finally(() => {
        usePhenixStore().logout();
      });
  }
  resetTimer() {
    if (!this.data.enabled) {
      return;
    }
    if (this.warnToast) {
      this.warnToast.close();
      this.warnToast = null;
    }
    clearTimeout(this.logoutTimer);
    this.startLogoutTimer();
  }
}
