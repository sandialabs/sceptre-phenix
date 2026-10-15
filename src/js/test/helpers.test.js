// The shared test helpers other tests build on: the roles the mocked
// roleAllowed answers with, what resetMocks undoes, the order dialogs()
// reports, and the fixtures' defaults.
import { describe, expect, it, vi } from 'vitest';

import { dialogs, fakeBuefy, lastConfirm, toasts } from './helpers/buefy.js';
import { disk, httpError, vm, wsMsg } from './helpers/fixtures.js';
import {
  axios,
  modules,
  notify,
  rbac,
  resetMocks,
  saver,
  STORE_DEFAULTS,
  store,
  ws,
} from './helpers/mocks.js';
import { capturing, fetching, loaderSpy } from './helpers/pageLoader.js';
import {
  allowAll,
  allowOnly,
  denyAll,
  denyOnly,
} from './helpers/permissions.js';

describe('permissions', () => {
  it('allows or denies everything', () => {
    expect(allowAll('vms', 'delete', 'web')).toBe(true);
    expect(denyAll('vms', 'list')).toBe(false);
  });

  it.each([
    // [permission, resource, verb, name, matches]
    [['vms'], 'vms', 'delete', 'web', true],
    [['vms'], 'disks', 'delete', 'web', false],
    [['vms', 'patch'], 'vms', 'patch', 'web', true],
    [['vms', 'patch'], 'vms', 'list', 'web', false],
    [['*', 'delete'], 'disks', 'delete', undefined, true],
    [['*', 'delete'], 'disks', 'get', undefined, false],
    [['files', 'get', 'exp1'], 'files', 'get', 'exp1', true],
    [['files', 'get', 'exp1'], 'files', 'get', 'exp2', false],
    [['files', 'get', '*'], 'files', 'get', 'exp2', true],
  ])('%j matching %s %s %s is %s', (perm, resource, verb, name, matches) => {
    expect(allowOnly(perm)(resource, verb, name)).toBe(matches);
    expect(denyOnly(perm)(resource, verb, name)).toBe(!matches);
  });

  it('allows what any one of several permissions matches', () => {
    const role = allowOnly(['vms', 'list'], ['disks']);
    expect(role('vms', 'list')).toBe(true);
    expect(role('disks', 'create')).toBe(true);
    expect(role('vms', 'delete')).toBe(false);
  });
});

describe('resetMocks', () => {
  const roleAllowed = modules.rbac().roleAllowed;

  it('lets every role do anything again', () => {
    rbac.allowed = denyAll;
    expect(roleAllowed('vms', 'list')).toBe(false);
    resetMocks();
    expect(roleAllowed('vms', 'list')).toBe(true);
  });

  it('forgets every request, error report and download', () => {
    axios.get('experiments');
    axios.request({});
    notify.error(new Error('boom'));
    saver.saveAs('blob');
    resetMocks();
    for (const fn of [axios.get, axios.request, notify.error, saver.saveAs]) {
      expect(fn).not.toHaveBeenCalled();
    }
  });

  it('restores the default store, dropping keys a test added', () => {
    store.token = 'other';
    store.features.push('webshark');
    store.extra = true;
    resetMocks();
    expect(store).toEqual(STORE_DEFAULTS);
    expect(STORE_DEFAULTS.features).toEqual([]);
  });

  it('holds exactly the store it is given', () => {
    resetMocks({ store: { role: { name: 'Global Admin' } } });
    expect(store).toEqual({ role: { name: 'Global Admin' } });
    resetMocks({ store: {} });
    expect(store).toEqual({});
  });

  it('forgets the websocket traffic', () => {
    const socket = modules.websocket();
    const handler = () => {};
    socket.addWsHandler(handler);
    socket.onWsReconnect(() => {});
    socket.sendWsMsg(wsMsg('vm', 'web', 'start'));
    resetMocks();
    expect(ws).toEqual({ sent: [], handlers: [], reconnect: [] });
  });

  it('records websocket handlers until they are removed', () => {
    const socket = modules.websocket();
    const handler = () => {};
    socket.addWsHandler(handler);
    const off = socket.onWsReconnect(handler);
    expect(ws.handlers).toEqual([handler]);
    socket.removeWsHandler(handler);
    off();
    expect(ws.handlers).toEqual([]);
    expect(ws.reconnect).toEqual([]);
  });
});

describe('page loaders', () => {
  const real = async () => ({ createPageLoader: null, loadingText: 'real' });

  it('records the options and load() of a capturing loader', async () => {
    resetMocks();
    const mod = await capturing(real);
    expect(mod.loadingText).toBe('real');
    const options = { fetch: vi.fn() };
    mod.createPageLoader(options).load(1);
    expect(loaderSpy.options).toBe(options);
    expect(loaderSpy.load).toHaveBeenCalledWith(1);
    expect(options.fetch).not.toHaveBeenCalled();
  });

  it('fetches with a fetching loader', async () => {
    resetMocks();
    const mod = await fetching(real);
    const loader = mod.createPageLoader({ fetch: async () => 'data' });
    await expect(loader.load()).resolves.toBe('data');
    expect(loaderSpy.load).toHaveBeenCalledOnce();
  });
});

describe('Buefy', () => {
  it('lists dialogs in the order the page opened them', () => {
    const ctx = { $buefy: fakeBuefy() };
    ctx.$buefy.dialog.alert({ title: 'first' });
    ctx.$buefy.dialog.confirm({ title: 'second' });
    ctx.$buefy.dialog.prompt({ title: 'third' });
    ctx.$buefy.dialog.alert({ title: 'fourth' });
    expect(dialogs(ctx).map((d) => d.title)).toEqual([
      'first',
      'second',
      'third',
      'fourth',
    ]);
    expect(lastConfirm(ctx).title).toBe('second');
  });

  it('lists toasts in order', () => {
    const ctx = { $buefy: fakeBuefy() };
    ctx.$buefy.toast.open({ message: 'a' });
    ctx.$buefy.toast.open({ message: 'b' });
    expect(toasts(ctx).map((t) => t.message)).toEqual(['a', 'b']);
  });
});

describe('fixtures', () => {
  it('makes a disk outside the files directory read-only', () => {
    expect(disk('/data/base.qc2')).toMatchObject({
      name: 'base.qc2',
      outsideFilesDir: true,
      readOnly: true,
    });
    expect(disk('/phenix/images/a.qc2', 'a.qc2')).toMatchObject({
      name: 'a.qc2',
      outsideFilesDir: false,
      readOnly: false,
    });
  });

  it('lets a test replace any field', () => {
    expect(disk('/a', 'a', { kind: 'ISO' }).kind).toBe('ISO');
    expect(vm({ name: 'db' })).toMatchObject({ name: 'db', running: true });
  });

  it('carries the status of a failed request', () => {
    const error = httpError(413, { message: 'too large' });
    expect(error).toBeInstanceOf(Error);
    expect(error.response).toEqual({
      status: 413,
      data: { message: 'too large' },
    });
  });
});
