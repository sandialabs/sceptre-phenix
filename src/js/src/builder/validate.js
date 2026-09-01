// Document validation, mirroring phenix/types/builder validate.go.
//
// The rules here are the same rules the server enforces, reported as issues the
// inspector and outline can surface before a save is attempted. Issues use the
// server's `path` form (nodes[0].device.hostname) so a server rejection and a
// local rejection read identically.

import { isIconKey } from './catalog.js';
import {
  SCHEMA_REVISION,
  SCHEMA_URI,
  deviceHandles,
  edgeEndpoints,
  sizeOf,
} from './model.js';

export const MAX_VLAN_ALIAS = 4094;

function fold(value) {
  return String(value ?? '')
    .trim()
    .toLowerCase();
}

function finite(value) {
  return typeof value === 'number' && Number.isFinite(value);
}

function issue(issues, path, message, level = 'error') {
  issues.push({ path, message, level });
}

function validateHeader(doc, issues) {
  if (doc.$schema !== SCHEMA_URI) {
    issue(
      issues,
      '$schema',
      `expected "${SCHEMA_URI}", got "${doc.$schema ?? ''}"`,
    );
  }

  if (doc.revision !== SCHEMA_REVISION) {
    issue(
      issues,
      'revision',
      `expected ${SCHEMA_REVISION}, got ${doc.revision ?? ''}`,
    );
  }

  if (!String(doc.id || '').trim()) {
    issue(issues, 'id', 'document ID is required');
  }

  const viewport = doc.viewport || {};

  if (!finite(viewport.x) || !finite(viewport.y) || !finite(viewport.zoom)) {
    issue(issues, 'viewport', 'viewport values must be finite numbers');
  } else if (viewport.zoom < 0) {
    issue(issues, 'viewport.zoom', 'zoom must not be negative');
  }

  const grid = doc.grid || {};

  if (!finite(grid.size) || grid.size < 0) {
    issue(
      issues,
      'grid.size',
      'grid size must be a non-negative finite number',
    );
  }
}

function validateNetworks(doc, issues, networksById) {
  const seenIds = new Map();
  const seenNames = new Map();
  const seenAliases = new Map();

  (doc.networks || []).forEach((network, index) => {
    const path = `networks[${index}]`;

    if (!String(network.id || '').trim()) {
      issue(issues, `${path}.id`, 'network ID is required');
    } else if (seenIds.has(fold(network.id))) {
      issue(
        issues,
        `${path}.id`,
        `duplicate network ID "${network.id}" (also networks[${seenIds.get(fold(network.id))}])`,
      );
    } else {
      seenIds.set(fold(network.id), index);
      networksById.set(network.id, network);
    }

    if (!String(network.name || '').trim()) {
      issue(issues, `${path}.name`, 'network name is required');
    } else if (/\s/.test(network.name)) {
      issue(
        issues,
        `${path}.name`,
        `network name "${network.name}" must not contain whitespace`,
      );
    } else if (seenNames.has(fold(network.name))) {
      issue(
        issues,
        `${path}.name`,
        `conflicting network name "${network.name}" (also networks[${seenNames.get(fold(network.name))}])`,
      );
    } else {
      seenNames.set(fold(network.name), index);
    }

    if (network.alias === undefined || network.alias === null) {
      return;
    }

    if (
      !Number.isInteger(network.alias) ||
      network.alias < 1 ||
      network.alias > MAX_VLAN_ALIAS
    ) {
      issue(
        issues,
        `${path}.alias`,
        `VLAN alias ${network.alias} is out of range (1-${MAX_VLAN_ALIAS})`,
      );

      return;
    }

    if (seenAliases.has(network.alias)) {
      issue(
        issues,
        `${path}.alias`,
        `conflicting VLAN alias ${network.alias} (also networks[${seenAliases.get(network.alias)}])`,
      );
    } else {
      seenAliases.set(network.alias, index);
    }
  });
}

const PAYLOAD_KEYS = ['device', 'switch', 'note', 'group'];

function validateNodePayload(node, path, issues) {
  if (!PAYLOAD_KEYS.includes(node.kind)) {
    issue(issues, `${path}.kind`, `unknown node kind "${node.kind ?? ''}"`);

    return false;
  }

  if (!node[node.kind]) {
    issue(
      issues,
      path,
      `node of kind "${node.kind}" is missing its "${node.kind}" payload`,
    );
  }

  PAYLOAD_KEYS.filter((key) => key !== node.kind).forEach((key) => {
    if (node[key]) {
      issue(
        issues,
        path,
        `node of kind "${node.kind}" must not carry a "${key}" payload`,
      );
    }
  });

  return true;
}

function validateDeviceHandles(node, path, issues, handleOwner) {
  const seenNames = new Map();
  const specNames = new Set(
    (node.device?.spec?.network?.interfaces || []).map((iface) => iface.name),
  );

  deviceHandles(node).forEach((handle, index) => {
    const handlePath = `${path}.device.interfaces[${index}]`;

    if (!String(handle.id || '').trim()) {
      issue(issues, `${handlePath}.id`, 'interface handle ID is required');
    } else if (handleOwner.has(handle.id)) {
      issue(
        issues,
        `${handlePath}.id`,
        `duplicate interface handle ID "${handle.id}" (also used by node "${handleOwner.get(handle.id).id}")`,
      );
    } else {
      handleOwner.set(handle.id, node);
    }

    if (!String(handle.name || '').trim()) {
      issue(issues, `${handlePath}.name`, 'interface name is required');

      return;
    }

    if (seenNames.has(fold(handle.name))) {
      issue(
        issues,
        `${handlePath}.name`,
        `duplicate interface name "${handle.name}" (also interfaces[${seenNames.get(fold(handle.name))}])`,
      );
    } else {
      seenNames.set(fold(handle.name), index);
    }

    if (!specNames.has(handle.name)) {
      issue(
        issues,
        `${handlePath}.name`,
        `interface "${handle.name}" has no matching entry in the device spec`,
      );
    }
  });
}

function validateNodes(doc, issues, nodesById, networksById, handleOwner) {
  const seenIds = new Map();
  const seenHostnames = new Map();

  (doc.nodes || []).forEach((node, index) => {
    const path = `nodes[${index}]`;

    if (!String(node.id || '').trim()) {
      issue(issues, `${path}.id`, 'node ID is required');
    } else if (seenIds.has(fold(node.id))) {
      issue(
        issues,
        `${path}.id`,
        `duplicate node ID "${node.id}" (also nodes[${seenIds.get(fold(node.id))}])`,
      );
    } else {
      seenIds.set(fold(node.id), index);
      nodesById.set(node.id, node);
    }

    if (!finite(node.position?.x) || !finite(node.position?.y)) {
      issue(
        issues,
        `${path}.position`,
        'position values must be finite numbers',
      );
    }

    if (node.size) {
      const size = sizeOf(node);

      if (
        !finite(size.width) ||
        !finite(size.height) ||
        size.width < 0 ||
        size.height < 0
      ) {
        issue(
          issues,
          `${path}.size`,
          'size values must be non-negative finite numbers',
        );
      }
    }

    if (!validateNodePayload(node, path, issues)) {
      return;
    }

    if (node.kind === 'device' && node.device) {
      const hostname = node.device.hostname;

      if (!String(hostname || '').trim()) {
        issue(issues, `${path}.device.hostname`, 'hostname is required');
      } else if (/\s/.test(hostname)) {
        issue(
          issues,
          `${path}.device.hostname`,
          `hostname "${hostname}" must not contain whitespace`,
        );
      } else if (seenHostnames.has(fold(hostname))) {
        issue(
          issues,
          `${path}.device.hostname`,
          `duplicate hostname "${hostname}" (also nodes[${seenHostnames.get(fold(hostname))}])`,
        );
      } else {
        seenHostnames.set(fold(hostname), index);
      }

      if (!node.device.spec) {
        issue(issues, `${path}.device.spec`, 'device spec is required');
      } else if (node.device.spec.general?.hostname !== hostname) {
        issue(
          issues,
          `${path}.device.spec.general.hostname`,
          'spec hostname must match the device hostname',
        );
      }

      if (node.device.iconKey && !isIconKey(node.device.iconKey)) {
        issue(
          issues,
          `${path}.device.iconKey`,
          `unknown icon key "${node.device.iconKey}"`,
        );
      }

      validateDeviceHandles(node, path, issues, handleOwner);
    }

    if (node.kind === 'switch' && node.switch) {
      if (!node.switch.networkId) {
        issue(
          issues,
          `${path}.switch.networkId`,
          'switch must reference a network',
        );
      } else if (!networksById.has(node.switch.networkId)) {
        issue(
          issues,
          `${path}.switch.networkId`,
          `unknown network "${node.switch.networkId}"`,
        );
      }
    }
  });
}

function validateParents(doc, issues, nodesById) {
  (doc.nodes || []).forEach((node, index) => {
    const path = `nodes[${index}].parentId`;

    if (!node.parentId) {
      return;
    }

    if (node.parentId === node.id) {
      issue(issues, path, 'node cannot be its own parent');

      return;
    }

    const parent = nodesById.get(node.parentId);

    if (!parent) {
      issue(issues, path, `unknown parent node "${node.parentId}"`);

      return;
    }

    if (parent.kind !== 'group') {
      issue(issues, path, `parent node "${node.parentId}" is not a group`);

      return;
    }

    const seen = new Set([node.id]);
    let current = node;

    while (current?.parentId) {
      if (seen.has(current.parentId)) {
        issue(
          issues,
          path,
          `group membership cycle detected at node "${node.id}"`,
        );

        return;
      }

      seen.add(current.parentId);
      current = nodesById.get(current.parentId);
    }
  });
}

function validateEdges(doc, issues, nodesById, networksById) {
  const seenIds = new Map();
  const connected = new Map();

  (doc.edges || []).forEach((edge, index) => {
    const path = `edges[${index}]`;

    if (!String(edge.id || '').trim()) {
      issue(issues, `${path}.id`, 'edge ID is required');
    } else if (seenIds.has(fold(edge.id))) {
      issue(
        issues,
        `${path}.id`,
        `duplicate edge ID "${edge.id}" (also edges[${seenIds.get(fold(edge.id))}])`,
      );
    } else {
      seenIds.set(fold(edge.id), index);
    }

    if (!nodesById.has(edge.sourceNodeId)) {
      issue(
        issues,
        `${path}.sourceNodeId`,
        `unknown node "${edge.sourceNodeId}"`,
      );
    }

    if (!nodesById.has(edge.targetNodeId)) {
      issue(
        issues,
        `${path}.targetNodeId`,
        `unknown node "${edge.targetNodeId}"`,
      );
    }

    if (
      !nodesById.has(edge.sourceNodeId) ||
      !nodesById.has(edge.targetNodeId)
    ) {
      return;
    }

    if (edge.sourceNodeId === edge.targetNodeId) {
      issue(issues, path, 'edge endpoints must differ');

      return;
    }

    const endpoints = edgeEndpoints(doc, edge);

    if (!endpoints || !endpoints.handleId) {
      issue(
        issues,
        path,
        'an edge must connect one device interface to one switch',
      );

      return;
    }

    const handle = deviceHandles(endpoints.device).find(
      (entry) => entry.id === endpoints.handleId,
    );

    if (!handle) {
      issue(
        issues,
        path,
        `unknown interface handle "${endpoints.handleId}" on device node "${endpoints.device.id}"`,
      );

      return;
    }

    if (connected.has(endpoints.handleId)) {
      issue(
        issues,
        path,
        `interface "${handle.name}" of device "${endpoints.device.device.hostname}" is already connected by edges[${connected.get(endpoints.handleId)}]`,
      );
    } else {
      connected.set(endpoints.handleId, index);
    }

    if (!edge.networkId) {
      issue(issues, `${path}.networkId`, 'edge must reference a network');

      return;
    }

    if (!networksById.has(edge.networkId)) {
      issue(issues, `${path}.networkId`, `unknown network "${edge.networkId}"`);

      return;
    }

    const switchNetwork = endpoints.switchNode.switch?.networkId;

    if (edge.networkId !== switchNetwork) {
      issue(
        issues,
        `${path}.networkId`,
        `network "${edge.networkId}" does not match network "${switchNetwork}" of switch "${endpoints.switchNode.id}"`,
      );
    }
  });
}

function validateScenario(doc, issues) {
  const ref = doc.scenario;

  if (!ref) {
    return;
  }

  if (ref.kind === 'stored') {
    if (!String(ref.name || '').trim()) {
      issue(
        issues,
        'scenario.name',
        'stored scenario reference requires a name',
      );
    }
  } else if (ref.kind === 'uploaded') {
    if (!ref.content || Object.keys(ref.content).length === 0) {
      issue(
        issues,
        'scenario.content',
        'uploaded scenario reference requires content',
      );
    }
  } else {
    issue(
      issues,
      'scenario.kind',
      `unknown scenario reference kind "${ref.kind ?? ''}"`,
    );

    return;
  }

  const hasContent = ref.content && Object.keys(ref.content).length > 0;

  if (!hasContent && ref.digest) {
    issue(issues, 'scenario.digest', 'digest present without content');
  }

  if (hasContent && !ref.digest) {
    issue(issues, 'scenario.digest', 'content digest is required');
  }
}

function validateSource(doc, issues) {
  if (!doc.source) {
    return;
  }

  if (!['manual', 'topology', 'experiment'].includes(doc.source.kind)) {
    issue(
      issues,
      'source.kind',
      `unknown source kind "${doc.source.kind ?? ''}"`,
    );
  }
}

/**
 * Advisory checks that are not server errors but are worth surfacing while
 * editing (they would block publishing).
 *
 * @param {object} doc
 * @param {object[]} issues
 */
function collectWarnings(doc, issues) {
  (doc.nodes || []).forEach((node, index) => {
    if (node.kind !== 'device') {
      return;
    }

    const handles = deviceHandles(node);

    if (handles.length === 0) {
      issue(
        issues,
        `nodes[${index}]`,
        `device "${node.device?.hostname}" has no interfaces`,
        'warning',
      );

      return;
    }

    const connected = new Set(
      (doc.edges || []).flatMap((edge) =>
        [edge.sourceHandleId, edge.targetHandleId].filter(Boolean),
      ),
    );

    handles
      .filter((handle) => !connected.has(handle.id))
      .forEach((handle) => {
        issue(
          issues,
          `nodes[${index}].device.interfaces`,
          `interface "${handle.name}" of "${node.device.hostname}" is not connected to a network`,
          'warning',
        );
      });
  });

  (doc.networks || []).forEach((network, index) => {
    const hasSwitch = (doc.nodes || []).some(
      (node) => node.kind === 'switch' && node.switch?.networkId === network.id,
    );

    if (!hasSwitch) {
      issue(
        issues,
        `networks[${index}]`,
        `network "${network.name}" has no switch on the canvas`,
        'warning',
      );
    }
  });
}

/**
 * Validates a document exactly as the server does, plus editing warnings.
 *
 * @param {object} doc
 * @returns {{path: string, message: string, level: 'error'|'warning'}[]} issues
 *   sorted by path
 */
export function validateDocument(doc) {
  const issues = [];

  if (!doc || typeof doc !== 'object') {
    return [{ path: '', message: 'document is required', level: 'error' }];
  }

  const nodesById = new Map();
  const networksById = new Map();
  const handleOwner = new Map();

  validateHeader(doc, issues);
  validateNetworks(doc, issues, networksById);
  validateNodes(doc, issues, nodesById, networksById, handleOwner);
  validateParents(doc, issues, nodesById);
  validateEdges(doc, issues, nodesById, networksById);
  validateScenario(doc, issues);
  validateSource(doc, issues);
  collectWarnings(doc, issues);

  return issues.sort((a, b) => a.path.localeCompare(b.path));
}

/**
 * @param {object} doc
 * @returns {boolean} true when the document has no validation errors
 */
export function isValidDocument(doc) {
  return !validateDocument(doc).some((entry) => entry.level === 'error');
}
