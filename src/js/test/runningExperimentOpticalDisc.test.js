// The CD-ROM picker keeps the VM name in its title until it has finished
// closing, and only then forgets the VM and the disc.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, test, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

import { clearPageCache } from '@/utils/pageCache.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { disk } from './helpers/fixtures.js';
import { mountPage, textOf } from './helpers/mount.js';

const closed = {
  active: false,
  vmName: null,
  current: '',
  disc: null,
  isos: [],
  loading: false,
  error: null,
};

// The exp page with the real transitions, so that the picker leaves as in
// the browser.
function openPage() {
  axios.get.mockRejectedValue(new Error('no backend'));
  return mountPage(RunningExperiment, {
    route: '/experiment/exp',
    data: { experiment: { name: 'exp', running: true, vms: [] } },
    stubs: { transition: false },
  });
}

// Opens the picker for web, and resolves to its modal card.
async function openPicker(page) {
  Object.assign(page.vm.opticalDiscModal, {
    vmName: 'web',
    disc: '/images/tools.iso',
    isos: [disk('/images/tools.iso', 'tools.iso', { kind: 'ISO' })],
    active: true,
  });
  await flushPromises();

  return page.get('.optical-disc-card');
}

const button = (card, text) =>
  card.findAll('footer button').find((b) => textOf(b) === text);

describe('RunningExperiment CD-ROM picker', () => {
  beforeEach(() => {
    resetMocks();
    clearPageCache();
  });

  test.each(['Cancel', 'Insert'])(
    '%s keeps the title until the picker has left',
    async (label) => {
      axios.post.mockResolvedValue({});
      const page = await openPage();
      const card = await openPicker(page);

      expect(textOf(card.get('.modal-card-title'))).toBe(
        'Insert a CD-ROM for web',
      );

      await button(card, label).trigger('click');

      // The picker is closing. Its title still names the VM.
      expect(page.vm.opticalDiscModal.active).toBe(false);
      expect(page.vm.opticalDiscModal.vmName).toBe('web');

      await vi.waitFor(() => expect(page.vm.opticalDiscModal).toEqual(closed));
    },
  );

  test('insert sends the chosen disc of the VM', async () => {
    axios.post.mockResolvedValue({});
    const page = await openPage();
    const card = await openPicker(page);

    await button(card, 'Insert').trigger('click');

    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp/vms/web/cdrom',
      null,
      { params: { isoPath: '/images/tools.iso' } },
    );
  });
});
