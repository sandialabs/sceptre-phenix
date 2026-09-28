import { defineStore } from 'pinia';
import router from '@/router';
import {
  endBuilderSession,
  startBuilderSession,
  unsentBuilderWork,
} from '@/builder/session.js';
import axiosInstance from '@/utils/axios.js';
import { createLogoutFlow } from '@/utils/logout.js';

/**
 * Whether the token says it has expired; a token that is no JWT (without
 * authentication) never does.
 *
 * @param {string|null} token
 * @returns {boolean}
 */
export function tokenExpired(token) {
  try {
    return Date.now() >= JSON.parse(atob(token.split('.')[1])).exp * 1000;
  } catch {
    return false;
  }
}

// Logging out warns first while Builder Flow holds changes the server does
// not have (see utils/logout.js).
const logoutFlow = createLogoutFlow({
  findUnsent: ({ send }) =>
    unsentBuilderWork({ username: usePhenixStore().username, send }),
  async finish(reason) {
    // The server forgets the token first; it no longer knows an expired
    // one, and says so (401) when asked.
    if (reason !== 'expired') {
      const status = await axiosInstance.get('logout').then(
        (response) => response.status,
        (error) => error.response?.status,
      );

      if (status !== 204 && status !== 401) {
        return false;
      }
    }

    usePhenixStore().logout();

    return true;
  },
  show(warning) {
    usePhenixStore().logoutWarning = warning;
  },
  busy(busy) {
    usePhenixStore().loggingOut = busy;
  },
});

export const usePhenixStore = defineStore('phenix', {
  state: () => ({
    username:
      localStorage.getItem('phenix.user') ||
      sessionStorage.getItem('phenix.user'),
    token:
      localStorage.getItem('phenix.token') ||
      sessionStorage.getItem('phenix.token'),
    role:
      JSON.parse(localStorage.getItem('phenix.role')) ||
      JSON.parse(sessionStorage.getItem('phenix.role')),
    auth:
      localStorage.getItem('phenix.auth') === 'true' ||
      sessionStorage.getItem('phenix.auth') === 'true',
    next: null,
    features: [],
    featuresLoaded: false,
    featuresPromise: null,
    featuresError: null,
    // The warning shown before a logout would delete Builder Flow changes
    // the server does not have, and whether a logout is under way.
    logoutWarning: null,
    loggingOut: false,
  }),
  actions: {
    // Single-flight fetch of /features so feature-gated routes can await a
    // deterministic answer instead of racing App.vue's initial request.
    ensureFeatures(fetchImpl) {
      if (this.featuresLoaded) {
        return Promise.resolve(this.features);
      }

      if (!this.featuresPromise) {
        const doFetch =
          fetchImpl ||
          (() =>
            fetch(router.resolve({ name: 'features' }).href).then((resp) => {
              if (!resp.ok) {
                throw new Error(
                  `feature request failed with status ${resp.status}`,
                );
              }

              return resp.json();
            }));

        this.featuresPromise = Promise.resolve()
          .then(doFetch)
          .then((data) => {
            if (!Array.isArray(data?.features)) {
              throw new Error('feature response does not contain a list');
            }

            this.features = data.features;
            this.featuresLoaded = true;
            this.featuresError = null;
            return this.features;
          })
          .catch((error) => {
            this.featuresError =
              error instanceof Error ? error.message : String(error);
            this.featuresPromise = null;
            throw error;
          });
      }

      return this.featuresPromise;
    },

    login(loginResponse, remember, navigate = true) {
      // Builder Flow data another user left on this device, by closing the
      // browser without logging out, goes before this user's session
      // starts; its preferences stay, as at logout.
      startBuilderSession(loginResponse.user.username);

      this.username = loginResponse.user.username;
      this.token = loginResponse.token;
      this.role = loginResponse.user.role;
      this.auth = true;

      if (remember) {
        localStorage.setItem('phenix.user', this.username);
        localStorage.setItem('phenix.token', this.token);
        localStorage.setItem('phenix.role', JSON.stringify(this.role));
        localStorage.setItem('phenix.auth', this.auth);
      }

      sessionStorage.setItem('phenix.user', this.username);
      sessionStorage.setItem('phenix.token', this.token);
      sessionStorage.setItem('phenix.role', JSON.stringify(this.role));
      sessionStorage.setItem('phenix.auth', this.auth);

      if (!navigate) {
        return;
      }

      if (this.role.name === 'VM Viewer') {
        router.replace({ name: 'vmtiles' });
      } else if (this.role.name === 'Disabled') {
        router.replace({ name: 'disabled' });
      } else if (this.next && this.next.name !== 'signin') {
        router.replace(this.next);
        this.next = null;
      } else {
        router.replace({ name: 'home' });
      }
    },
    /**
     * Logs out: the header's Logout, the idle timeout, and an expired or
     * refused token. When Builder Flow holds changes the server does not
     * have, a warning comes first (see utils/logout.js).
     *
     * @param {'manual'|'idle'|'expired'} [reason]
     * @returns {Promise<'logged-out'|'stayed'|'failed'>}
     */
    requestLogout(reason = 'manual') {
      // A session whose token has expired cannot stay signed in, or send
      // anything, whatever ends it. A logout the user asked for still
      // waits for an answer.
      return logoutFlow.request(tokenExpired(this.token) ? 'expired' : reason, {
        countdown: reason !== 'manual',
      });
    },

    /** @param {'stay'|'logout'} choice the warning's answer */
    answerLogoutWarning(choice) {
      logoutFlow.answer(choice);
    },

    // Ends the session at once; requestLogout comes here once it may.
    logout() {
      this.username = null;
      this.token = null;
      this.role = null;
      this.auth = false;

      localStorage.removeItem('phenix.user');
      localStorage.removeItem('phenix.token');
      localStorage.removeItem('phenix.role');
      localStorage.removeItem('phenix.auth');

      sessionStorage.removeItem('phenix.user');
      sessionStorage.removeItem('phenix.token');
      sessionStorage.removeItem('phenix.role');
      sessionStorage.removeItem('phenix.auth');

      // Builder Flow's drafts, lists and recent commands on this device go
      // too, before the sign-in page shows; its preferences stay (every
      // logout, including the idle timeout's and an expired token's, comes
      // through here, after requestLogout).
      endBuilderSession();

      router.replace('/signin');
    },
  },
});
