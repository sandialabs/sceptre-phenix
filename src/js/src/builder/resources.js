// The vCPUs and memory of the devices phenix starts, for the editor header:
// every device except an external one, also those of included topologies
// (preflightDevices in api/builder/preflight.go). An invalid value takes the
// phenix schema default (preflightDefaultVCPUs and preflightDefaultMemory).

import { count } from './announce.js';

/** The vCPUs of a device that sets none. */
export const DEFAULT_VCPUS = 1;

/** The memory of a device that sets none, in MB. */
export const DEFAULT_MEMORY_MB = 512;

// The largest number that the server reads from a JSON number (specWhole in
// api/builder/preflight.go).
const MAX_WHOLE = 2147483647;

// A positive whole number of a node spec as specWhole in preflight.go reads it:
// a JSON number, or text of one with a sign and white space. Else null.
export function specWhole(value) {
  if (typeof value === 'number') {
    return Number.isInteger(value) && value >= 1 && value <= MAX_WHOLE
      ? value
      : null;
  }

  if (typeof value !== 'string') {
    return null;
  }

  const text = value.trim();

  if (!/^[+-]?\d+$/.test(text)) {
    return null;
  }

  const number = Number(text);

  return Number.isSafeInteger(number) && number > 0 ? number : null;
}

/** Whether phenix starts the node, as a VM or a container. */
export function startsDevice(node) {
  return (
    node?.kind === 'device' &&
    Boolean(node.device) &&
    node.device.spec?.external !== true
  );
}

/** The vCPUs and memory (MB) of a device that phenix starts. */
export function deviceResources(node) {
  const hardware = node?.device?.spec?.hardware;

  return {
    vcpus: specWhole(hardware?.vcpus) ?? DEFAULT_VCPUS,
    memory: specWhole(hardware?.memory) ?? DEFAULT_MEMORY_MB,
  };
}

// The totals of the nodes that phenix starts, memory in MB. Each node counts
// once, also when it is given more than once.
export function resourceTotals(nodes = []) {
  const seen = new Set();
  const totals = { devices: 0, vcpus: 0, memory: 0 };

  for (const node of nodes) {
    if (!startsDevice(node) || seen.has(node)) {
      continue;
    }

    seen.add(node);

    const { vcpus, memory } = deviceResources(node);

    totals.devices += 1;
    totals.vcpus += vcpus;
    totals.memory += memory;
  }

  return totals;
}

/** The totals of every device of the diagram. */
export function diagramResources(doc) {
  return resourceTotals(doc?.nodes || []);
}

// The totals of the selected nodes: the selected devices, and the devices in
// a selected group at any depth.
export function selectionResources(doc, ids = []) {
  const nodes = doc?.nodes || [];
  const chosen = new Set(ids);

  if (!chosen.size) {
    return resourceTotals([]);
  }

  const parents = new Map(nodes.map((node) => [node.id, node.parentId]));

  // Whether the node or a group that holds it is selected. A loop of
  // parents, which the validator refuses, ends the walk.
  const inSelection = (node) => {
    const visited = new Set();

    for (let id = node.id; id && !visited.has(id); id = parents.get(id)) {
      if (chosen.has(id)) {
        return true;
      }

      visited.add(id);
    }

    return false;
  };

  return resourceTotals(nodes.filter(inSelection));
}

/** The disk images of a device's drives, trimmed, without empty names. */
export function driveImages(node) {
  const drives = node?.kind === 'device' && node.device?.spec?.hardware?.drives;

  return (Array.isArray(drives) ? drives : [])
    .map((drive) =>
      typeof drive?.image === 'string' ? drive.image.trim() : '',
    )
    .filter(Boolean);
}

const UNITS = ['MB', 'GB', 'TB'];

// Memory in MB as the header shows it: MB below 1 GB, else GB or TB (1024
// each) with at most one decimal ("512 MB", "1.5 GB").
export function formatMemory(megabytes) {
  let value = Math.max(0, Number(megabytes) || 0);
  let unit = 0;

  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }

  const rounded = unit === 0 ? Math.round(value) : Math.round(value * 10) / 10;

  return `${rounded.toLocaleString('en-US')} ${UNITS[unit]}`;
}

// Totals in words, in two parts: ["4 vCPUs", "2 GB RAM"]. A narrow header
// shows the parts on two lines.
export function resourcesParts({ vcpus, memory }) {
  return [count(vcpus, 'vCPU'), `${formatMemory(memory)} RAM`];
}

/** Totals in words, as the header shows them: "4 vCPUs, 2 GB RAM". */
export function resourcesText(totals) {
  return resourcesParts(totals).join(', ');
}
