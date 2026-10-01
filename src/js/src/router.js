import { createRouter, createWebHistory } from 'vue-router';

import {
  SnackbarProgrammatic as Snackbar,
  ToastProgrammatic as Toast,
} from 'buefy';

import { expiredNavigation, signIn } from '@/builder/signin.js';
import { tokenExpired, usePhenixStore } from '@/store.js';
import axiosInstance from '@/utils/axios.js';
import { BUILDER_V2_FEATURE, createFeatureGuard } from '@/utils/features.js';

/**
 * A page's document title: what the page shows, most specific first, then
 * the app's name (WCAG 2.4.2 Page Titled).
 *
 * @param {...string} parts
 * @returns {string} for example "Configs - phēnix"
 */
export function pageTitle(...parts) {
  return [...parts.filter(Boolean), 'phēnix'].join(' - ');
}

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
      // Builder v2. `beforeEnter` runs before Vue Router resolves the async
      // component, so a disabled feature flag denies the route without ever
      // downloading the editor chunk. The server finds the files only this
      // view loads by its path in the build manifest, and serves them
      // compressed (src/go/web/builder_v2_assets.go).
      path: '/builder-v2',
      name: 'builder-v2',
      component: () => import('@/views/BuilderV2.vue'),
      // The editor fills the viewport below the header, so App.vue drops the
      // page container, its padding and the footer for this route. The
      // editor adds the open diagram to the title (see BuilderV2.vue).
      meta: { fullBleed: true, title: 'Builder v2' },
      // The redirect is explained by a notice that stays until it is
      // dismissed: a timed toast can vanish before it is read (WCAG 2.2.1).
      // Without an action button the snackbar keeps role=alert.
      beforeEnter: createFeatureGuard({
        flag: BUILDER_V2_FEATURE,
        ensureFeatures: () => usePhenixStore().ensureFeatures(),
        fallback: { name: 'home' },
        onDenied: () => {
          new Snackbar().open({
            message: 'Builder v2 is not enabled on this phenix server.',
            type: 'is-warning',
            indefinite: true,
            actionText: null,
            cancelText: 'Dismiss',
          });
        },
        onError: (error) => {
          console.error('Unable to load server features.', error);
          new Snackbar().open({
            message:
              'Unable to verify whether Builder v2 is enabled. Reload the page to try again.',
            type: 'is-danger',
            indefinite: true,
            actionText: null,
            cancelText: 'Dismiss',
          });
        },
      }),
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
    } else if (signIn.open || tokenExpired(store.token)) {
      // Builder v2 asks for the password again in place, and nothing
      // logs out or leaves the page meanwhile (see builder/signin.js).
      const builder = expiredNavigation(to, from);

      if (builder === 'go') {
        next();
        return;
      }

      if (builder === 'stay') {
        next(false);
        return;
      }

      // handle expired JWT by logging user out: https://stackoverflow.com/a/69058154
      // The page stays while a warning about Builder v2 changes the
      // server does not have is shown (see utils/logout.js); the logout
      // then goes to the sign-in page.
      if (!store.loggingOut) {
        new Toast().open({
          message: `Token is expired. Log in again`,
          type: 'is-warning',
          duration: 5000,
        });
      }

      store.requestLogout('expired');
      next(false);
      return;
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
  document.title = pageTitle(to.meta?.title);
});

export default router;
