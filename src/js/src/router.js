import Vue    from 'vue'
import Router from 'vue-router'

import Configs       from './components/Configs.vue'
import Disabled      from './components/Disabled.vue'
import Experiment    from './components/Experiment.vue'
import Experiments   from './components/Experiments.vue'
import Hosts         from './components/Hosts.vue'
import Log           from './components/Log.vue'
import ProxySignUp   from './components/ProxySignUp.vue'
import Scorch        from './components/Scorch.vue'
import ScorchRuns    from './components/ScorchRuns.vue'
import SignIn        from './components/SignIn.vue'
import StateOfHealth from './components/StateOfHealth.vue'
import Users         from './components/Users.vue'
import VMtiles       from './components/VMtiles.vue'
import MiniConsole   from './components/MiniConsole.vue'
import Tunneler      from './components/Tunneler.vue'

import store from './store'

Vue.use(Router)

const router = new Router({
  mode: 'history',
  base: process.env.BASE_URL,
  routes: [
    {path: '/',                  name: 'home',        redirect:  {name: 'experiments'}},
    {path: '/configs',           name: 'configs',     component: Configs},
    {path: '/disabled',          name: 'disabled',    component: Disabled},
    {path: '/experiments',       name: 'experiments', component: Experiments},
    {path: '/experiment/:id',    name: 'experiment',  component: Experiment},
    {path: '/hosts',             name: 'hosts',       component: Hosts},
    {path: '/log',               name: 'log',         component: Log},
    {path: '/scorch/:id',        name: 'scorchruns',  component: ScorchRuns},
    {path: '/scorch',            name: 'scorch',      component: Scorch},
    {path: '/signin',            name: 'signin',      component: SignIn},
    {path: '/stateofhealth/:id', name: 'soh',         component: StateOfHealth},
    {path: '/users',             name: 'users',       component: Users},
    {path: '/vmtiles',           name: 'vmtiles',     component: VMtiles},
    {path: '/console',           name: 'console',     component: MiniConsole},
    {path: '/tunneler',          name: 'tunneler',    component: Tunneler},

    {path: '/builder?token=:token', name: 'builder'},
    {path: '/version',              name: 'version'},
    {path: '/features',             name: 'features'},

    {path: '/api/v1/console/:pid/ws',   name: 'console-ws'},
    {path: '/api/v1/console/:pid/size', name: 'console-size'},

    {path: '/api/v1/experiments/:id/files/:name\\?path=:path&token=:token', name: 'file'},
    {path: '/api/v1/experiments/:id/vms/:name/vnc?token=:token',            name: 'vnc'},

    {path: '/downloads/tunneler/phenix-tunneler-linux-amd64',       name: 'linux-tunneler'},
    {path: '/downloads/tunneler/phenix-tunneler-darwin-arm64',      name: 'macos-tunneler'},
    {path: '/downloads/tunneler/phenix-tunneler-windows-amd64.exe', name: 'windows-tunneler'},

    {path: '/proxysignup', name: 'proxysignup', component: ProxySignUp, props: true},

    {path: '*', redirect: {name: 'signin'}}
  ]
})

router.beforeEach( async ( to, from, next ) => {
  if ( process.env.VUE_APP_AUTH === 'disabled' ) {
    if ( !store.getters.auth ) {
      let role = {
        name: "Global Admin",
        policies: [{
          "resources": ["*", "*/*"],
          "resourceNames": ["*", "*/*"],
          "verbs": ["*"]
        }]
      }

      let loginResponse = {
        'token': 'authorized',
        'user': {
          'username': 'global-admin',
          role,
        }
      }

      store.commit( 'LOGIN', { loginResponse, 'remember': false } )
    }

    next()
    return
  }

  if ( to.name === 'disabled' ) {
    next()
    return
  }

  if ( to.name === 'signin' && process.env.VUE_APP_AUTH === 'enabled' ) {
    next()
    return
  }

  if ( to.name === 'proxysignup' && process.env.VUE_APP_AUTH === 'proxy' ) {
    next()
    return
  }

  if ( store.getters.auth ) {
    if ( store.getters.role === 'Disabled' ) {
      router.replace( '/disabled' );
    }

    // No need to go to the signin route if already authorized.
    if ( to.name === 'signin' ) {
      router.replace( '/' );
    }

    next()
  } else {
    store.commit( 'NEXT', to )

    if ( process.env.VUE_APP_AUTH === 'proxy' ) {
      try {
        let resp = await Vue.http.get('login');
        let loginResponse = await resp.json();

        store.commit( 'LOGIN', { loginResponse, "remember": false } );
      } catch (resp) {
        if ( resp.status === 404 ) {
          next( {name: 'proxysignup', params: {'username': resp.body.trim()}} )
        } else {
          // TODO: ???
        }
      }
    } else {
      next( {name: 'signin'} )
    }
  }
})

export default router
