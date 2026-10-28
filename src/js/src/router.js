import { createRouter, createWebHistory } from 'vue-router';

import { ToastProgrammatic as Toast } from 'buefy';

import * as authApi from '@/api/auth.js';
import { usePhenixStore } from '@/store.js';
import { routeTitle } from '@/utils/pageTitle.js';
import { roleAllowed, usersPageAllowed } from '@/utils/rbac.js';
import { isChunkLoadError, reloadForStaleChunk } from '@/utils/staleChunk.js';
import { tokenExpired } from '@/utils/token.js';
import { canUseWebShark } from '@/utils/webshark.js';

export { pageTitle } from '@/utils/pageTitle.js';

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
      meta: {
        title: 'Experiments',
        allowed: () => roleAllowed('experiments', 'list'),
      },
      component: () => import('@/views/Experiments.vue'),
    },
    {
      path: '/experiment/:id',
      name: 'experiment',
      meta: {
        title: 'Experiment',
        titleParam: 'id',
        allowed: (to) => roleAllowed('experiments', 'get', to.params.id),
      },
      component: () => import('@/views/experiment/Base.vue'),
    },
    {
      path: '/hosts',
      name: 'hosts',
      meta: { title: 'Hosts', allowed: () => roleAllowed('hosts', 'list') },
      component: () => import('@/views/Hosts.vue'),
    },
    {
      path: '/vlans',
      name: 'vlans',
      meta: { title: 'VLANs', allowed: () => roleAllowed('vlans', 'list') },
      component: () => import('@/views/Vlans.vue'),
    },
    {
      path: '/configs/',
      name: 'configs',
      meta: { title: 'Configs', allowed: () => roleAllowed('configs', 'list') },
      component: () => import('@/views/Configs.vue'),
    },
    {
      path: '/disks/',
      name: 'disks',
      meta: { title: 'Disks', allowed: () => roleAllowed('disks', 'list') },
      component: () => import('@/views/Disks.vue'),
    },
    {
      path: '/vmtiles',
      name: 'vmtiles',
      meta: { title: 'VM tiles', allowed: () => roleAllowed('vms', 'list') },
      component: () => import('@/views/experiment/VMtilesView.vue'),
    },
    {
      path: '/users',
      name: 'users',
      meta: { title: 'Users', allowed: () => usersPageAllowed() },
      component: () => import('@/views/Users.vue'),
    },
    {
      path: '/log',
      name: 'log',
      component: () => import('@/views/Logs.vue'),
      meta: { title: 'Logs', allowed: () => roleAllowed('logs', 'get') },
    },
    {
      path: '/console',
      name: 'console',
      meta: {
        title: 'Console',
        allowed: () => roleAllowed('miniconsole', 'post'),
      },
      component: () => import('@/views/Console.vue'),
    },
    {
      path: '/scorch',
      name: 'scorch',
      meta: {
        title: 'SCORCH',
        allowed: () => roleAllowed('experiments', 'list'),
      },
      component: () => import('@/views/Scorch.vue'),
    },
    {
      path: '/scorch/:id',
      name: 'scorchruns',
      meta: {
        title: 'SCORCH runs',
        titleParam: 'id',
        allowed: (to) => roleAllowed('experiments', 'get', to.params.id),
      },
      component: () => import('@/views/ScorchRuns.vue'),
    },
    {
      path: '/soh',
      name: 'sohoverview',
      component: () => import('@/views/SohOverview.vue'),
      meta: {
        title: 'State of Health',
        allowed: () => roleAllowed('experiments', 'list'),
      },
    },
    {
      path: '/soh/:id',
      name: 'soh',
      meta: {
        title: 'State of Health',
        titleParam: 'id',
        allowed: (to) => roleAllowed('experiments', 'get', to.params.id),
        // the graph uses the whole window width (App.vue)
        wide: true,
      },
      component: () => import('@/views/StateOfHealth.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      meta: {
        title: 'Settings',
        allowed: () => roleAllowed('settings', 'update'),
      },
      component: () => import('@/views/Settings.vue'),
    },
    {
      path: '/tunneler',
      name: 'tunneler',
      meta: { title: 'Tunneler' },
      component: () => import('@/views/Tunneler.vue'),
    },
    {
      // not /webshark: the server serves WebShark itself there
      path: '/packets',
      name: 'webshark',
      component: () => import('@/views/WebShark.vue'),
      meta: { title: 'WebShark', allowed: () => canUseWebShark() },
    },
    {
      // The Builder. The build finds the files that only this view loads
      // from the view's path (src/js/plugins/builder-assets.js). The server
      // sends them compressed (src/go/web/builder_assets.go).
      path: '/builder',
      name: 'builder',
      component: () => import('@/views/Builder.vue'),
      // The editor fills the viewport below the header, so App.vue drops the
      // page container, its padding and the footer for this route. The
      // editor adds the open diagram to the title (see Builder.vue). Its
      // large chunk loads only on demand, so it is not prefetched.
      meta: { fullBleed: true, title: 'Builder', prefetch: false },
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
    { path: '/version', name: 'version' },
    { path: '/features', name: 'features' },

    //vnc
    { path: '/api/v1/experiments/:id/vms/:name/vnc?token=:token', name: 'vnc' },

    {
      path: '/:pathMatch(.*)*',
      name: 'notfound',
      meta: { title: 'Page not found' },
      component: () => import('@/views/NotFound.vue'),
    },
  ],
});

// the address of the navigation under way, for a failed chunk load to reload to
let pending = null;

export function pendingNavigationHref() {
  return pending;
}

router.beforeEach((to) => {
  pending = router.resolve(to.fullPath).href;
});

// forgets the navigation to `to` once it ends, but not a newer one that
// cancelled it
function settle(to) {
  if (!to || pending === router.resolve(to.fullPath).href) pending = null;
}

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
    } else if (tokenExpired(store.token)) {
      // handle expired JWT by logging user out: https://stackoverflow.com/a/69058154
      new Toast().open({
        message: `Token is expired. Log in again`,
        type: 'is-warning',
      });
      store.logout();
    } else if (to.meta.allowed && !to.meta.allowed(to)) {
      // a page the role cannot use (an old link or bookmark) would only show
      // permission errors: go to the first page it can use instead
      const fallback = ['experiments', 'vmtiles'].find(
        (name) => name !== to.name && router.resolve({ name }).meta.allowed(),
      );
      if (fallback) {
        next({ name: fallback });
        return;
      }
    }

    next();
  } else {
    store.next = to;

    if (import.meta.env.VITE_AUTH === 'proxy') {
      // awaited, so the router waits for the next() it calls
      await authApi
        .getProxyLogin()
        .then((response) => {
          store.login(response.data, false);
          next();
        })
        .catch((err) => {
          next({
            name: 'proxysignup',
            params: {
              username: err.response?.data?.metadata?.username ?? '',
            },
          });
        });
    } else {
      next({ name: 'signin' });
    }
  }
});
// Give every route its own document title (WCAG 2.4.2 Page Titled), with the
// name of the item it shows (WCAG 2.4.8 Location).
router.afterEach((to) => {
  settle(to);
  document.title = routeTitle(to);
});

// A page whose chunk the server no longer has (an update while the tab was
// open) is loaded again from the server, once (see utils/staleChunk.js).
router.onError((err, to) => {
  settle(to);
  if (isChunkLoadError(err)) {
    reloadForStaleChunk(to ? router.resolve(to.fullPath).href : '');
  }
});

export default router;
