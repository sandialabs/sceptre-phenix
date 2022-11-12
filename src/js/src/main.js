import Vue           from 'vue'
import VueResource   from 'vue-resource'
import Buefy         from 'buefy'
import VueNativeSock from 'vue-native-websocket'

import '@fortawesome/fontawesome-free/css/all.css'
import '@fortawesome/fontawesome-free/js/all.js'

import App    from './App.vue'
import router from './router'
import store  from './store'

import { errorNotification } from './components/utils.js'

import { fas }             from '@fortawesome/free-solid-svg-icons'
import { library }         from '@fortawesome/fontawesome-svg-core'
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome'

library.add(fas)

Vue.component( 'font-awesome-icon', FontAwesomeIcon )

Vue.config.productionTip = false

Vue.use(Buefy, {
  defaultIconComponent: 'font-awesome-icon',
	defaultIconPack:      'fa'
})

Vue.use( VueResource )
Vue.use( VueNativeSock, `//${location.host}${process.env.BASE_URL}`, { connectManually: true, reconnection: true } );

Vue.filter( 'lowercase', function( value ) {
  if ( value == null ) { return value }
  return value.toLowerCase()
})

Vue.filter( 'stringify', function( value ) {
  if ( value == null || value.length == 0)  {
    return 'none';
  }

  return value.join( ', ' );
})

Vue.filter( 'ram', function( value ) {
  if ( value == 0 ) {
    return '0 Byte';
  } else {
    let size   = ['MB', 'GB', 'TB'];
    let i      = parseInt( Math.floor( Math.log( value ) / Math.log( 1024 ) ) );
    let output = Math.round( value / Math.pow( 1024, i ), 2 ) + ' ' + size[i];

    return output;
  }
})

Vue.filter( 'uptime', function( value ) {
	var uptime = null;
	
  if ( value == null ) { 
    return value 
  } else {
    var seconds = parseInt( value, 10 );

    var days = Math.floor( seconds / ( 3600 * 24 ) );
    seconds -= days * 3600 * 24;
    var hrs  = Math.floor( seconds / 3600 );
    seconds -= hrs * 3600;
    var mnts = Math.floor( seconds / 60 );
    seconds -= mnts * 60;
    if ( days >= 1 ) {
			uptime = days + " days, " + ( '0' + hrs ).slice( -2 ) + ":" + ( '0' + mnts).slice( -2 ) + ":" + ( '0' + seconds ).slice( -2 );
    } else {
			uptime = ( '0' + hrs ).slice( -2 ) + ":" + ( '0' + mnts ).slice( -2 ) + ":" + ( '0' + seconds ).slice( -2 );
    }
    return uptime;
  }
})

Vue.prototype.errorNotification = errorNotification;
Vue.errorNotification = errorNotification;

Vue.http.options.root = `${process.env.BASE_URL}api/v1/`

Vue.http.interceptors.push(
  request => {
    // If a token is present in the store (meaning a user is logged in), add the
    // token to the request's header.
    if ( store.state.token ) {
      request.headers.set( 'Authorization', 'bearer ' + store.state.token )
    }

    return response => {
      // If we ever get a 401 (Unauthorized) we should automatically redirect
      // the user to the login screen.
      if ( response.status === 401 ) {
        store.commit( 'LOGOUT' )
        router.replace( {name: 'signin'} )
      }
    }
  }
)

new Vue({
	router,
	store,
	render: h => h( App )
}).$mount( '#app' )
