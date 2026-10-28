// Calls the RunningExperiment methods with a stand-in for the component, so
// no DOM is needed.
import { beforeEach, describe, expect, test, vi } from 'vitest';
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

const { methods } = RunningExperiment;

function instance(fields = {}) {
  return {
    ...RunningExperiment.data.call({}),
    $route: { params: { id: 'exp' } },
    $buefy: { toast: { open: vi.fn() } },
    ...fields,
  };
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

  test('every multi-VM action has its own value', () => {
    const values = Object.values(instance().vmActions);

    expect(values.every(Number.isInteger)).toBe(true);
    expect(new Set(values).size).toBe(values.length);
  });

  test('the Capture Snapshot action captures the selected VMs', () => {
    const vm = instance({
      captureSnapshot: vi.fn(),
      unSelectAllVMs: vi.fn(),
      notImplemented: vi.fn(),
    });
    vm.vmSelectedArray = ['vm-1', 'vm-2'];

    methods.processMultiVmAction.call(vm, vm.vmActions.captureSnapshot);

    expect(vm.captureSnapshot).toHaveBeenCalledWith(['vm-1', 'vm-2']);
    expect(vm.notImplemented).not.toHaveBeenCalled();
  });
});
