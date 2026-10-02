// Which VM actions the running experiment page offers, for which VM states and
// roles, and how it colors a VM's state.
import { describe, expect, it } from 'vitest';

import {
  CAPTURE_ICON,
  POWER_ACTIONS,
  ROW_ACTIONS,
  rowActionNames,
  TOOL_ACTIONS,
  captureTargets,
  partitionVMsForAction,
  skippedVMsText,
  vmActionState,
  vmActionStates,
  vmInterfaces,
  vmStateInfo,
  vmStateType,
} from '@/components/experiment/vmActions.js';

const running = {
  name: 'vm1',
  state: 'RUNNING',
  running: true,
  snapshot: true,
  ccActive: true,
};
const paused = { ...running, state: 'PAUSED', running: false };
const quit = { ...running, state: 'QUIT', running: false };
const persistent = { ...running, snapshot: false };
const agentless = { ...running, ccActive: false };
const busy = { ...running, busy: true };

const all = () => true;
const none = () => false;
// a role allowed only the given resource/verb pairs
const only =
  (...pairs) =>
  (resource, verb) =>
    pairs.some(([r, v]) => r === resource && v === verb);

const NOT_RUNNING = 'the VM is not running';
const ALREADY_RUNNING = 'the VM is already running';
const PAUSED = 'the VM is paused and must be resumed first';
const NO_SNAPSHOTS = 'the VM does not have snapshots enabled';
const NO_AGENT = 'it needs an active miniccc agent in the VM';
const BUSY = 'the VM is busy with another action';

// The state rule of every action in the details modal: why it is ruled out
// for a VM in each state it is, and allowed in the others. A busy VM can take
// none of them. The capture actions' rules are in 'capture all interfaces'
// and 'rowActionNames' below.
const STATES = {
  running,
  paused,
  'shut down': quit,
  persistent,
  agentless,
  busy,
};
const RULES = {
  start: {
    running: ALREADY_RUNNING,
    persistent: ALREADY_RUNNING,
    agentless: ALREADY_RUNNING,
  },
  pause: { paused: NOT_RUNNING, 'shut down': NOT_RUNNING },
  restart: { paused: PAUSED },
  shutdown: { paused: NOT_RUNNING, 'shut down': NOT_RUNNING },
  kill: { paused: NOT_RUNNING, 'shut down': NOT_RUNNING },
  redeploy: {},
  resetDisk: { paused: PAUSED, persistent: NO_SNAPSHOTS },
  snapshot: {
    paused: NOT_RUNNING,
    'shut down': NOT_RUNNING,
    persistent: NO_SNAPSHOTS,
  },
  commit: {
    paused: NOT_RUNNING,
    'shut down': NOT_RUNNING,
    persistent: NO_SNAPSHOTS,
  },
  memorySnapshot: { paused: NOT_RUNNING, 'shut down': NOT_RUNNING },
  portForward: {
    paused: NOT_RUNNING,
    'shut down': NOT_RUNNING,
    agentless: NO_AGENT,
  },
  cdrom: { paused: NOT_RUNNING, 'shut down': NOT_RUNNING },
  mount: {
    paused: NOT_RUNNING,
    'shut down': NOT_RUNNING,
    agentless: NO_AGENT,
  },
};
const RULE_ROWS = Object.entries(RULES).flatMap(([action, blocked]) =>
  Object.keys(STATES).map((state) => {
    const reason = state === 'busy' ? BUSY : (blocked[state] ?? null);
    return { action, state, reason, outcome: reason ?? 'allowed' };
  }),
);

// how each action's tooltip starts when the action is ruled out
const RULED_OUT = {
  start: "Can't start",
  pause: "Can't pause",
  restart: "Can't restart",
  shutdown: "Can't shut down",
  kill: "Can't kill",
  redeploy: "Can't redeploy",
  resetDisk: "Can't reset disk",
  snapshot: "Can't snapshot",
  commit: "Can't create a backing image",
  memorySnapshot: "Can't create a memory snapshot",
  portForward: "Can't forward a port",
  cdrom: "Can't insert a CD-ROM",
  mount: "Can't mount disk",
};

// the actions the page takes on a selection of VMs
const SELECTION_ACTIONS = [
  'start',
  'pause',
  'restart',
  'shutdown',
  'kill',
  'redeploy',
  'resetDisk',
  'snapshot',
  'commit',
  'memorySnapshot',
];

describe('the state rules', () => {
  it('cover every action of the details modal', () => {
    expect(Object.keys(RULES).sort()).toEqual(
      [...POWER_ACTIONS, ...TOOL_ACTIONS].sort(),
    );
  });

  // the button for one VM keeps a ruled out action visible with the reason,
  // and a selection skips the VM for it
  it.each(RULE_ROWS)(
    '$action on a $state VM: $outcome',
    ({ action, state, reason }) => {
      const vm = STATES[state];
      const button = vmActionState(action, vm, {
        can: all,
        features: ['vm-mount'],
      });
      expect(button.permitted).toBe(true);
      expect(button.disabled).toBe(reason !== null);
      expect(button.reason).toBe(reason && `${RULED_OUT[action]}: ${reason}`);
      expect(partitionVMsForAction(action, vm.name, [vm]).skipped).toEqual(
        reason ? [{ reason, names: [vm.name] }] : [],
      );
    },
  );
});

describe('vmStateInfo', () => {
  it.each([
    ['RUNNING', 'is-success', 'Running'],
    ['PAUSED', 'is-warning', 'Paused'],
    ['QUIT', 'is-danger', 'Shut down'],
    ['ERROR', 'is-danger', 'Error'],
    ['BUILDING', 'is-info', 'Building'],
  ])('shows %s as %s', (state, type, label) => {
    expect(vmStateInfo({ state })).toMatchObject({ type, label });
  });

  it('shows a busy VM as busy whatever its state', () => {
    expect(vmStateInfo({ state: 'RUNNING', busy: true })).toMatchObject({
      type: 'is-warning',
      label: 'Busy',
    });
  });

  it('shows external, delayed, and not-booted VMs', () => {
    expect(vmStateInfo({ external: true, state: 'EXTERNAL' }).label).toBe(
      'External',
    );
    expect(vmStateInfo({ state: 'BUILDING', delayed_start: '5m' })).toEqual(
      expect.objectContaining({ type: 'is-info', label: 'Delayed start' }),
    );
    expect(vmStateInfo({ state: '', dnb: true })).toEqual(
      expect.objectContaining({ type: null, label: 'Not booted' }),
    );
  });

  it('shows an unknown state in gray', () => {
    expect(vmStateInfo({ state: 'WEIRD' })).toMatchObject({
      type: null,
      label: 'Weird',
    });
    expect(vmStateInfo({})).toMatchObject({ type: null, label: 'Unknown' });
  });

  it("gives the table's name tag the state's color, and none for gray", () => {
    expect(vmStateType({ state: 'PAUSED' })).toBe('is-warning');
    expect(vmStateType({ state: 'EXTERNAL', external: true })).toBeUndefined();
  });
});

describe('vmActionState', () => {
  it('labels start as resume for a paused VM', () => {
    expect(vmActionState('start', paused, { can: all }).label).toBe('Resume');
    expect(vmActionState('start', quit, { can: all }).label).toBe('Start');
  });

  it('offers to change the CD-ROM of a VM that has one', () => {
    const vm = { ...quit, cdRom: '/iso/a.iso' };
    expect(vmActionState('cdrom', vm, { can: all })).toMatchObject({
      label: 'Change CD-ROM',
      reason: "Can't change the CD-ROM: the VM is not running",
    });
  });

  it("marks actions the role can't take as not permitted", () => {
    const states = vmActionStates(POWER_ACTIONS, running, { can: none });
    expect(states.every((a) => !a.permitted && a.disabled)).toBe(true);
    expect(states[0].reason).toBe("Can't start: you don't have permission");
  });

  it('checks each action against its own permission', () => {
    const can = only(['vms/shutdown', 'update'], ['vms/snapshots', 'create']);
    const states = vmActionStates(ROW_ACTIONS, running, { can });
    expect(states.map((a) => [a.name, a.permitted])).toEqual([
      ['shutdown', true],
      ['restart', false],
      ['portForward', false],
      ['snapshot', true],
      ['commit', false],
      ['captureAll', false],
    ]);
  });

  it('needs every permission an action lists', () => {
    const can = only(['vms/cdrom', 'update']);
    expect(vmActionState('cdrom', running, { can }).permitted).toBe(false);
    expect(
      vmActionState('cdrom', running, {
        can: only(['vms/cdrom', 'update'], ['vms/cdrom', 'delete']),
      }).permitted,
    ).toBe(true);
  });

  it('offers nothing for an external node', () => {
    const states = vmActionStates(
      ROW_ACTIONS,
      { external: true },
      { can: all },
    );
    expect(states.every((a) => !a.permitted)).toBe(true);
  });

  it('offers mounting only when the server supports it', () => {
    expect(vmActionState('mount', running, { can: all }).permitted).toBe(false);
    expect(
      vmActionState('mount', running, { can: all, features: ['vm-mount'] })
        .permitted,
    ).toBe(true);
  });

  it('puts shutdown, restart, port forward, snapshot, backing image, and capture all in the table', () => {
    expect(ROW_ACTIONS).toEqual([
      'shutdown',
      'restart',
      'portForward',
      'snapshot',
      'commit',
      'captureAll',
    ]);
  });

  it('rejects an unknown action', () => {
    expect(() => vmActionState('explode', running, { can: all })).toThrow(
      /unknown VM action/,
    );
  });
});

describe('vmInterfaces', () => {
  it('pairs each network with its VLAN, IP, tap, and capture', () => {
    const vm = {
      networks: ['SW1 (149)', 'mgmt (151)', 'disconnected'],
      ipv4: ['10.0.0.5', '', '10.2.0.1'],
      taps: ['mega_tap1', 'mega_tap2'],
      captures: [{ vm: 'vm1', interface: 1, filepath: 'x.pcap' }],
    };
    expect(vmInterfaces(vm)).toEqual([
      {
        index: 0,
        network: 'sw1',
        vlan: '149',
        disconnected: false,
        ip: '10.0.0.5',
        tap: 'mega_tap1',
        capturing: false,
      },
      {
        index: 1,
        network: 'mgmt',
        vlan: '151',
        disconnected: false,
        ip: null,
        tap: 'mega_tap2',
        capturing: true,
      },
      {
        index: 2,
        network: 'disconnected',
        vlan: null,
        disconnected: true,
        ip: '10.2.0.1',
        tap: null,
        capturing: false,
      },
    ]);
  });

  it('handles a VM without networks or captures', () => {
    expect(vmInterfaces({})).toEqual([]);
    expect(vmInterfaces({ networks: ['a (1)'], captures: null })).toHaveLength(
      1,
    );
  });
});

describe('capture all interfaces', () => {
  const vm = {
    ...running,
    networks: ['DMZ (149)', 'disconnected', 'MGMT (151)'],
    captures: [{ vm: 'vm1', interface: 2 }],
  };

  it('targets the connected interfaces without a capture', () => {
    expect(captureTargets(vm)).toEqual([0]);
    expect(captureTargets({ ...vm, captures: [] })).toEqual([0, 2]);
    // a fresher capture list from the server wins over the VM's own
    expect(captureTargets(vm, [])).toEqual([0, 2]);
    expect(captureTargets(vm, [{ interface: 0 }, { interface: 2 }])).toEqual(
      [],
    );
  });

  it('uses the capture icon and needs permission to create captures', () => {
    const state = vmActionState('captureAll', vm, { can: all });
    expect(state).toMatchObject({
      icon: CAPTURE_ICON,
      permitted: true,
      disabled: false,
    });
    expect(
      vmActionState('captureAll', vm, { can: only(['vms/captures', 'list']) })
        .permitted,
    ).toBe(false);
    expect(
      vmActionState('captureAll', vm, {
        can: only(['vms/captures', 'create']),
      }).permitted,
    ).toBe(true);
  });

  it.each([
    ['not running', { ...vm, running: false }, /not running/],
    ['without interfaces', { ...vm, networks: [] }, /no interfaces/],
    [
      'with every interface disconnected',
      { ...vm, networks: ['disconnected'], captures: [] },
      /disconnected/,
    ],
    [
      'already capturing everything',
      { ...vm, captures: [{ interface: 0 }, { interface: 2 }] },
      /already being captured/,
    ],
  ])('is disabled for a VM %s, saying why', (_, target, reason) => {
    const state = vmActionState('captureAll', target, { can: all });
    expect(state.permitted).toBe(true);
    expect(state.disabled).toBe(true);
    expect(state.reason).toMatch(/^Can't capture all interfaces: /);
    expect(state.reason).toMatch(reason);
  });
});

describe('rowActionNames', () => {
  it('swaps capture all for stop all while the VM is capturing', () => {
    expect(rowActionNames({ captures: [] })).toContain('captureAll');
    const names = rowActionNames({ captures: [{ interface: 0 }] });
    expect(names).toContain('stopCaptures');
    expect(names).not.toContain('captureAll');
  });

  it('offers stop all only to roles that may stop captures', () => {
    const vm = {
      running: true,
      networks: ['DMZ (1)'],
      captures: [{ interface: 0 }],
    };
    const state = (can) => vmActionState('stopCaptures', vm, { can });
    expect(state(() => true)).toMatchObject({
      disabled: false,
      type: 'is-danger',
    });
    expect(state((_, verb) => verb !== 'delete')).toMatchObject({
      permitted: false,
    });
  });
});

describe('actions on a selection', () => {
  const vms = [
    { ...running, name: 'web' },
    { ...quit, name: 'off' },
    { ...persistent, name: 'persistent' },
    { ...running, name: 'db' },
    { ...quit, name: 'down' },
  ];

  it("splits the VMs by the action's rule, grouping the skipped ones by reason", () => {
    expect(
      partitionVMsForAction(
        'snapshot',
        ['web', 'off', 'persistent', 'db', 'down'],
        vms,
      ),
    ).toEqual({
      allowed: ['web', 'db'],
      skipped: [
        { reason: NOT_RUNNING, names: ['off', 'down'] },
        { reason: NO_SNAPSHOTS, names: ['persistent'] },
      ],
    });
  });

  it("skips a VM the table doesn't show, whatever the action", () => {
    for (const action of SELECTION_ACTIONS) {
      expect(partitionVMsForAction(action, ['gone'], vms)).toEqual({
        allowed: [],
        skipped: [
          { reason: 'the VM is not shown in the table', names: ['gone'] },
        ],
      });
    }
  });

  it('says which VMs were skipped and why, for one VM or several', () => {
    expect(
      skippedVMsText([
        { reason: NOT_RUNNING, names: ['off', 'down'] },
        { reason: NO_SNAPSHOTS, names: ['persistent'] },
        { reason: NO_SNAPSHOTS, names: ['c', 'd'] },
      ]),
    ).toBe(
      'The VMs off and down are not running. ' +
        'The persistent VM does not have snapshots enabled. ' +
        'The VMs c and d do not have snapshots enabled.',
    );
  });

  it('names the skipped VMs for every reason a selection can be skipped for', () => {
    const reasons = RULE_ROWS.filter(
      (row) => SELECTION_ACTIONS.includes(row.action) && row.reason,
    ).map((row) => row.reason);
    for (const reason of new Set([
      ...reasons,
      'the VM is not shown in the table',
    ])) {
      expect(skippedVMsText([{ reason, names: ['a', 'b'] }])).toMatch(
        /^The VMs a and b (are|do) .*\.$/,
      );
    }
  });
});
