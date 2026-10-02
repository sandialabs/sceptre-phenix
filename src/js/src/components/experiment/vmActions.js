import { isInterfaceCapturing } from '@/utils/captures.js';
import { namedList, pluralWord, upperFirst } from '@/utils/plural.js';

// What the running experiment page shows for a VM's state, and which VM
// actions a role may take on a VM in its current state. The details modal, the
// VM table's Actions column, and the selection toolbar share these so they
// always agree.

// the icon of every button that starts a packet capture (see utils/icons.js)
export const CAPTURE_ICON = 'shark-fin';

// Bulma color, label, and icon for each minimega VM state
// (BUILDING, RUNNING, PAUSED, QUIT, ERROR); vmStateInfo adds phenix's own
const STATES = {
  RUNNING: { label: 'Running', type: 'is-success', icon: 'play' },
  PAUSED: { label: 'Paused', type: 'is-warning', icon: 'pause' },
  QUIT: { label: 'Shut down', type: 'is-danger', icon: 'power-off' },
  ERROR: { label: 'Error', type: 'is-danger', icon: 'exclamation-triangle' },
  BUILDING: { label: 'Building', type: 'is-info', icon: 'hourglass-half' },
};

// how a VM's state is shown: label, Bulma color type (null for gray), icon
export function vmStateInfo(vm) {
  if (vm.busy) return { label: 'Busy', type: 'is-warning', icon: 'sync-alt' };
  if (vm.external) return { label: 'External', type: null, icon: 'plug' };

  const state = (vm.state ?? '').toUpperCase();
  if (state === 'BUILDING' && vm.delayed_start) {
    return { label: 'Delayed start', type: 'is-info', icon: 'hourglass-half' };
  }
  if (STATES[state]) return { ...STATES[state] };
  // a do-not-boot VM that minimega has not launched has no state
  if (!state && vm.dnb) {
    return { label: 'Not booted', type: null, icon: 'ban' };
  }
  return {
    label: state ? state.charAt(0) + state.slice(1).toLowerCase() : 'Unknown',
    type: null,
    icon: 'circle',
  };
}

// the Bulma color type the VM table's name tag uses for a VM
export function vmStateType(vm) {
  return vmStateInfo(vm).type ?? undefined;
}

const paused = (vm) => (vm.state ?? '').toUpperCase() === 'PAUSED';

// why a paused VM can be neither restarted nor reset
const PAUSED = 'the VM is paused and must be resumed first';
// why a busy VM can take no action
const BUSY = 'the VM is busy with another action';

// Each action: its label, what it does (its tooltip), its icon, the Bulma
// color type of its button, the RBAC permissions it needs (all of them), and
// why the VM's state rules it out (null when it is allowed). These rules gate
// the buttons for one VM, and partitionVMsForAction applies them to a
// selection. A reason reads after "Can't <verb>: " in a button's tooltip,
// where the verb is the label in lowercase unless the action gives one; the
// reasons of the actions the page takes on a selection start "the VM is" or
// "the VM does", so skippedVMsText can say them of several VMs.
export const VM_ACTIONS = {
  start: {
    label: (vm) => (paused(vm) ? 'Resume' : 'Start'),
    description: (vm) =>
      paused(vm) ? 'Resume the paused VM where it left off' : 'Start the VM',
    icon: 'play',
    type: 'is-success',
    rbac: [['vms/start', 'update']],
    blocked: (vm) => (vm.running ? 'the VM is already running' : null),
  },
  pause: {
    label: 'Pause',
    description: 'Pause the VM, keeping its memory, until it is resumed',
    icon: 'pause',
    type: 'is-warning',
    rbac: [['vms/stop', 'update']],
    blocked: (vm) => (vm.running ? null : 'the VM is not running'),
  },
  restart: {
    label: 'Restart',
    description: 'Restart the VM, or start it if it is shut down',
    icon: 'sync-alt',
    type: 'is-warning',
    outlined: true,
    rbac: [['vms/restart', 'update']],
    blocked: (vm) => (paused(vm) ? PAUSED : null),
  },
  shutdown: {
    label: 'Shut down',
    description: 'Power off the VM; it can be started again later',
    icon: 'power-off',
    type: 'is-danger',
    outlined: true,
    rbac: [['vms/shutdown', 'update']],
    blocked: (vm) => (vm.running ? null : 'the VM is not running'),
  },
  kill: {
    label: 'Kill',
    description: 'Kill the VM and remove it from the running experiment',
    icon: 'skull-crossbones',
    type: 'is-danger',
    rbac: [['vms', 'delete']],
    blocked: (vm) => (vm.running ? null : 'the VM is not running'),
  },
  redeploy: {
    label: 'Redeploy',
    description:
      'Kill the VM and deploy it again, optionally with new CPUs, memory or disk',
    icon: 'history',
    rbac: [['vms/redeploy', 'update']],
    blocked: () => null,
  },
  resetDisk: {
    label: 'Reset disk',
    description:
      "Reset the VM's disk to how it was when the experiment started, discarding changes",
    icon: 'undo-alt',
    rbac: [['vms/reset', 'update']],
    blocked: (vm) => {
      if (!vm.snapshot) return 'the VM does not have snapshots enabled';
      if (paused(vm)) return PAUSED;
      return null;
    },
  },
  snapshot: {
    label: 'Snapshot',
    description:
      "Save the VM's current disk and memory as a snapshot that can be restored later",
    icon: 'camera',
    rbac: [['vms/snapshots', 'create']],
    blocked: (vm) => {
      if (!vm.snapshot) return 'the VM does not have snapshots enabled';
      if (!vm.running) return 'the VM is not running';
      return null;
    },
  },
  commit: {
    label: 'Backing image',
    verb: 'create a backing image',
    description: 'Create a new backing image from the current VM disk',
    icon: 'save',
    rbac: [['vms/commit', 'create']],
    blocked: (vm) => {
      if (!vm.snapshot) return 'the VM does not have snapshots enabled';
      if (!vm.running) return 'the VM is not running';
      return null;
    },
  },
  memorySnapshot: {
    label: 'Memory snapshot',
    verb: 'create a memory snapshot',
    description:
      "Save a dump of the VM's memory for forensics tools such as Volatility",
    icon: 'database',
    rbac: [['vms/memorySnapshot', 'create']],
    blocked: (vm) => (vm.running ? null : 'the VM is not running'),
  },
  portForward: {
    label: 'Port forward',
    verb: 'forward a port',
    description: 'Forward a port to a port on the VM through its miniccc agent',
    icon: 'arrow-right',
    rbac: [['vms/forwards', 'create']],
    blocked: (vm) => {
      if (!vm.running) return 'the VM is not running';
      if (!vm.ccActive) return 'it needs an active miniccc agent in the VM';
      return null;
    },
  },
  cdrom: {
    label: (vm) => (vm.cdRom ? 'Change CD-ROM' : 'Insert CD-ROM'),
    verb: (vm) => (vm.cdRom ? 'change the CD-ROM' : 'insert a CD-ROM'),
    description: (vm) =>
      vm.cdRom
        ? "Change or eject the ISO image in the VM's CD-ROM drive"
        : "Insert an ISO image into the VM's CD-ROM drive",
    icon: 'compact-disc',
    rbac: [
      ['vms/cdrom', 'update'],
      ['vms/cdrom', 'delete'],
    ],
    blocked: (vm) => (vm.running ? null : 'the VM is not running'),
  },
  mount: {
    label: 'Mount disk',
    description:
      "Mount the VM's filesystem to browse, upload and download files",
    icon: 'hdd',
    feature: 'vm-mount',
    rbac: [['vms/mount', 'post']],
    blocked: (vm) => {
      if (!vm.running) return 'the VM is not running';
      if (!vm.ccActive) return 'it needs an active miniccc agent in the VM';
      return null;
    },
  },
  captureAll: {
    label: 'Capture all interfaces',
    description:
      'Start a packet capture on every connected interface of the VM',
    icon: CAPTURE_ICON,
    rbac: [['vms/captures', 'create']],
    blocked: (vm) => {
      if (!vm.running) return 'the VM is not running';
      if (!(vm.networks ?? []).length) return 'the VM has no interfaces';
      if (!vmInterfaces(vm).some((i) => !i.disconnected)) {
        return 'all its interfaces are disconnected';
      }
      if (!captureTargets(vm).length) {
        return 'every connected interface is already being captured';
      }
      return null;
    },
  },
  // takes captureAll's place in the Actions column while the VM is capturing
  stopCaptures: {
    label: 'Stop all packet captures',
    description: 'Stop every packet capture running on the VM',
    icon: 'stop',
    type: 'is-danger',
    rbac: [['vms/captures', 'delete']],
    blocked: (vm) =>
      (vm.captures ?? []).length ? null : 'no packet capture is running',
  },
};

// power operations, shown first in the details modal
export const POWER_ACTIONS = ['start', 'pause', 'restart', 'shutdown', 'kill'];
// the rest of the details modal's actions
export const TOOL_ACTIONS = [
  'redeploy',
  'resetDisk',
  'snapshot',
  'commit',
  'memorySnapshot',
  'portForward',
  'cdrom',
  'mount',
];
// the VM table's Actions column
export const ROW_ACTIONS = [
  'shutdown',
  'restart',
  'portForward',
  'snapshot',
  'commit',
  'captureAll',
];

// The VM table's Actions column for one VM: capturing VMs get a button that
// stops their captures in place of the one that starts them.
export function rowActionNames(vm) {
  const capturing = (vm?.captures ?? []).length > 0;
  return ROW_ACTIONS.map((name) =>
    name === 'captureAll' && capturing ? 'stopCaptures' : name,
  );
}

// whether `can(resource, verb)` grants every permission the action needs
export function actionPermitted(name, can) {
  return VM_ACTIONS[name].rbac.every(([resource, verb]) => can(resource, verb));
}

// why the VM's state rules the action out, or null when it is allowed
function stateBlocks(action, vm) {
  return vm.busy ? BUSY : action.blocked(vm);
}

// an action's label, verb, or description, some of which depend on the VM
const forVm = (text, vm) => (typeof text === 'function' ? text(vm) : text);

// Whether and how to offer an action for a VM. `can(resource, verb)` answers
// RBAC for this VM; `features` lists the server's enabled features.
//   permitted: the role may take the action (and the server supports it)
//   disabled: it cannot be taken now; `reason` says why
export function vmActionState(name, vm, { can, features = [] }) {
  const action = VM_ACTIONS[name];
  if (!action) throw new Error(`unknown VM action ${name}`);

  const label = forVm(action.label, vm);
  const verb = action.verb ? forVm(action.verb, vm) : label.toLowerCase();
  const result = {
    name,
    label,
    description: forVm(action.description, vm),
    icon: action.icon,
    type: action.type ?? null,
    outlined: !!action.outlined,
    permitted: true,
    disabled: false,
    reason: null,
  };

  const deny = (reason, permitted = true) => ({
    ...result,
    permitted,
    disabled: true,
    reason: `Can't ${verb}: ${reason}`,
  });

  if (vm.external) return deny('external nodes are not VMs', false);
  if (action.feature && !features.includes(action.feature)) {
    return deny('the server does not support it', false);
  }
  if (!actionPermitted(name, can)) {
    return deny("you don't have permission", false);
  }

  const blocked = stateBlocks(action, vm);
  if (blocked) return deny(blocked);

  return result;
}

// the action states for a list of action names
export function vmActionStates(names, vm, opts) {
  return names.map((name) => vmActionState(name, vm, opts));
}

// Splits the VMs named for an action on a selection by the rules that disable
// the action's button for one VM: `allowed` lists those it can act on, and
// `skipped` groups the rest by why they were left out. `vms` are the VMs the
// table shows; a VM it doesn't (on another page, filtered out, or killed) is
// skipped, since its state is unknown.
export function partitionVMsForAction(name, names, vms) {
  const action = VM_ACTIONS[name];
  const byName = new Map((vms ?? []).map((vm) => [vm.name, vm]));
  const allowed = [];
  const skipped = new Map();
  for (const vmName of [names].flat()) {
    const vm = byName.get(vmName);
    const reason = vm
      ? stateBlocks(action, vm)
      : 'the VM is not shown in the table';
    if (reason) skipped.set(reason, [...(skipped.get(reason) ?? []), vmName]);
    else allowed.push(vmName);
  }
  return {
    allowed,
    skipped: [...skipped].map(([reason, list]) => ({ reason, names: list })),
  };
}

const PLURAL_VERBS = { is: 'are', does: 'do' };

// One sentence for each reason partitionVMsForAction skipped VMs for, turning
// a reason about "the VM" into one about all of them: "The VMs a and b are
// not running."
export function skippedVMsText(skipped) {
  return skipped
    .map(({ reason, names }) => {
      const subject = upperFirst(namedList(names, 'VM'));
      const text = reason.replace(
        /^the VM (is|does)\b/,
        (_, verb) =>
          `${subject} ${pluralWord(names.length, verb, PLURAL_VERBS[verb])}`,
      );
      return `${text}.`;
    })
    .join(' ');
}

// Splits minimega's "alias (vlan)" network names, pairing each interface with
// its IP, tap, and whether a packet capture is running on it.
export function vmInterfaces(vm) {
  const networks = vm?.networks ?? [];
  return networks.map((network, index) => {
    const match = /^(.*?)\s*\((\d+)\)$/.exec(network ?? '');
    const disconnected = (network ?? '').toLowerCase() === 'disconnected';
    return {
      index,
      network: (match ? match[1] : network || 'unknown').toLowerCase(),
      vlan: match ? match[2] : null,
      disconnected,
      ip: vm.ipv4?.[index] || null,
      tap: vm.taps?.[index] || null,
      capturing: isInterfaceCapturing(vm.captures, index),
    };
  });
}

// The interfaces "capture all interfaces" starts captures on: each connected
// interface without a capture running. `captures` defaults to the VM's own
// list; pass a fresher one when there is one.
export function captureTargets(vm, captures = vm?.captures) {
  return vmInterfaces({ ...vm, captures })
    .filter((iface) => !iface.disconnected && !iface.capturing)
    .map((iface) => iface.index);
}
