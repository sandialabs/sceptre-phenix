// @vitest-environment happy-dom
// The Change Optical Disc dialog keeps the VM name in its title until it
// has finished closing, and only then forgets the VM and the disc.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import {
  RouterLinkStub,
  enableAutoUnmount,
  flushPromises,
  mount,
} from '@vue/test-utils';
import Buefy from 'buefy';
import axiosInstance from '@/utils/axios.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';

vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.reject(new Error('no backend'))),
    post: vi.fn(() => Promise.resolve({})),
    delete: vi.fn(() => Promise.resolve({})),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: vi.fn(),
  removeWsHandler: vi.fn(),
  sendWsMsg: vi.fn(),
}));
vi.mock('@/store', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null, features: [] }),
}));

enableAutoUnmount(afterEach);

function mountView() {
  return mount(RunningExperiment, {
    attachTo: document.body,
    global: {
      plugins: [Buefy],
      // The real transition, so that the dialog leaves as in the browser.
      stubs: { 'router-link': RouterLinkStub, transition: false },
      mocks: { $route: { params: { id: 'exp' } } },
    },
  });
}

// Opens the dialog for vm-1, and resolves to its modal card.
async function openDialog(wrapper) {
  wrapper.vm.opticalDiscModal.vmName = 'vm-1';
  wrapper.vm.opticalDiscModal.disc = '/phenix/images/tools.iso';
  wrapper.vm.opticalDiscModal.active = true;
  await flushPromises();

  return wrapper
    .findAll('.modal-card')
    .find((card) => card.text().includes('Change Optical Disc for'));
}

const button = (card, text) =>
  card.findAll('button').find((b) => b.text() === text);

describe('RunningExperiment Change Optical Disc dialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test.each(['Cancel', 'insert optical disc'])(
    '%s keeps the title until the dialog has left',
    async (label) => {
      const wrapper = mountView();
      await flushPromises();
      const card = await openDialog(wrapper);

      expect(card.get('.modal-card-title').text()).toBe(
        'Change Optical Disc for vm-1',
      );

      await button(card, label).trigger('click');

      // The dialog is closing. Its title still names the VM.
      expect(wrapper.vm.opticalDiscModal.active).toBe(false);
      expect(wrapper.vm.opticalDiscModal.vmName).toBe('vm-1');

      await vi.waitFor(() =>
        expect(wrapper.vm.opticalDiscModal).toEqual({
          active: false,
          disc: '',
          vmName: null,
        }),
      );
    },
  );

  test('insert sends the chosen disc of the VM', async () => {
    const wrapper = mountView();
    await flushPromises();
    const card = await openDialog(wrapper);

    await button(card, 'insert optical disc').trigger('click');

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'experiments/exp/vms/vm-1/cdrom?isoPath=/phenix/images/tools.iso',
    );
  });
});
