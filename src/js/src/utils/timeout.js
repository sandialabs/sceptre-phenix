import axiosInstance from '@/utils/axios.js';
import { usePhenixStore } from '@/store.js';
import { ToastProgrammatic as Toast } from 'buefy';

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
    // Set while the idle logout waits on its warning (see logoutUser).
    this.loggingOut = false;
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

    var timeout = this.data.timeout_min;
    const warning = this.data.warning_min;

    if (timeout <= 0) {
      timeout = 30;
    }

    if (warning > 0) {
      const diff = timeout - warning;
      this.logoutTimer = setTimeout(
        () => this.warnUser(warning),
        1000 * 60 * diff,
      );
    } else {
      this.logoutTimer = setTimeout(
        () => this.logoutUser(),
        1000 * 60 * timeout,
      );
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

    this.logoutTimer = setTimeout(
      () => this.logoutUser(),
      1000 * 60 * timeLeft,
    );
  }
  // Logs out, or first warns for a minute when Builder v2 holds changes
  // the server does not have (see utils/logout.js). Activity meanwhile does
  // not restart the timer; Stay signed in in the warning does.
  logoutUser() {
    if (this.warnToast) {
      this.warnToast.close();
      this.warnToast = null;
    }

    const store = usePhenixStore();

    if (!store.auth) {
      return;
    }

    clearTimeout(this.logoutTimer);
    this.loggingOut = true;
    store
      .requestLogout('idle')
      .then((outcome) => {
        this.loggingOut = false;

        // Still signed in: the user stayed, or the server did not answer
        // the logout, which the next timeout tries again.
        if (outcome === 'stayed' || outcome === 'failed') {
          this.resetTimer();
        }
      })
      .catch(() => {
        this.loggingOut = false;
      });
  }
  resetTimer() {
    if (!this.data.enabled || this.loggingOut) {
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
