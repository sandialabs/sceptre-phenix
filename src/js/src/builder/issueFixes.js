// One-click fixes of the Diagram checks (see FIXES). A fix never changes a
// device from an included topology. For a hostname that an included device
// shares, Fix renames the diagram's own device.

import { freeAddressFor } from './addressing.js';
import { foldKey } from './goCase.js';
import {
  findNode,
  includedFrom,
  specInterfaces,
  uniqueHostname,
  updateNetwork,
  updateNode,
} from './model.js';
import { deepCopy } from './values.js';

const NODE_PATH = /^nodes\[(\d+)\]/;
const INTERFACE_INDEX = /interfaces(?:\[(\d+)\]|\.(\d+))/;

// The device an issue is about, and the interface its path or field names.
function issueSubject(doc, issue) {
  const byPath = NODE_PATH.exec(issue?.path || '');
  const id =
    issue?.nodeId || (byPath ? doc?.nodes?.[Number(byPath[1])]?.id : '');
  const node = id ? findNode(doc, id) : null;

  if (node?.kind !== 'device' || !node.device) {
    return null;
  }

  const found =
    INTERFACE_INDEX.exec(issue?.path || '') ||
    INTERFACE_INDEX.exec(issue?.field || '');
  const index = found ? Number(found[1] ?? found[2]) : -1;
  const iface = index >= 0 ? specInterfaces(node)[index] : undefined;

  return { node, index, iface };
}

// "interface eth0 of web-01"
function interfaceName(node, iface, index) {
  const name =
    typeof iface?.name === 'string' && iface.name.trim()
      ? iface.name
      : `${index + 1}`;

  return `interface ${name} of ${node.device.hostname}`;
}

// The first device of the diagram's own with the hostname of `node`,
// ignoring case as the server does (foldKey), other than `node`.
function ownNamesake(doc, node) {
  const name = foldKey(node.device.hostname);

  return (doc?.nodes || []).find(
    (entry) =>
      entry.id !== node.id &&
      entry.kind === 'device' &&
      !includedFrom(entry) &&
      foldKey(entry.device?.hostname) === name,
  );
}

// Each fix: whether it applies to the subject, and the edit with its name.
const FIXES = {
  'node.hostname.duplicate': (doc, { node: subject }) => {
    const node = includedFrom(subject) ? ownNamesake(doc, subject) : subject;

    if (!node) {
      return null;
    }

    const from = node.device.hostname;
    const to = uniqueHostname(doc, from);

    return to === from
      ? null
      : {
          name: `Rename ${from} to ${to}`,
          label: `Renamed ${from} to ${to}`,
          apply: (current) =>
            updateNode(current, node.id, { device: { hostname: to } }),
        };
  },

  'interface.ip.shared': (doc, { node, index, iface }) => {
    const free =
      iface && !includedFrom(node) ? freeAddressFor(doc, node.id, index) : null;

    if (!free?.address) {
      return null;
    }

    const where = interfaceName(node, iface, index);
    const network = free.network.name;
    const role = free.setGateway
      ? `, and make it the gateway of network ${network}`
      : free.holdsGateway
        ? `, the gateway of network ${network}`
        : '';
    const done = free.setGateway
      ? `, and made it the gateway of network ${network}`
      : role;

    return {
      name: `Change the IP address of ${where} to ${free.address}${role}`,
      label: `Changed the IP address of ${where} to ${free.address}${done}`,
      apply: (current) => {
        const next = withInterface(current, node, index, (copy) => {
          copy.address = free.address;
          copy.mask = free.mask;

          if (free.gateway) {
            copy.gateway = free.gateway;
          }
        });

        return free.setGateway
          ? updateNetwork(next, free.network.id, { gateway: free.address })
          : next;
      },
    };
  },

  'interface.mac.shared': (_, { node, index, iface }) => {
    if (!iface || includedFrom(node)) {
      return null;
    }

    const where = interfaceName(node, iface, index);

    return {
      name: `Clear the MAC address of ${where}`,
      label: `Cleared the MAC address of ${where}`,
      apply: (current) =>
        withInterface(current, node, index, (copy) => {
          delete copy.mac;
        }),
    };
  },
};

// The document with one spec interface of a device changed by `change`.
function withInterface(doc, node, index, change) {
  const spec = deepCopy(node.device.spec);

  change(spec.network.interfaces[index]);

  return updateNode(doc, node.id, { label: node.label, device: { spec } });
}

/**
 * The fix of an issue in the diagram as it is now: its button label, its Undo
 * step name and its edit. Null when the issue has no fix, or the fix cannot
 * apply, such as a shared IP on a network with no free address.
 */
export function issueFix(doc, issue) {
  const fix = Object.hasOwn(FIXES, issue?.code) ? FIXES[issue.code] : null;
  const subject = fix ? issueSubject(doc, issue) : null;

  return subject ? fix(doc, subject) : null;
}
