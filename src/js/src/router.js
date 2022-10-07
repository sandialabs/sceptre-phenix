import Vue    from 'vue'
import Router from 'vue-router'

import Configs       from './components/Configs.vue'
import Experiment    from './components/Experiment.vue'
import Experiments   from './components/Experiments.vue'
import Hosts         from './components/Hosts.vue'
import Log           from './components/Log.vue'
import Scorch        from './components/Scorch.vue'
import ScorchRuns    from './components/ScorchRuns.vue'
import SignIn        from './components/SignIn.vue'
import StateOfHealth from './components/StateOfHealth.vue'
import Users         from './components/Users.vue'
import VMtiles       from './components/VMtiles.vue'

import store from './store'

Vue.use(Router)

const router = new Router({
  mode: 'history',
  base: process.env.BASE_URL,
  routes: [
    { path: '/',                  name: 'home',        redirect: {name: 'experiments'} },
    { path: '/configs',           name: 'configs',     component: Configs },
    { path: '/experiments',       name: 'experiments', component: Experiments },
    { path: '/experiment/:id',    name: 'experiment',  component: Experiment },
    { path: '/hosts',             name: 'hosts',       component: Hosts },
    { path: '/log',               name: 'log',         component: Log },
    { path: '/scorch/:id',        name: 'scorchruns',  component: ScorchRuns },
    { path: '/scorch',            name: 'scorch',      component: Scorch },
    { path: '/signin',            name: 'signin',      component: SignIn },
    { path: '/stateofhealth/:id', name: 'soh',         component: StateOfHealth },
    { path: '/users',             name: 'users',       component: Users },
    { path: '/vmtiles',           name: 'vmtiles',     component: VMtiles },

    { path: '/version', name: 'version' },

    { path: '/api/v1/experiments/:id/files/:name?token=:token&path=:path', name: 'file' },
    { path: '/api/v1/experiments/:id/vms/:name/vnc?token=:token',          name: 'vnc' },

    { path: '*', redirect: {name: 'signin'} }
  ]
})

router.beforeEach( ( to, from, next ) => {
  if ( process.env.VUE_APP_AUTH === 'disabled' ) {
    if ( !store.getters.auth ) {
      store.commit( 'LOGIN', { 'user': { 'token': '', 'role': 'Global Admin' }, 'remember': false } )
    }

    next()
    return
  }

  let pub = ['/signup', '/signin']

  if ( pub.includes( to.path ) ) {
    next()
    return
  }

  if ( store.getters.auth ) {
    next()
  } else {
    store.commit( 'NEXT', to )
    next( {name: 'signin'} )
  }
})

export default router
