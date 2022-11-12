import Vue    from 'vue'
import Vuex   from 'vuex'
import router from './router'

Vue.use( Vuex )

export default new Vuex.Store({
  state: {
    username: localStorage.getItem( 'phenix.user' ),
    token:    localStorage.getItem( 'phenix.token' ),
    role:     localStorage.getItem( 'phenix.role' ),
    auth:     localStorage.getItem( 'phenix.auth' ) === 'true',
    next:     null
  },

  mutations: {
    'LOGIN' ( state, { user, remember } ) {
      state.username = user.username;
      state.token    = user.token;
      state.role     = user.role;
      state.auth     = true;

      if ( remember ) {
        localStorage.setItem( 'phenix.user',  state.username );
        localStorage.setItem( 'phenix.token', state.token );
        localStorage.setItem( 'phenix.role',  state.role );
        localStorage.setItem( 'phenix.auth',  state.auth );
      }

      if ( state.role === "VM Viewer" ) {
        router.replace( {name: 'vmtiles'} );
      } else if ( state.next ) {
        router.replace( state.next );
        state.next = null;
      } else if ( router.currentRoute.path === "/signin" ) {
        router.replace( {name: 'home'} )
      }
    },

    'LOGOUT' ( state ) {
      state.username = null;
      state.token    = null;
      state.role     = null;
      state.auth     = false;
      state.next     = null;

      localStorage.removeItem( 'phenix.user' );
      localStorage.removeItem( 'phenix.token' );
      localStorage.removeItem( 'phenix.role' );
      localStorage.removeItem( 'phenix.auth' );

      router.replace( '/signin' );
    },

    'NEXT' ( state, to ) {
      state.next = to;
    }
  },
  
  getters: {
    username: state => {
      return state.username;
    },

    token: state => {
      return state.token;
    },
    
    role: state => {
      return state.role;
    },
    
    auth: state => {
      return state.auth;
    }
  }
});
