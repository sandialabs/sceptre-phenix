// Calls the RunningExperiment methods with a stand-in for the component, so
// no DOM is needed.
import { beforeEach, describe, expect, test, vi } from 'vitest';
import axiosInstance from '@/utils/axios.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';

vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.reject(new Error('no backend'))),
    post: vi.fn(() => Promise.resolve({})),
    delete: vi.fn(() => Promise.resolve({})),
  },
}));
vi.mock('@/utils/errorNotif', () => ({
  showError: vi.fn(),
  useErrorNotification: vi.fn(),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: vi.fn(),
  onWsReconnect: vi.fn(() => () => {}),
  removeWsHandler: vi.fn(),
  sendWsMsg: vi.fn(),
}));

const { methods } = RunningExperiment;

// the actions of the selection toolbar
const SELECTION_ACTIONS = [
  'start',
  'pause',
  'memorySnapshot',
  'commit',
  'snapshot',
  'redeploy',
  'resetDisk',
  'restart',
  'shutdown',
  'kill',
];

function instance(fields = {}) {
  const $route = { params: { id: 'exp' }, query: {} };
  const vm = {
    ...RunningExperiment.data.call({
      $route,
      offeredTabs: methods.offeredTabs,
    }),
    $route,
    $buefy: { toast: { open: vi.fn() } },
    vmSeqs: { record: vi.fn() },
    ...fields,
  };
  for (const name of ['handleVmEvent', 'resetOpticalDiscModal']) {
    vm[name] ??= methods[name].bind(vm);
  }
  return vm;
}

describe('RunningExperiment', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('a backing image in progress marks the VM it names busy', () => {
    const vm = instance();
    vm.experiment = { vms: [{ name: 'vm-1' }, { name: 'vm-2' }] };

    methods.handleWs.call(vm, {
      resource: {
        type: 'experiment/vm/commit',
        action: 'committing',
        name: 'exp/vm-2',
      },
      result: { disk: 'vm-2.qc2' },
    });

    expect(vm.experiment.vms).toEqual([
      { name: 'vm-1' },
      { name: 'vm-2', busy: true, percent: 0 },
    ]);
    expect(vm.$buefy.toast.open).toHaveBeenCalledOnce();
  });

  test('changing the optical disc resets the dialog', () => {
    const vm = instance();
    const closed = vm.opticalDiscModal;
    vm.opticalDiscModal = {
      ...closed,
      active: true,
      disc: '/phenix/images/tools.iso',
      vmName: 'vm-1',
    };

    methods.insertOpticalDisc.call(vm);

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'experiments/exp/vms/vm-1/cdrom',
      null,
      { params: { isoPath: '/phenix/images/tools.iso' } },
    );
    expect(vm.opticalDiscModal).toEqual(closed);
  });

  test('every selection action has its own handler', () => {
    const handlerOf = (action) => {
      const handlers = Object.fromEntries(
        Object.keys(methods).map((name) => [name, vi.fn()]),
      );
      const vm = instance({ ...handlers, vmSelectedArray: ['vm-1'] });
      methods.actOnSelection.call(vm, action);
      return Object.keys(handlers).filter(
        (name) => handlers[name].mock.calls.length > 0,
      );
    };

    const handlers = SELECTION_ACTIONS.map(handlerOf);

    expect(handlers.every((called) => called.length === 1)).toBe(true);
    expect(new Set(handlers.flat()).size).toBe(SELECTION_ACTIONS.length);
  });

  test('the Capture Snapshot action captures the selected VMs', () => {
    const vm = instance({ captureSnapshot: vi.fn() });
    vm.vmSelectedArray = ['vm-1', 'vm-2'];

    methods.actOnSelection.call(vm, 'snapshot');

    expect(vm.captureSnapshot).toHaveBeenCalledWith(['vm-1', 'vm-2']);
    expect(vm.vmSelectedArray).toEqual([]);
  });
});
