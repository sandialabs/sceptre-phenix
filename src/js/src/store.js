import { defineStore } from 'pinia';
import router from '@/router';

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

      router.replace('/signin');
    },
  },
});
