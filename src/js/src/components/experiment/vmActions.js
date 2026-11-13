import { isInterfaceCapturing } from '@/utils/captures.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import {
  actionState,
  partitionForAction,
  skippedText,
} from '@/utils/operations.js';
import { namedList } from '@/utils/plural.js';

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
  if (notLaunched(vm)) {
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
// why a do-not-boot VM that minimega has not launched cannot start
const NOT_LAUNCHED =
  'the VM is set to not boot, so minimega did not launch it. Redeploy the VM to launch it';
// a do-not-boot VM that minimega has not launched has no state
const notLaunched = (vm) => Boolean(vm.dnb) && !vm.state;

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
    blocked: (vm) => {
      if (notLaunched(vm)) return NOT_LAUNCHED;
      return vm.running ? 'the VM is already running' : null;
    },
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
    icon: 'clone',
    rbac: [['vms/snapshots', 'create']],
    blocked: (vm) => {
      if (!vm.snapshot) return 'the VM does not have snapshots enabled';
      if (!vm.running) return NOT_RUNNING;
      return null;
    },
  },
  screenshot: {
    label: 'Screenshot',
    verb: 'save a screenshot',
    description:
      "Save a full-size PNG of the VM's screen in the experiment files, in the screenshots folder",
    icon: 'camera',
    rbac: [['vms/screenshot', 'create']],
    blocked: (vm) => (vm.running ? null : NOT_RUNNING),
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
  // the console page's capture button while the VM captures nothing
  captureIface: {
    label: 'Capture interface 0',
    description: 'Start a packet capture on interface 0 of the VM',
    icon: CAPTURE_ICON,
    rbac: [['vms/captures', 'create']],
    blocked: (vm) => {
      if (!vm.running) return NOT_RUNNING;
      const iface = vmInterfaces(vm)[0];
      if (!iface) return 'the VM has no interface 0';
      if (iface.disconnected) return 'interface 0 of the VM is disconnected';
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
  labels: {
    label: 'Edit labels',
    description: 'Edit the labels and notes of the VM',
    icon: 'tag',
    rbac: [['vms', 'patch']],
    blocked: () => null,
  },
  annotations: {
    label: 'Edit annotations',
    description:
      "Edit the annotations of the VM in this experiment's copy of the topology",
    icon: 'edit',
    rbac: [['vms', 'patch']],
    blocked: () => null,
  },
};

// Why the role may not take an action: the first permission it does not have.
export const notPermittedReason = (resource, verb) =>
  `Your role does not permit this action (${resource} ${verb})`;

// The confirm dialog of an action: the title names the VM, or the count of
// VMs; `does` says what the action does. `many` replaces the words for VMs.
const askFor =
  ({ title, verb, does, confirmText, type = 'is-success', many = {} }) =>
  (vms) => {
    const one = vms.length === 1;
    const words = one ? { title, verb } : { title, verb, ...many };
    return {
      title: one
        ? `${words.title} the ${vms[0]} VM`
        : `${words.title} ${vms.length} VMs`,
      message: escapeHTML(
        `This will ${words.verb} ${namedList(vms, 'VM')}. ${does}`,
      ),
      confirmText,
      type,
    };
  };

// The confirm dialog (see confirmOperation) of each VM action that asks
// first, for the VM names given. The message is HTML. Start asks only for a
// selection; memory snapshot asks in its file name dialog.
export const VM_ACTION_CONFIRMS = {
  start: askFor({
    title: 'Start',
    verb: 'start',
    does: 'Start boots a VM that is shut down and resumes a VM that is paused.',
    confirmText: 'Start',
  }),
  pause: askFor({
    title: 'Pause',
    verb: 'pause',
    does: 'Pause stops a VM and keeps its memory until you resume it.',
    confirmText: 'Pause',
  }),
  restart: askFor({
    title: 'Restart',
    verb: 'restart',
    does: 'Restart resets a VM at once without a shutdown; unsaved data is lost.',
    confirmText: 'Restart',
  }),
  shutdown: askFor({
    title: 'Shut Down',
    verb: 'shut down',
    does: 'Shut down tells the operating system of a VM to power off; you can start the VM again.',
    confirmText: 'Shut Down',
    type: 'is-danger',
  }),
  kill: askFor({
    title: 'Kill',
    verb: 'kill',
    does:
      'Kill stops a VM at once without a shutdown and removes it until the ' +
      'experiment starts again; unsaved data is lost.',
    confirmText: 'Kill',
    type: 'is-danger',
  }),
  resetDisk: askFor({
    title: 'Reset the Disk of',
    verb: 'reset the disk of',
    does: 'Reset disk sets the disk of a VM back to its state at the start of the experiment; changes are lost.',
    confirmText: 'Reset',
    many: { title: 'Reset the Disks of', verb: 'reset the disks of' },
  }),
  snapshot: askFor({
    title: 'Create a Snapshot of',
    verb: 'create a snapshot of',
    does: 'A snapshot saves the disk and memory of a VM, which you can restore later.',
    confirmText: 'Create',
    many: { title: 'Create Snapshots of', verb: 'create snapshots of' },
  }),
};

/** What the memory snapshot dialog says that the action does. */
export const MEMORY_SNAPSHOT_DOES =
  'A memory snapshot saves the memory of a VM to an ELF file in the experiment files.';

// the pending keys of a VLAN change on an interface and a snapshot restore
// or delete, which spin beside their links rather than on an action button
export const vlanPendingKey = (index) => `vlan:${index}`;
export const restorePendingKey = (snapshot) => `restore:${snapshot}`;
export const deleteSnapshotPendingKey = (snapshot) =>
  `deleteSnapshot:${snapshot}`;

// power operations, shown first in the details modal
export const POWER_ACTIONS = ['start', 'pause', 'restart', 'shutdown', 'kill'];
// the rest of the details modal's actions
export const TOOL_ACTIONS = [
  'redeploy',
  'resetDisk',
  'snapshot',
  'screenshot',
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
//   denied: the role may not take it; `reason` names the permission
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

  const missing = unavailable
    ? null
    : action.rbac.find(([resource, verb]) => !can(resource, verb));
  const state = actionState(
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
      permitted: !missing,
      blocked: unavailable ? null : stateBlocks(action, vm),
    },
  );
  return missing
    ? { ...state, denied: true, reason: notPermittedReason(...missing) }
    : state;
}

// Whether a page shows an action's button: one the role may not take shows
// greyed out with the permission it needs; one the server lacks does not.
export const shownAction = (state) => state.permitted || !!state.denied;

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

// minimega's "alias (vlan)" name of the network an interface is on
const NETWORK_NAME = /^(.*?)\s*\((\d+)\)$/;

// The VLAN alias of a network name as minimega writes it, or null for a
// disconnected interface or a name without a VLAN.
export function vlanAlias(network) {
  const match = NETWORK_NAME.exec(network ?? '');
  return match ? match[1] : null;
}

// Splits minimega's "alias (vlan)" network names, pairing each interface with
// its IP, tap, and whether a packet capture is running on it.
export function vmInterfaces(vm) {
  const networks = vm?.networks ?? [];
  return networks.map((network, index) => {
    const match = NETWORK_NAME.exec(network ?? '');
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
