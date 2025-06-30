import { ref, computed } from 'vue';
import { defineStore } from 'pinia';
import router from '@/router';

export const usePhenixStore = defineStore('phenix', {
  state: () => ({
    username: localStorage.getItem('phenix.user'),
    token: localStorage.getItem('phenix.token'),
    role: JSON.parse(localStorage.getItem('phenix.role')),
    auth: localStorage.getItem('phenix.auth') === 'true',
    next: null,
    logs: [],
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

// import router from './router'
//
// export default new Vuex.Store({
//   state: {
//     username: localStorage.getItem( 'phenix.user' ),
//     token:    localStorage.getItem( 'phenix.token' ),
//     role:     JSON.parse(localStorage.getItem( 'phenix.role' )),
//     auth:     localStorage.getItem( 'phenix.auth' ) === 'true',
//     next:     null,
//
//     features: [],
//     options:  {},
//     logs:     [],
//
//     logs_max: 5000,
//   },
//
//   mutations: {
//     'LOGIN' ( state, { loginResponse, remember } ) {
//       state.username = loginResponse.user.username;
//       state.token    = loginResponse.token;
//       state.role     = loginResponse.user.role;
//       state.auth     = true;
//
//       if ( remember ) {
//         localStorage.setItem( 'phenix.user',  state.username );
//         localStorage.setItem( 'phenix.token', state.token );
//         localStorage.setItem( 'phenix.role',  JSON.stringify(state.role) );
//         localStorage.setItem( 'phenix.auth',  state.auth );
//       }
//
//       if ( state.role.name === "VM Viewer" ) {
//         router.replace( {name: 'vmtiles'} );
//       } else if ( state.role.name === "Disabled" ) {
//         router.replace( {name: 'disabled'} );
//       } else if ( state.next && state.next.name !== 'signin' ) {
//         router.replace( state.next );
//         state.next = null;
//       } else {
//         router.replace( {name: 'home'} )
//       }
//     },
//
//     'LOGOUT' ( state ) {
//       state.username = null;
//       state.token    = null;
//       state.role     = null;
//       state.auth     = false;
//       state.next     = null;
//
//       localStorage.removeItem( 'phenix.user' );
//       localStorage.removeItem( 'phenix.token' );
//       localStorage.removeItem( 'phenix.role' );
//       localStorage.removeItem( 'phenix.auth' );
//
//       router.replace( '/signin' );
//     },
//
//     'NEXT' ( state, to ) {
//       state.next = to;
//     },
//
//     'FEATURES' ( state, features ) {
//       state.features = features;
//     },
//
//     'OPTIONS' ( state, options ) {
//       state.options = options;
//     },
//
//     'LOG' ( state, log ) {
//       let count = state.logs.unshift(log);
//
//       if ( count > state.logs_max ) {
//         state.logs.length = state.logs_max;
//       }
//     },
//
//     'LOGS' ( state, logs ) {
//       let count = state.logs.unshift(...logs.reverse());
//
//       if ( count > state.logs_max ) {
//         state.logs.length = state.logs_max;
//       }
//     },
//
//     'MAX_LOGS' ( state, max ) {
//       state.logs_max = max;
//     }
//   },
//
//   getters: {
//     username: state => {
//       return state.username;
//     },
//
//     token: state => {
//       return state.token;
//     },
//
//     role: state => {
//       return state.role;
//     },
//
//     auth: state => {
//       return state.auth;
//     },
//
//     features: state => {
//       return state.features;
//     },
//
//     options: state => {
//       return state.options;
//     },
//
//     logs: state => {
//       return state.logs.toReversed();
//     }
//   }
// });
