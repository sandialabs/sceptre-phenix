// Where the router sends a visit: to sign in when no one is signed in, to a
// page the role may use when it may not use the one asked for, and anywhere
// when the server runs without authentication.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { NavigationFailureType, isNavigationFailure } from 'vue-router';
import { role } from './helpers/roles.js';
import { memoryStorage } from './helpers/storage.js';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal();
  // there is no browser history to use outside a browser
  return { ...actual, createWebHistory: actual.createMemoryHistory };
});
const toast = vi.hoisted(() => vi.fn());
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open(options) {
      toast(options);
    }
  },
}));
// the pages visits end on; the real views need a browser
vi.mock('@/views/Experiments.vue', () => ({ default: {} }));
vi.mock('@/views/Hosts.vue', () => ({ default: {} }));
vi.mock('@/views/Users.vue', () => ({ default: {} }));
vi.mock('@/views/SignIn.vue', () => ({ default: {} }));
vi.mock('@/views/Disabled.vue', () => ({ default: {} }));
vi.mock('@/views/experiment/Base.vue', () => ({ default: {} }));
vi.mock('@/views/experiment/VMtilesView.vue', () => ({ default: {} }));

let router;
let store;

beforeEach(async () => {
  vi.stubEnv('VITE_AUTH', 'enabled');
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  vi.stubGlobal('document', { title: '' });
  toast.mockReset();

  // a new router for each test, so every visit starts from nowhere
  vi.resetModules();
  setActivePinia(createPinia());
  ({ default: router } = await import('@/router.js'));
  store = (await import('@/store.js')).usePhenixStore();
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

// signs in with a token that expires in the given number of seconds
function signIn(signedInRole, expiresIn = 3600) {
  const exp = Math.floor(Date.now() / 1000) + expiresIn;
  store.auth = true;
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
      '/users',
      'users',
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
  ])('%s', async (_, signedInRole, path, destination) => {
    signIn(signedInRole);
    const to = await visit(path);
    expect(to.name).toBe(destination);
    expect(document.title).toBe(`${to.meta.title} - phēnix`);
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
});

describe('with no one signed in', () => {
  it('sends a visit to sign in and remembers where it was going', async () => {
    expect((await visit('/hosts')).name).toBe('signin');
    expect(store.next.fullPath).toBe('/hosts');
    expect(document.title).toBe('Sign in - phēnix');
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
