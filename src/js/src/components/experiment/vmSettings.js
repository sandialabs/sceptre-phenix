// The settings of a stopped experiment's VMs that its page edits: the choices
// each control offers, how a value reads, how it differs from the value the
// topology gave the VM, and the port forwards the VM gets at start.

import { diskLabel, findDisk } from '@/utils/diskChain.js';

// the settings a PATCH of a VM changes, in the order the page shows them
export const SETTING_FIELDS = Object.freeze([
  'host',
  'cpus',
  'ram',
  'disk',
  'inject_partition',
  'snapshot',
  'dnb',
]);

export const CPU_CHOICES = Object.freeze([1, 2, 3, 4]);
export const RAM_CHOICES = Object.freeze([
  512, 1024, 2048, 3072, 4096, 8192, 12288, 16384,
]);
export const PARTITION_CHOICES = Object.freeze([1, 2, 3, 4]);

// the built-in schedulers, for when the server does not list them
export const BUILTIN_SCHEDULERS = Object.freeze([
  { name: 'isolate-experiment', builtin: true },
  { name: 'round-robin', builtin: true },
  { name: 'subnet-compute', builtin: true },
]);

// The choices of a numeric control, with the current value added in order
// when the list does not hold it, so the control can show it.
export function choicesWith(choices, current) {
  const value = Number(current);
  if (!current || !Number.isFinite(value) || choices.includes(value)) {
    return [...choices];
  }
  return [...choices, value].sort((a, b) => a - b);
}

// A field's value in one form for comparing, whatever type it came in.
function normal(field, value) {
  if (field === 'snapshot' || field === 'dnb') return Boolean(value);
  if (field === 'host' || field === 'disk') return value ?? '';
  return value == null || value === '' ? '' : Number(value);
}

// whether the VM tells the values its topology gave it
export const hasDefaults = (vm) =>
  vm?.defaults != null && typeof vm.defaults === 'object';

// The fields whose value differs from the topology's, each with the
// topology's value; empty when the VM has no defaults.
export function changedSettings(vm) {
  if (!hasDefaults(vm)) return {};
  const changed = {};
  for (const field of SETTING_FIELDS) {
    if (!(field in vm.defaults)) continue;
    if (normal(field, vm[field]) !== normal(field, vm.defaults[field])) {
      changed[field] = vm.defaults[field];
    }
  }
  return changed;
}

// whether every setting of the VM has the topology's value
export const atDefaults = (vm) => Object.keys(changedSettings(vm)).length === 0;

// memory in MiB as its choices name it: "512 MiB", "2 GiB", "1.5 GiB"
const ramText = (mib) =>
  mib >= 1024 ? `${Math.round((mib / 1024) * 100) / 100} GiB` : `${mib} MiB`;

// How a setting's value reads in a sentence: "2 GiB", "no host".
export function settingText(field, value, disks) {
  switch (field) {
    case 'host':
      return value || 'no host';
    case 'ram':
      return ramText(Number(value));
    case 'disk':
      return diskLabel(findDisk(disks, value)) || value || 'no disk';
    case 'snapshot':
      return value ? 'non-persistent' : 'persistent';
    case 'dnb':
      return value ? 'do not boot' : 'boot';
    default:
      return String(value ?? '');
  }
}

// the tooltip of the mark on a setting the user changed
export const changedTip = (field, value, disks) =>
  `Changed from the topology value: ${settingText(field, value, disks)}`;

// The annotation that has the startup app open port forwards to the VM.
export const AUTOTUNNEL = 'phenix/startup-autotunnel';

// The port forwards the startup app opens to the VM when the experiment
// starts, from its annotations: each "port", "port:dport" or
// "port:host:dport" entry as { srcPort, dstHost, dstPort }, or { invalid }
// with the entry as text.
export function startupForwards(annotations) {
  const value = annotations?.[AUTOTUNNEL];
  if (value == null) return [];
  const entries = Array.isArray(value) ? value : [value];
  return entries.map((entry) => {
    const parts = typeof entry === 'string' ? entry.split(':') : [];
    switch (parts.length) {
      case 1:
        if (parts[0]) {
          return { srcPort: parts[0], dstHost: '127.0.0.1', dstPort: parts[0] };
        }
        break;
      case 2:
        return { srcPort: parts[0], dstHost: '127.0.0.1', dstPort: parts[1] };
      case 3:
        return { srcPort: parts[0], dstHost: parts[1], dstPort: parts[2] };
    }
    return {
      invalid: typeof entry === 'string' ? entry : JSON.stringify(entry),
    };
  });
}

// The outcome items of a PATCH or reset of several VMs: those the server
// returned succeeded, and its errors failed (retryable) or were skipped.
export function batchOutcome(data) {
  return [
    ...(data?.vms ?? []).map(({ name }) => ({
      name,
      status: 'succeeded',
      error: null,
      retryable: false,
    })),
    ...(data?.errors ?? []).map(({ name, status, error }) => ({
      name,
      status: status === 'skipped' ? 'skipped' : 'failed',
      error: error ?? null,
      retryable: status !== 'skipped',
    })),
  ];
}

// The named VMs a reset can change, to send, and outcome items for the others:
// a VM on the page (in vms) is skipped when it does not know its topology
// values or already has them. A VM not on the page is sent, for the server.
export function resetPlan(names, vms) {
  const rows = new Map((vms ?? []).map((vm) => [vm.name, vm]));
  const send = [];
  const skipped = [];
  for (const name of names) {
    const vm = rows.get(name);
    let error = null;
    if (vm && !hasDefaults(vm)) error = 'its topology values are not known';
    else if (vm && atDefaults(vm)) error = 'it has the topology values';
    if (error) {
      skipped.push({ name, status: 'skipped', error, retryable: false });
    } else {
      send.push(name);
    }
  }
  return { send, skipped };
}

// Orders the VMs a stopped experiment's page reads against the settings it
// saves, so an older answer cannot put back an old value. Each read is
// numbered as it is sent (sent), and each save as its answer arrives (saved).
// A read sent before a save's answer arrived keeps the saved value of that
// field. So does a websocket update whose seq is not higher than the seq of
// the save's answer.
export function createSaveClock() {
  let clock = 0;
  // by VM name, each saved field's { tick, seq }
  const saves = new Map();
  return {
    sent: () => ++clock,

    saved(name, fields, seq) {
      const tick = ++clock;
      const byField = saves.get(name) ?? new Map();
      for (const field of fields) byField.set(field, { tick, seq });
      saves.set(name, byField);
    },

    // The VM as read, with the current row's value of each field in keep
    // (saves in flight) and of each field saved after the read: one sent at
    // tick sentAt, or a websocket update with this server seq.
    merge(read, current, { sentAt, seq, keep = [] } = {}) {
      if (!current) return read;
      const kept = {};
      const hold = (field) => {
        if (field in current) kept[field] = current[field];
      };
      for (const field of keep) hold(field);
      const byField = saves.get(read.name);
      for (const [field, save] of byField ?? []) {
        if (sentAt !== undefined) {
          if (save.tick > sentAt) hold(field);
          // the read was sent after the answer, so it holds the saved value
          else byField.delete(field);
        } else if (seq != null && save.seq != null && seq <= save.seq) {
          hold(field);
        }
      }
      return Object.keys(kept).length ? { ...read, ...kept } : read;
    },
  };
}

// The VMs of a schedule ([{ vm, host }]) by name, those with a host only.
export function scheduleMap(schedule) {
  const hosts = {};
  for (const { vm, host } of schedule ?? []) {
    if (vm && host) hosts[vm] = host;
  }
  return hosts;
}

// the hosts a schedule map puts VMs on, sorted, each once
export const scheduledHosts = (hosts) =>
  [...new Set(Object.values(hosts ?? {}))].sort();

// the error of a VLAN ID typed in, or null for a valid one
export function vlanIdError(text) {
  const value = String(text ?? '').trim();
  if (!/^\d+$/.test(value) || Number(value) > 4094) {
    return 'Enter a whole number from 0 to 4094';
  }
  return null;
}
