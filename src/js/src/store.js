import { defineStore } from 'pinia';
import router from '@/router';
import { endBuilderSession, startBuilderSession } from '@/builder/session.js';

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
  }),
  actions: {
    login(loginResponse, remember, navigate = true) {
      // Another user can leave Builder data on this device when they close
      // the browser without a logout. That data goes before this user's
      // session starts. Its preferences stay, as at logout.
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

      // Builder's drafts, lists and recent commands on this device go too,
      // before the sign-in page shows. Its preferences stay.
      endBuilderSession();

      router.replace('/signin');
    },
  },
});
