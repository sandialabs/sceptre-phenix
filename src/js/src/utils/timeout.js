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
  }

  fetchAndStart() {
    let now = new Date();

    //don't update if we've updated in past hour
    if (this.data.enabled || this.time_set - now >= 60 * 60 * 1000) {
      return;
    }

    axiosInstance.get('settings/timeout').then((resp) => {
      this.data = resp.data;
      this.time_set = new Date();
      this.startLogoutTimer();
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
  logoutUser() {
    if (this.warnToast) {
      this.warnToast.close();
      this.warnToast = null;
    }
    axiosInstance.get('logout').then((resp) => {
      if (resp.status == 204) {
        const store = usePhenixStore();
        store.logout();
      }
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
