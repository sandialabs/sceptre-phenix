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
      meta: { title: 'Sign in' },
      component: () => import('@/views/SignIn.vue'),
    },
    {
      path: '/experiments',
      name: 'experiments',
      meta: { title: 'Experiments' },
      component: () => import('@/views/Experiments.vue'),
    },
    {
      path: '/experiment/:id',
      name: 'experiment',
      meta: { title: 'Experiment' },
      component: () => import('@/views/experiment/Base.vue'),
    },
    {
      path: '/hosts',
      name: 'hosts',
      meta: { title: 'Hosts' },
      component: () => import('@/views/Hosts.vue'),
    },
    {
      path: '/configs/',
      name: 'configs',
      meta: { title: 'Configs' },
      component: () => import('@/views/Configs.vue'),
    },
    {
      path: '/disks/',
      name: 'disks',
      meta: { title: 'Disks' },
      component: () => import('@/views/Disks.vue'),
    },
    {
      path: '/vmtiles',
      name: 'vmtiles',
      meta: { title: 'VM tiles' },
      component: () => import('@/views/experiment/VMtilesView.vue'),
    },
    {
      path: '/users',
      name: 'users',
      meta: { title: 'Users' },
      component: () => import('@/views/Users.vue'),
    },
    {
      path: '/log',
      name: 'log',
      meta: { title: 'Logs' },
      component: () => import('@/views/Logs.vue'),
    },
    {
      path: '/console',
      name: 'console',
      meta: { title: 'Console' },
      component: () => import('@/views/Console.vue'),
    },
    {
      path: '/scorch',
      name: 'scorch',
      meta: { title: 'SCORCH' },
      component: () => import('@/views/Scorch.vue'),
    },
    {
      path: '/scorch/:id',
      name: 'scorchruns',
      meta: { title: 'SCORCH runs' },
      component: () => import('@/views/ScorchRuns.vue'),
    },
    {
      path: '/soh/:id',
      name: 'soh',
      meta: { title: 'State of health' },
      component: () => import('@/views/StateOfHealth.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      meta: { title: 'Settings' },
      component: () => import('@/views/Settings.vue'),
    },
    {
      path: '/tunneler',
      name: 'tunneler',
      meta: { title: 'Tunneler' },
      component: () => import('@/views/Tunneler.vue'),
    },

    {
      // username must be a path param: Vue Router 4 drops params that are
      // not part of the route path (passing them worked in Vue Router 3)
      path: '/proxysignup/:username?',
      name: 'proxysignup',
      meta: { title: 'Sign up' },
      component: () => import('@/views/ProxySignUp.vue'),
      props: true,
    },
    {
      path: '/disabled',
      name: 'disabled',
      meta: { title: 'Account disabled' },
      component: () => import('@/views/Disabled.vue'),
    },

    //static paths
    { path: '/builder?token=:token', name: 'builder' },
    { path: '/version', name: 'version' },
    { path: '/features', name: 'features' },
    { path: '/api/v1/options', name: 'options' },

    //file, vnc
    {
      path: '/api/v1/experiments/:id/files/:name',
      name: 'file',
    },
    { path: '/api/v1/experiments/:id/vms/:name/vnc?token=:token', name: 'vnc' },

    //console paths
    { path: '/api/v1/console/:pid/ws', name: 'console-ws' },

    //tunneler paths
    {
      path: '/downloads/tunneler/phenix-tunneler-linux-amd64',
      name: 'linux-tunneler',
    },
    {
      path: '/downloads/tunneler/phenix-tunneler-darwin-arm64',
      name: 'macos-arm-tunneler',
    },
    {
      path: '/downloads/tunneler/phenix-tunneler-darwin-amd64',
      name: 'macos-intel-tunneler',
    },
    {
      path: '/downloads/tunneler/phenix-tunneler-windows-amd64.exe',
      name: 'windows-tunneler',
    },
  ],
});

router.beforeEach(async (to, _, next) => {
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
      store.login(loginResponse, false, false);
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
    }

    next();
    return;
  } else {
    store.next = to;

    if (import.meta.env.VITE_AUTH === 'proxy') {
      // next(); //TODO
      // return;

      axiosInstance
        .get('login')
        .then((response) => {
          store.login(response.data, false);
          next();
        })
        .catch((err) => {
          next({
            name: 'proxysignup',
            params: { username: (err.response?.data ?? '').trim() },
          });
        });
    } else {
      next({ name: 'signin' });
      return;
    }
  }
});
// Give every route its own document title (WCAG 2.4.2 Page Titled).
router.afterEach((to) => {
  document.title = to.meta?.title ? `${to.meta.title} - phēnix` : 'phēnix';
});

export default router;
