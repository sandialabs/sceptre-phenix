import { isInterfaceCapturing } from '@/utils/captures.js';
import {
  actionState,
  partitionForAction,
  skippedText,
} from '@/utils/operations.js';

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
const PAUSED = 'the VM is paused. Resume paused VMs first';
// why a busy VM can take no action
const BUSY = 'the VM is busy with another action';
// why an action that needs a running VM is ruled out
const NOT_RUNNING = 'the VM is not running';

// Each action's label, tooltip (description), icon, Bulma color, the RBAC
// permissions it needs (all of them), and why the VM's state rules it out
// (null when allowed), which reads after "Can't <verb>: " in its tooltip.
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
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
  },
  restart: {
    label: 'Restart',
    description: 'Restart the running VM',
    icon: 'sync-alt',
    type: 'is-warning',
    outlined: true,
    rbac: [['vms/restart', 'update']],
    blocked: (vm) => {
      if (paused(vm)) return PAUSED;
      return vm.running ? null : NOT_RUNNING;
    },
  },
  shutdown: {
    label: 'Shut down',
    description: 'Power off the VM; it can be started again later',
    icon: 'power-off',
    type: 'is-danger',
    outlined: true,
    rbac: [['vms/shutdown', 'update']],
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
  },
  kill: {
    label: 'Kill',
    description: 'Kill the VM and remove it from the running experiment',
    icon: 'skull-crossbones',
    type: 'is-danger',
    rbac: [['vms', 'delete']],
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
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
      if (!vm.running) return NOT_RUNNING;
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
      if (!vm.running) return NOT_RUNNING;
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
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
  },
  portForward: {
    label: 'Port forward',
    verb: 'forward a port',
    description: 'Forward a port to a port on the VM through its miniccc agent',
    icon: 'arrow-right',
    rbac: [['vms/forwards', 'create']],
    blocked: (vm) => {
      if (!vm.running) return NOT_RUNNING;
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
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
  },
  mount: {
    label: 'Mount disk',
    description:
      "Mount the VM's filesystem to browse, upload and download files",
    icon: 'hdd',
    feature: 'vm-mount',
    rbac: [['vms/mount', 'post']],
    blocked: (vm) => {
      if (!vm.running) return NOT_RUNNING;
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
      if (!vm.running) return NOT_RUNNING;
      if (!(vm.networks ?? []).length) return 'the VM has no interfaces';
      if (!vmInterfaces(vm).some((i) => !i.disconnected)) {
        return 'all its interfaces are disconnected';
      }
      if (!captureTargets(vm).length) {
        return 'phēnix already captures every connected interface';
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

// the pending keys of a VLAN change on an interface and a snapshot restore,
// which spin beside their links rather than on an action button
export const vlanPendingKey = (index) => `vlan:${index}`;
export const restorePendingKey = (snapshot) => `restore:${snapshot}`;

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

// the actions that take another's place in the Actions column, by its name
export const ROW_SWAPS = { shutdown: 'start', captureAll: 'stopCaptures' };

// The action a row swaps with this one, so a pending action's spinner survives
// the swap its job causes, as Shut down turning into Start.
export function rowSwapOf(name) {
  return (
    ROW_SWAPS[name] ??
    Object.keys(ROW_SWAPS).find((swapped) => ROW_SWAPS[swapped] === name)
  );
}

// The VM table's Actions column for one VM: a stopped VM gets Start in place of
// Shut down, and a capturing VM a button that stops its captures.
export function rowActionNames(vm) {
  const swapped = {
    shutdown: !vm?.running,
    captureAll: (vm?.captures ?? []).length > 0,
  };
  return ROW_ACTIONS.map((name) => (swapped[name] ? ROW_SWAPS[name] : name));
}

// The buttons for one VM's actions; a Restart greyed out because the VM is not
// running points to the Start beside it.
export function vmButtonStates(names, vm, opts) {
  const notRunning = `Cannot restart: ${NOT_RUNNING}`;
  return vmActionStates(names, vm, opts).map((state) =>
    state.name === 'restart' && state.reason === notRunning
      ? { ...state, reason: `${notRunning}. Start it instead` }
      : state,
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
  let unavailable = null;
  if (vm.external) unavailable = 'external nodes are not VMs';
  else if (action.feature && !features.includes(action.feature)) {
    unavailable = 'the server does not support it';
  }

  return actionState(
    {
      name,
      label,
      verb: action.verb ? forVm(action.verb, vm) : label.toLowerCase(),
      description: forVm(action.description, vm),
      icon: action.icon,
      type: action.type,
      outlined: action.outlined,
    },
    {
      unavailable,
      permitted: unavailable ? true : actionPermitted(name, can),
      blocked: unavailable ? null : stateBlocks(action, vm),
    },
  );
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
  return partitionForAction(
    names,
    (vmName) => byName.get(vmName),
    (vm) => stateBlocks(action, vm),
    'the VM is not in the table',
  );
}

// One sentence for each reason partitionVMsForAction skipped VMs for. The
// reasons start "the VM is" or "the VM does", so each can be said of them all:
// "The VMs a and b are not running."
export function skippedVMsText(skipped) {
  return skippedText(skipped, 'VM');
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
