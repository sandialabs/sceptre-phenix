import { createRouter, createWebHistory } from 'vue-router';

import { ToastProgrammatic as Toast } from 'buefy';

import { usePhenixStore } from '@/store.js';
import axiosInstance from '@/utils/axios.js';

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'home',
      redirect: () => {
        const phenixStore = usePhenixStore();
        if (phenixStore.auth && phenixStore.role.name === 'VM Viewer') {
          return { name: 'vmtiles' };
        }
        return { name: 'experiments' };
      },
    },
    {
      path: '/signin',
      name: 'signin',
      component: () => import('@/views/SignIn.vue'),
    },
    {
      path: '/experiments',
      name: 'experiments',
      component: () => import('@/views/Experiments.vue'),
    },
    {
      path: '/experiment/:id',
      name: 'experiment',
      component: () => import('@/views/experiment/BaseView.vue'),
    },
    {
      path: '/hosts',
      name: 'hosts',
      component: () => import('@/views/Hosts.vue'),
    },
    {
      path: '/configs/',
      name: 'configs',
      component: () => import('@/views/Configs.vue'),
    },
    {
      path: '/disks/',
      name: 'disks',
      component: () => import('@/views/Disks.vue'),
    },
    {
      path: '/vmtiles',
      name: 'vmtiles',
      component: () => import('@/views/experiment/VMtilesView.vue'),
    },
    {
      path: '/users',
      name: 'users',
      component: () => import('@/views/Users.vue'),
    },
    { path: '/log', name: 'log', component: () => import('@/views/Logs.vue') },
    {
      path: '/console',
      name: 'console',
      component: () => import('@/views/Console.vue'),
    },
    {
      path: '/scorch',
      name: 'scorch',
      component: () => import('@/views/Scorch.vue'),
    },
    {
      path: '/scorch/:id',
      name: 'scorchruns',
      component: () => import('@/views/ScorchRuns.vue'),
    },
    {
      path: '/soh/:id',
      name: 'soh',
      component: () => import('@/views/StateOfHealth.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/Settings.vue'),
    },
    {
      path: '/tunneler',
      name: 'tunneler',
      component: () => import('@/views/Tunneler.vue'),
    },

    {path: '/proxysignup', name: 'proxysignup', component: () => import('@/views/ProxySignUp.vue'), props: true},

    //static paths
    { path: '/builder?token=:token', name: 'builder' },
    { path: '/version', name: 'version' },
    {path: '/features',             name: 'features'},
    {path: '/api/v1/options',       name: 'options'},

    //file, vnc
    {
      path: '/api/v1/experiments/:id/files/:name?path=:path&token=:token',
      name: 'file',
    },
    { path: '/api/v1/experiments/:id/vms/:name/vnc?token=:token', name: 'vnc' },

    //console paths
    { path: '/api/v1/console/:pid/ws', name: 'console-ws' },
    { path: '/api/v1/console/:pid/size', name: 'console-size' },

    //tunneler paths
    {path: '/downloads/tunneler/phenix-tunneler-linux-amd64',       name: 'linux-tunneler'},
    {path: '/downloads/tunneler/phenix-tunneler-darwin-arm64',      name: 'macos-arm-tunneler'},
    {path: '/downloads/tunneler/phenix-tunneler-darwin-amd64',      name: 'macos-intel-tunneler'},
    {path: '/downloads/tunneler/phenix-tunneler-windows-amd64.exe', name: 'windows-tunneler'},

  ],
});

router.beforeEach(async (to, from, next) => {
  const store = usePhenixStore();

  if (import.meta.env.VITE_AUTH === 'disabled' || !import.meta.env.VITE_AUTH) {
    if (!store.auth) {
      let role = {
        name: 'Global Admin',
        policies: [
          {
            resources: ['*', '*/*'],
            resourceNames: ['*', '*/*'],
            verbs: ['*'],
          },
        ],
      };

      let loginResponse = {
        token: 'authorized',
        user: {
          username: 'global-admin',
          role,
        },
      };
      store.login(loginResponse, false);
    }
    next();
    return;
  }

  if (to.name === 'disabled') {
    next();
    return;
  }

  if (to.name === 'signin' && import.meta.env.VITE_AUTH === 'enabled') {
    next();
    return;
  }

  if (to.name === 'proxysignup' && import.meta.env.VITE_AUTH === 'proxy') {
    next();
    return;
  }

  if (store.auth) {
    if (store.role.name === 'Disabled') {
      router.replace('/disabled');
    } else if (to.name === 'signin') {
      // No need to go to the signin route if already authorized.
      router.replace('/');
    } else if (
      Date.now() >=
      JSON.parse(atob(store.token.split('.')[1])).exp * 1000
    ) {
      // handle expired JWT by logging user out: https://stackoverflow.com/a/69058154
      new Toast().open({
        message: `Token is expired. Log in again`,
        type: 'is-warning',
        duration: 5000,
      });
      store.logout();
      router.replace('/signin');
    }

    next();
    return;
  } else {
    store.next = to;

    if (import.meta.env.VITE_AUTH === 'proxy') {
      // next(); //TODO
      // return;

      axiosInstance.get('login')
        .then((response) => {
          store.commit('LOGIN', )
          store.login(response.data, false)
          next();
        })
        .catch((err) => {
          next({name: 'proxysignup', params: {'username': err.body.trim()}})
        })

      // try {
      //   let resp = await Vue.http.get('login');
      //   let loginResponse = await resp.json();
      //
      //   store.commit( 'LOGIN', { loginResponse, "remember": false } );
      // } catch (resp) {
      //   if ( resp.status === 404 ) {
      //     next( {name: 'proxysignup', params: {'username': resp.body.trim()}} )
      //   } else {
      //     // TODO: ???
      //   }
      // }
    } else {
      next({ name: 'signin' });
      return;
    }
  }
});
export default router;
