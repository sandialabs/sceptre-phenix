// Which VM actions the running experiment page offers, for which VM states and
// roles, and how it colors a VM's state.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  CAPTURE_ICON,
  POWER_ACTIONS,
  ROW_ACTIONS,
  rowActionNames,
  TOOL_ACTIONS,
  VM_ACTIONS,
  captureTargets,
  partitionVMsForAction,
  skippedVMsText,
  vmActionState,
  vmActionStates,
  vmButtonStates,
  vmInterfaces,
  vmStateInfo,
  vmStateType,
} from '@/components/experiment/vmActions.js';
import { applyStopCaptureUpdate } from '@/utils/captures.js';
import { allowAll, allowOnly, denyAll } from './helpers/permissions.js';

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

const NOT_RUNNING = 'the VM is not running';
const ALREADY_RUNNING = 'the VM is already running';
const PAUSED = 'the VM is paused. Resume paused VMs first';
const NO_SNAPSHOTS = 'the VM does not have snapshots enabled';
const NO_AGENT = 'it needs an active miniccc agent in the VM';
const BUSY = 'the VM is busy with another action';

// Why each details-modal action is ruled out by VM state; a busy VM takes none.
// The capture rules are in 'capture all interfaces' and 'rowActionNames'.
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
  start: 'Cannot start',
  pause: 'Cannot pause',
  restart: 'Cannot restart',
  shutdown: 'Cannot shut down',
  kill: 'Cannot kill',
  redeploy: 'Cannot redeploy',
  resetDisk: 'Cannot reset disk',
  snapshot: 'Cannot snapshot',
  commit: 'Cannot create a backing image',
  memorySnapshot: 'Cannot create a memory snapshot',
  portForward: 'Cannot forward a port',
  cdrom: 'Cannot insert a CD-ROM',
  mount: 'Cannot mount disk',
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

// a VM in any mix of the states the rules read, on a few interfaces
const networkArb = fc.constantFrom(
  'dmz (1)',
  'MGMT (2)',
  'disconnected',
  'plain',
  '',
);
const vmArb = fc.record({
  name: fc.constantFrom('a', 'b', 'c', 'd'),
  state: fc.oneof(
    fc.constantFrom('RUNNING', 'paused', 'Paused', 'QUIT', 'BUILDING', ''),
    fc.string({ maxLength: 8 }),
  ),
  running: fc.boolean(),
  snapshot: fc.boolean(),
  ccActive: fc.boolean(),
  busy: fc.boolean(),
  cdRom: fc.constantFrom(undefined, '/iso/a.iso'),
  networks: fc.array(networkArb, { maxLength: 3 }),
  captures: fc.array(fc.record({ interface: fc.nat(3) }), { maxLength: 3 }),
});
const actionArb = fc.constantFrom(...Object.keys(VM_ACTIONS));

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
        can: allowAll,
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

  it('give every button a reason exactly when it is disabled', () => {
    fc.assert(
      fc.property(
        actionArb,
        vmArb,
        fc.boolean(),
        fc.func(fc.boolean()),
        fc.subarray(['vm-mount']),
        (name, vm, external, can, features) => {
          const opts = { can, features };
          const button = vmActionState(name, { ...vm, external }, opts);
          expect(button.disabled).toBe(button.reason !== null);
          if (!button.permitted) expect(button.disabled).toBe(true);
          if (external) {
            expect(button.permitted).toBe(false);
            expect(button.reason).toMatch(/: external nodes are not VMs$/);
          }
          if (button.reason) expect(button.reason).toMatch(/^Cannot \S.*: \S/);
          if (vm.busy && button.permitted) {
            expect(button.reason?.endsWith(`: ${BUSY}`)).toBe(true);
          }
          // a permitted action on an idle VM is enabled unless its rule says no
          if (button.permitted && !vm.busy) {
            expect(button.disabled).toBe(!!VM_ACTIONS[name].blocked(vm));
          }
        },
      ),
    );
  });

  // the table's checkboxes leave external nodes out of a selection
  it('skip a VM in a selection exactly when its button says why', () => {
    const shown = fc.uniqueArray(vmArb, { selector: (vm) => vm.name });
    const picked = fc.shuffledSubarray(['a', 'b', 'c', 'd', 'gone']);
    fc.assert(
      fc.property(actionArb, shown, picked, (name, vms, names) => {
        const { allowed, skipped } = partitionVMsForAction(name, names, vms);
        const flat = [...allowed, ...skipped.flatMap((group) => group.names)];
        expect(flat.toSorted()).toEqual(names.toSorted());
        expect(allowed).toEqual(names.filter((n) => allowed.includes(n)));

        const reasons = new Map(
          skipped.flatMap((g) => g.names.map((n) => [n, g.reason])),
        );
        for (const vmName of names) {
          const vm = vms.find((v) => v.name === vmName);
          const reason = reasons.get(vmName);
          if (!vm) {
            expect(reason).toBe('the VM is not in the table');
            continue;
          }
          const button = vmActionState(name, vm, {
            can: allowAll,
            features: ['vm-mount'],
          });
          expect(button.disabled).toBe(!!reason);
          if (reason) expect(button.reason.endsWith(`: ${reason}`)).toBe(true);
        }
      }),
    );
  });
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

  it('reads a state in any case alike, busy before external before delayed', () => {
    const colors = ['is-success', 'is-warning', 'is-danger', 'is-info', null];
    fc.assert(
      fc.property(
        vmArb,
        fc.record({
          dnb: fc.boolean(),
          external: fc.boolean(),
          delayed_start: fc.constantFrom(undefined, '', '5m'),
        }),
        (target, extra) => {
          const vm = { ...target, ...extra };
          const info = vmStateInfo(vm);
          expect(vmStateInfo({ ...vm, state: vm.state.toLowerCase() })).toEqual(
            info,
          );
          expect(colors).toContain(info.type);
          expect(info.label).toEqual(expect.any(String));
          expect(info.label).not.toBe('');
          if (vm.busy) expect(info.label).toBe('Busy');
          else if (vm.external) expect(info.label).toBe('External');
          else if (vm.state.toUpperCase() === 'BUILDING') {
            expect(info.label).toBe(
              vm.delayed_start ? 'Delayed start' : 'Building',
            );
          }
        },
      ),
    );
  });

  it("gives the table's name tag the state's color, and none for gray", () => {
    expect(vmStateType({ state: 'PAUSED' })).toBe('is-warning');
    expect(vmStateType({ state: 'EXTERNAL', external: true })).toBeUndefined();
  });
});

describe('vmActionState', () => {
  it('labels start as resume for a paused VM', () => {
    expect(vmActionState('start', paused, { can: allowAll }).label).toBe(
      'Resume',
    );
    expect(vmActionState('start', quit, { can: allowAll }).label).toBe('Start');
  });

  it('offers to change the CD-ROM of a VM that has one', () => {
    const vm = { ...quit, cdRom: '/iso/a.iso' };
    expect(vmActionState('cdrom', vm, { can: allowAll })).toMatchObject({
      label: 'Change CD-ROM',
      reason: 'Cannot change the CD-ROM: the VM is not running',
    });
  });

  it("marks actions the role can't take as not permitted", () => {
    const states = vmActionStates(POWER_ACTIONS, running, { can: denyAll });
    expect(states.every((a) => !a.permitted && a.disabled)).toBe(true);
    expect(states[0].reason).toBe('Cannot start: you do not have permission');
  });

  it('checks each action against its own permission', () => {
    const can = allowOnly(
      ['vms/shutdown', 'update'],
      ['vms/snapshots', 'create'],
    );
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
    const can = allowOnly(['vms/cdrom', 'update']);
    expect(vmActionState('cdrom', running, { can }).permitted).toBe(false);
    expect(
      vmActionState('cdrom', running, {
        can: allowOnly(['vms/cdrom', 'update'], ['vms/cdrom', 'delete']),
      }).permitted,
    ).toBe(true);
  });

  it('offers nothing for an external node', () => {
    const states = vmActionStates(
      ROW_ACTIONS,
      { external: true },
      { can: allowAll },
    );
    expect(states.every((a) => !a.permitted)).toBe(true);
  });

  it('offers mounting only when the server supports it', () => {
    expect(vmActionState('mount', running, { can: allowAll }).permitted).toBe(
      false,
    );
    expect(
      vmActionState('mount', running, { can: allowAll, features: ['vm-mount'] })
        .permitted,
    ).toBe(true);
  });

  it('rejects an unknown action', () => {
    expect(() => vmActionState('explode', running, { can: allowAll })).toThrow(
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

  it("reads back each network's alias and VLAN, in order", () => {
    const alias = fc.string({ maxLength: 8 }).filter((a) => a.trimEnd() === a);
    const iface = fc.oneof(
      fc.record({ alias, vlan: fc.nat(4094) }),
      fc.record({
        alias: fc.oneof(
          fc.constantFrom('disconnected', 'Disconnected', ''),
          alias.filter((a) => !/\(\d+\)$/.test(a)),
        ),
      }),
    );
    fc.assert(
      fc.property(fc.array(iface, { maxLength: 4 }), (ifaces) => {
        const networks = ifaces.map(({ alias, vlan }) =>
          vlan === undefined ? alias : `${alias} (${vlan})`,
        );
        expect(vmInterfaces({ networks })).toEqual(
          ifaces.map(({ alias, vlan }, index) =>
            expect.objectContaining({
              index,
              network: (vlan === undefined
                ? alias || 'unknown'
                : alias
              ).toLowerCase(),
              vlan: vlan === undefined ? null : String(vlan),
              disconnected:
                vlan === undefined && alias.toLowerCase() === 'disconnected',
            }),
          ),
        );
      }),
    );
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

  it('targets the connected interfaces, in order, until each is captured', () => {
    fc.assert(
      fc.property(vmArb, fc.nat(3), (target, n) => {
        const vm = { ...target, running: true, busy: false };
        const ifaces = vmInterfaces(vm);
        const connected = ifaces
          .filter((i) => !i.disconnected)
          .map((i) => i.index);
        const targets = captureTargets(vm);
        expect(captureTargets(vm, [])).toEqual(connected);
        expect(targets.every((i) => !ifaces[i].capturing)).toBe(true);
        expect(connected.filter((i) => targets.includes(i))).toEqual(targets);
        // stopping interface n's capture makes it a target if it is connected
        const stopped = applyStopCaptureUpdate(vm.captures, { interface: n });
        expect(captureTargets(vm, stopped)).toEqual(
          connected.filter((i) => i === n || targets.includes(i)),
        );
        expect(
          vmActionState('captureAll', vm, { can: allowAll }).disabled,
        ).toBe(!targets.length);
      }),
    );
  });

  it('uses the capture icon and needs permission to create captures', () => {
    const state = vmActionState('captureAll', vm, { can: allowAll });
    expect(state).toMatchObject({
      icon: CAPTURE_ICON,
      permitted: true,
      disabled: false,
    });
    expect(
      vmActionState('captureAll', vm, {
        can: allowOnly(['vms/captures', 'list']),
      }).permitted,
    ).toBe(false);
    expect(
      vmActionState('captureAll', vm, {
        can: allowOnly(['vms/captures', 'create']),
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
      /already captures/,
    ],
  ])('is disabled for a VM %s, saying why', (_, target, reason) => {
    const state = vmActionState('captureAll', target, { can: allowAll });
    expect(state.permitted).toBe(true);
    expect(state.disabled).toBe(true);
    expect(state.reason).toMatch(/^Cannot capture all interfaces: /);
    expect(state.reason).toMatch(reason);
  });
});

describe('rowActionNames', () => {
  it('swaps shut down for start while the VM is not running', () => {
    expect(rowActionNames({ running: true })).toContain('shutdown');
    const names = rowActionNames({ running: false, state: 'QUIT' });
    expect(names).toContain('start');
    expect(names).not.toContain('shutdown');
    expect(names.indexOf('start')).toBe(ROW_ACTIONS.indexOf('shutdown'));
  });

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

describe('vmButtonStates', () => {
  it.each([
    [
      'shut down',
      { state: 'QUIT' },
      { label: 'Start', disabled: false },
      'the VM is not running. Start it instead',
    ],
    [
      'paused',
      { state: 'PAUSED' },
      { label: 'Resume', disabled: false },
      'the VM is paused. Resume paused VMs first',
    ],
    [
      'busy',
      { state: 'QUIT', busy: true },
      { label: 'Start', disabled: true },
      'the VM is busy with another action',
    ],
  ])(
    'greys out restart beside the start of a %s VM, saying why',
    (_, fields, start, why) => {
      const vm = { name: 'vm1', running: false, ...fields };
      const byName = Object.fromEntries(
        vmButtonStates(rowActionNames(vm), vm, { can: allowAll }).map((a) => [
          a.name,
          a,
        ]),
      );
      expect(byName.start).toMatchObject(start);
      expect(byName.restart).toMatchObject({
        disabled: true,
        reason: `Cannot restart: ${why}`,
      });
    },
  );

  it('greys out restart unless the VM is running', () => {
    const vm = fc.record({
      name: fc.constant('vm1'),
      running: fc.boolean(),
      state: fc.constantFrom('RUNNING', 'PAUSED', 'QUIT', 'BUILDING', ''),
      busy: fc.boolean(),
      snapshot: fc.boolean(),
      captures: fc.constantFrom([], [{ interface: 0 }]),
    });
    fc.assert(
      fc.property(vm, (target) => {
        const states = vmButtonStates(rowActionNames(target), target, {
          can: allowAll,
        });
        const names = states.map((a) => a.name);
        expect(names.includes('start')).toBe(!target.running);
        expect(names.includes('shutdown')).toBe(target.running);
        expect(states).toHaveLength(ROW_ACTIONS.length);
        const restart = states.find((a) => a.name === 'restart');
        expect(restart).toEqual(
          target.running
            ? vmActionState('restart', target, { can: allowAll })
            : expect.objectContaining({ disabled: true }),
        );
      }),
    );
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
        skipped: [{ reason: 'the VM is not in the table', names: ['gone'] }],
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
    for (const reason of new Set([...reasons, 'the VM is not in the table'])) {
      expect(skippedVMsText([{ reason, names: ['a', 'b'] }])).toMatch(
        /^The VMs a and b (are|do) .*\.$/,
      );
    }
  });
});
