// The running VMs of the Add VM panel of the console: the panel shows the
// list that was last read at once, reads it again in the background, and
// keeps the old list when that read fails. The list is kept for each user
// in memory and in localStorage, and logout clears it.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

const { axios, modules, notify, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/errorNotif', modules.errorNotif);

import VncAddVm from '@/vnc/VncAddVm.vue';
import {
  cacheRunningVms,
  cachedRunningVms,
  clearRunningVmsCache,
  runningVmKeys,
} from '@/vnc/vmListCache.js';
import { httpError } from './helpers/fixtures.js';
import { mountComponent, textOf } from './helpers/mount.js';

const STORAGE_KEY = 'phenix.vnc.runningVms.alice';

const answer = (vms) => ({ data: { vms } });

// the listing that GET /vms answers until the test settles it
function pendingListing() {
  let settle;
  axios.get.mockReturnValue(
    new Promise((resolve, reject) => {
      settle = { resolve, reject };
    }),
  );
  return settle;
}

async function mountPanel(shown = []) {
  const wrapper = await mountComponent(VncAddVm, {
    props: { shown },
    attachTo: document.body,
  });
  return wrapper;
}

// the VMs the autocomplete offers once it has focus
async function choices(wrapper) {
  await wrapper.find('input').trigger('focus');
  await flushPromises();
  return wrapper.findAll('.dropdown-item:not(.is-disabled)').map(textOf);
}

const status = (wrapper) => textOf(wrapper.find('.vnc-add-vm-status'));

beforeEach(() => {
  resetMocks();
  clearRunningVmsCache();
});

describe('the cache of running VMs', () => {
  it('keeps the exp/vm keys of the running VMs, sorted', () => {
    expect(
      runningVmKeys([
        { experiment: 'train', name: 'plc', running: true },
        { experiment: 'demo', name: 'web', running: true },
        { experiment: 'demo', name: 'off', running: false },
      ]),
    ).toEqual(['demo/web', 'train/plc']);
    expect(runningVmKeys(undefined)).toEqual([]);
  });

  it('keeps the list for each user, also in localStorage for a reload', () => {
    cacheRunningVms('alice', ['demo/web']);
    cacheRunningVms('bob', ['demo/db']);
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY))).toEqual(['demo/web']);
    expect(cachedRunningVms('bob')).toEqual(['demo/db']);
    expect(cachedRunningVms('carol')).toBeNull();
  });

  it('reads the list from localStorage on a new page', async () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['demo/web']));
    expect(cachedRunningVms('alice')).toEqual(['demo/web']);
  });

  it.each([
    ['not JSON', '{'],
    ['not a list', '{"vms":[]}'],
    ['a list of other values', '[1,2]'],
  ])('ignores a stored value that is %s', (_, value) => {
    localStorage.setItem(STORAGE_KEY, value);
    expect(cachedRunningVms('alice')).toBeNull();
  });

  it('keeps the list in memory when localStorage refuses it', () => {
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('quota');
      });
    try {
      cacheRunningVms('alice', ['demo/web']);
      expect(cachedRunningVms('alice')).toEqual(['demo/web']);
    } finally {
      setItem.mockRestore();
    }
  });

  it('forgets every user on logout, and leaves other values', () => {
    localStorage.setItem('phenix.other', 'kept');
    cacheRunningVms('alice', ['demo/web']);
    cacheRunningVms('bob', ['demo/db']);
    clearRunningVmsCache();
    expect(cachedRunningVms('alice')).toBeNull();
    expect(cachedRunningVms('bob')).toBeNull();
    expect(localStorage.getItem('phenix.other')).toBe('kept');
  });
});

describe('the Add VM panel', () => {
  it('loads the list the first time, and keeps it for the next', async () => {
    const listing = pendingListing();
    const wrapper = await mountPanel(['demo/web']);
    expect(await choices(wrapper)).toEqual([]);
    expect(textOf(wrapper)).toContain('Loading the VMs');
    listing.resolve(
      answer([
        { experiment: 'demo', name: 'web', running: true },
        { experiment: 'demo', name: 'db', running: true },
      ]),
    );
    await flushPromises();
    expect(await choices(wrapper)).toEqual(['demo/db']);
    expect(cachedRunningVms(store.username)).toEqual(['demo/db', 'demo/web']);
  });

  it('shows the cached list at once, then the list it reads again', async () => {
    cacheRunningVms('alice', ['demo/db', 'demo/old']);
    const listing = pendingListing();
    const wrapper = await mountPanel();
    expect(await choices(wrapper)).toEqual(['demo/db', 'demo/old']);
    expect(status(wrapper)).toBe('Refreshing the list of VMs');
    expect(axios.get).toHaveBeenCalledWith('vms', undefined);

    listing.resolve(
      answer([{ experiment: 'demo', name: 'new', running: true }]),
    );
    await flushPromises();
    expect(await choices(wrapper)).toEqual(['demo/new']);
    expect(status(wrapper)).toBe('');
    expect(cachedRunningVms('alice')).toEqual(['demo/new']);
  });

  it('keeps the cached list, and reports the error, when the read fails', async () => {
    cacheRunningVms('alice', ['demo/db']);
    const listing = pendingListing();
    const wrapper = await mountPanel();
    listing.reject(httpError(500, { message: 'minimega is down' }));
    await flushPromises();
    expect(notify.error).toHaveBeenCalledOnce();
    expect(await choices(wrapper)).toEqual(['demo/db']);
    expect(status(wrapper)).toBe('');
    expect(cachedRunningVms('alice')).toEqual(['demo/db']);
  });

  it('reads the list again each time it opens', async () => {
    axios.get.mockResolvedValue(
      answer([{ experiment: 'demo', name: 'db', running: true }]),
    );
    (await mountPanel()).unmount();
    await flushPromises();
    (await mountPanel()).unmount();
    await flushPromises();
    expect(axios.get).toHaveBeenCalledTimes(2);
  });
});
