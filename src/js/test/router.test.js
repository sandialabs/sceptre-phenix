// Where the router sends a visit: to sign in when signed out, to a page the
// role may use instead of one it may not, and anywhere without auth.
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { NavigationFailureType, isNavigationFailure } from 'vue-router';
import { role } from './helpers/roles.js';

vi.unmock('@/store.js');
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal();
  // a fresh history for each test, not the window's shared one
  return { ...actual, createWebHistory: actual.createMemoryHistory };
});
const toast = vi.hoisted(() => vi.fn());
const notice = vi.hoisted(() => vi.fn());
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open(options) {
      toast(options);
    }
  },
  NotificationProgrammatic: class {
    open(options) {
      notice(options);
    }
  },
}));
// the pages visits end on, which these tests do not draw
vi.mock('@/views/Experiments.vue', () => ({ default: {} }));
vi.mock('@/views/Hosts.vue', () => ({ default: {} }));
vi.mock('@/views/Vlans.vue', () => ({ default: {} }));
vi.mock('@/views/Users.vue', () => ({ default: {} }));
vi.mock('@/views/Tunneler.vue', () => ({ default: {} }));
vi.mock('@/views/SignIn.vue', () => ({ default: {} }));
vi.mock('@/views/Disabled.vue', () => ({ default: {} }));
vi.mock('@/views/experiment/Base.vue', () => ({ default: {} }));
vi.mock('@/views/experiment/VMtilesView.vue', () => ({ default: {} }));
vi.mock('@/views/ProxySignUp.vue', () => ({ default: {} }));
vi.mock('@/views/NotFound.vue', () => ({ default: {} }));
const getProxyLogin = vi.hoisted(() => vi.fn());
vi.mock('@/api/auth.js', () => ({ getProxyLogin }));

let router;
let store;

beforeEach(async () => {
  vi.stubEnv('VITE_AUTH', 'enabled');
  // test/setup.js gives each test an empty localStorage
  sessionStorage.clear();
  document.title = '';
  toast.mockReset();

  // a new router for each test, so every visit starts from nowhere
  vi.resetModules();
  setActivePinia(createPinia());
  ({ default: router } = await import('@/router.js'));
  store = (await import('@/store.js')).usePhenixStore();
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.useRealTimers();
});

// signs in with a token that expires in the given number of seconds
function signIn(signedInRole, expiresIn = 3600) {
  const exp = Math.floor(Date.now() / 1000) + expiresIn;
  store.auth = true;
  store.username = 'alice';
  store.role = signedInRole;
  store.token = `header.${btoa(JSON.stringify({ exp }))}.signature`;
}

// the route a visit to path ends on, after any page the guard sends it to
function visit(path) {
  return new Promise((resolve, reject) => {
    const stop = router.afterEach((to, _, failure) => {
      // the guard starting a navigation of its own cancels this one
      if (isNavigationFailure(failure, NavigationFailureType.cancelled)) {
        return;
      }
      stop();
      if (failure) reject(failure);
      else resolve(to);
    });
    router.push(path);
  });
}

describe('a signed-in role', () => {
  const experimenter = role('Experimenter', ['experiments', 'list']);
  const tiles = role('Tiles', ['vms', 'list']);
  const oneExperiment = role(
    'One',
    ['experiments', 'list'],
    ['experiments', 'get', ['exp1']],
  );

  it.each([
    [
      'opens a page it may use',
      role('Hosts', ['experiments', 'list'], ['hosts', 'list']),
      '/hosts',
      'hosts',
    ],
    [
      'is sent to the experiments list from a page it may not use',
      experimenter,
      '/hosts',
      'experiments',
    ],
    [
      'opens the VLANs page when it may list VLANs',
      role('VLANs', ['experiments', 'list'], ['vlans', 'list', ['exp1']]),
      '/vlans?experiment=exp1',
      'vlans',
    ],
    [
      'is sent to the experiments list from the VLANs page it may not use',
      experimenter,
      '/vlans',
      'experiments',
    ],
    [
      'is sent to the VM tiles when it may only list VMs',
      tiles,
      '/hosts',
      'vmtiles',
    ],
    [
      'is sent to the VM tiles from the experiments list it may not use',
      tiles,
      '/experiments',
      'vmtiles',
    ],
    [
      'opens an experiment it may get',
      oneExperiment,
      '/experiment/exp1',
      'experiment',
    ],
    [
      'is sent to the experiments list from an experiment it may not get',
      oneExperiment,
      '/experiment/exp2',
      'experiments',
    ],
    [
      'opens the page asked for when it may use no page to be sent to',
      role('Configs', ['configs', 'list']),
      '/hosts',
      'hosts',
    ],
    [
      'opens a page without a permission check of its own',
      tiles,
      '/tunneler',
      'tunneler',
    ],
    [
      'opens the users page when it may list users',
      role('Admin', ['experiments', 'list'], ['users', 'list']),
      '/users',
      'users',
    ],
    [
      'opens the users page when it may get only its own user',
      role('Self', ['experiments', 'list'], ['users', 'get', ['alice']]),
      '/users',
      'users',
    ],
    [
      'is sent to the experiments list from the users page it may not use',
      role('Other', ['experiments', 'list'], ['users', 'get', ['bob']]),
      '/users',
      'experiments',
    ],
    ['opens the experiments list as home', experimenter, '/', 'experiments'],
    [
      'opens the VM tiles as home for a VM viewer',
      role('VM Viewer', ['vms', 'list']),
      '/',
      'vmtiles',
    ],
    [
      'is sent to the disabled page when its account is disabled',
      role('Disabled'),
      '/hosts',
      'disabled',
    ],
    [
      'is told an unknown address has no page',
      experimenter,
      '/no-such-page',
      'notfound',
    ],
  ])('%s', async (_, signedInRole, path, destination) => {
    signIn(signedInRole);
    const to = await visit(path);
    expect(to.name).toBe(destination);
    // a page that shows one item, such as an experiment, names it first
    const item = to.meta.titleParam
      ? `${to.params[to.meta.titleParam]} - `
      : '';
    expect(document.title).toBe(`${item}${to.meta.title} - phēnix`);
  });

  it('titles a page for one experiment with its name first', async () => {
    signIn(oneExperiment);
    await visit('/experiment/exp1');
    expect(document.title).toBe('exp1 - Experiment - phēnix');
  });

  it('is sent home from the sign-in page behind an authenticating proxy', async () => {
    vi.stubEnv('VITE_AUTH', 'proxy');
    signIn(experimenter);
    expect((await visit('/signin')).name).toBe('experiments');
  });

  it('is signed out when its token has expired', async () => {
    signIn(experimenter, -60);
    expect((await visit('/experiments')).name).toBe('signin');
    expect(store.auth).toBe(false);
    expect(toast).toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringMatching(/expired/) }),
    );
  });

  it('is signed out from the second its token expires, and not before', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    const expiresIn = fc.oneof(
      fc.integer({ min: -2, max: 2 }),
      fc.integer({ min: -1e6, max: 1e6 }),
    );
    // any moment, so the token's whole-second exp lands anywhere in a second
    const moment = fc.integer({
      min: Date.UTC(2020, 0),
      max: Date.UTC(2040, 0),
    });
    await fc.assert(
      fc.asyncProperty(moment, expiresIn, async (now, seconds) => {
        // from a page every visit may open, not the sign-in page logout opens
        await router.push('/disabled');
        vi.setSystemTime(now);
        signIn(experimenter, seconds);
        const to = await visit('/experiments');
        expect(to.name).toBe(seconds > 0 ? 'experiments' : 'signin');
        expect(store.auth).toBe(seconds > 0);
      }),
    );
  });
});

describe('with no one signed in', () => {
  it('sends a visit to sign in and remembers where it was going', async () => {
    expect((await visit('/hosts')).name).toBe('signin');
    expect(store.next.fullPath).toBe('/hosts');
    expect(document.title).toBe('Sign in - phēnix');
  });
});

describe('behind an authenticating proxy', () => {
  // the proxy's user has no phenix user: the server's 404 names it
  const unknownUser = (data) =>
    Object.assign(new Error('Request failed with status code 404'), {
      response: { status: 404, data },
    });

  it.each([
    [
      'offers to sign up the user a JSON Error names',
      { message: 'user not found', metadata: { username: 'alice' } },
      'alice',
    ],
    ['offers an empty sign-up for an error naming no user', 'forbidden', ''],
  ])('%s', async (_, data, username) => {
    vi.stubEnv('VITE_AUTH', 'proxy');
    getProxyLogin.mockRejectedValue(unknownUser(data));
    const to = await visit('/hosts');
    expect(to.name).toBe('proxysignup');
    expect(to.params.username).toBe(username);
  });
});

describe('without authentication', () => {
  it('signs in as a global admin and opens any page', async () => {
    vi.stubEnv('VITE_AUTH', 'disabled');
    expect((await visit('/hosts')).name).toBe('hosts');
    expect(store).toMatchObject({
      auth: true,
      role: { name: 'Global Admin' },
    });
  });
});

describe('a page whose build file the server no longer has', () => {
  const stale = () =>
    Promise.reject(
      new TypeError(
        'Failed to fetch dynamically imported module: /assets/Stale-1a2b.js',
      ),
    );

  // the router served from base, with a route whose chunk fails to load
  async function servedFrom(base) {
    vi.stubEnv('BASE_URL', base);
    vi.resetModules();
    setActivePinia(createPinia());
    ({ default: router } = await import('@/router.js'));
    store = (await import('@/store.js')).usePhenixStore();
    router.addRoute({ path: '/stale', name: 'stale', component: stale });
    router.addRoute({
      path: '/broken',
      name: 'broken',
      component: () => Promise.reject(new Error('syntax error')),
    });
    signIn(role('Any', ['experiments', 'list']));
  }

  beforeEach(() => {
    vi.stubGlobal('location', { assign: vi.fn(), reload: vi.fn() });
    notice.mockReset();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    sessionStorage.clear();
  });

  it('reloads once to the page asked for, under the base path', async () => {
    await servedFrom('/igor/bb-8/phenix/');
    let pendingSeen;
    router.beforeEach(async () => {
      pendingSeen = (await import('@/router.js')).pendingNavigationHref();
    });

    await expect(router.push('/stale?x=1')).rejects.toThrow(/dynamically/);
    expect(pendingSeen).toBe('/igor/bb-8/phenix/stale?x=1');
    expect(location.assign).toHaveBeenCalledWith('/igor/bb-8/phenix/stale?x=1');
    const { pendingNavigationHref } = await import('@/router.js');
    expect(pendingNavigationHref()).toBeNull();

    // the page reloaded, and the file is still missing: no second reload
    vi.resetModules();
    await servedFrom('/igor/bb-8/phenix/');
    await expect(router.push('/stale')).rejects.toThrow(/dynamically/);
    expect(location.assign).toHaveBeenCalledTimes(1);
    expect(notice).toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringMatching(/Reload/) }),
    );
  });

  it('does not reload for another error', async () => {
    await servedFrom('/');
    await expect(router.push('/broken')).rejects.toThrow('syntax error');
    expect(location.assign).not.toHaveBeenCalled();
    expect(location.reload).not.toHaveBeenCalled();
    expect(notice).not.toHaveBeenCalled();
  });

  it('forgets the navigation once it ends', async () => {
    await servedFrom('/');
    await router.push('/tunneler');
    const { pendingNavigationHref } = await import('@/router.js');
    expect(pendingNavigationHref()).toBeNull();
  });

  it('keeps the navigation under way when an older one is cancelled', async () => {
    await servedFrom('/');
    const { pendingNavigationHref } = await import('@/router.js');
    const gate = () => {
      let open;
      const shut = new Promise((resolve) => (open = resolve));
      return { open, shut };
    };
    const slow = gate();
    const late = gate();
    router.addRoute({
      path: '/slow',
      component: {},
      beforeEnter: () => slow.shut,
    });
    let pendingSeen;
    router.addRoute({
      path: '/late',
      component: async () => {
        await late.shut;
        // where Vite's preload error asks for the page under way
        pendingSeen = pendingNavigationHref();
        return stale();
      },
    });

    const first = router.push('/slow');
    const second = router.push('/late');
    // the first ends, cancelled, while the second still loads its page
    await vi.waitFor(() => expect(pendingNavigationHref()).toBe('/late'));
    slow.open();
    const failure = await first;
    expect(isNavigationFailure(failure, NavigationFailureType.cancelled)).toBe(
      true,
    );
    late.open();
    await expect(second).rejects.toThrow(/dynamically/);

    expect(pendingSeen).toBe('/late');
    expect(location.assign).toHaveBeenCalledWith('/late');
    expect(pendingNavigationHref()).toBeNull();
  });
});

describe('page width', () => {
  it('lets only the State of Health graph use the window width', () => {
    const wide = router
      .getRoutes()
      .filter((r) => r.meta.wide)
      .map((r) => r.name);
    expect(wide).toEqual(['soh']);
    // the Builder fills the window and has no footer instead
    expect(router.resolve('/builder').meta).toMatchObject({ fullBleed: true });
    expect(router.resolve('/soh').meta.wide).toBeUndefined();
  });
});
