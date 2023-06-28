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
    next:     null,

    features: [],
  },

  mutations: {
    'LOGIN' ( state, { loginResponse, remember } ) {
      state.username = loginResponse.user.username;
      state.token    = loginResponse.token;
      state.role     = loginResponse.user.role;
      state.auth     = true;

      if ( remember ) {
        localStorage.setItem( 'phenix.user',  state.username );
        localStorage.setItem( 'phenix.token', state.token );
        localStorage.setItem( 'phenix.role',  state.role );
        localStorage.setItem( 'phenix.auth',  state.auth );
      }

      if ( state.role === "VM Viewer" ) {
        router.replace( {name: 'vmtiles'} );
      } else if ( state.role === "Disabled" ) {
        router.replace( {name: 'disabled'} );
      } else if ( state.next && state.next.name !== 'signin' ) {
        router.replace( state.next );
        state.next = null;
      } else {
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
    },

    'FEATURES' ( state, features ) {
      state.features = features;
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
    },

    features: state => {
      return state.features;
    }
  }
});
