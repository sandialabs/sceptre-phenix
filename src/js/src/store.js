import { defineStore } from 'pinia';
import router from '@/router';

export const usePhenixStore = defineStore('phenix', {
  state: () => ({
    username: localStorage.getItem('phenix.user'),
    token: localStorage.getItem('phenix.token'),
    role: JSON.parse(localStorage.getItem('phenix.role')),
    auth: localStorage.getItem('phenix.auth') === 'true',
    next: null,
    features: [],
  }),
  actions: {
    login(loginResponse, remember) {
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

      router.replace('/signin');
    },
  },
});
