// Document validation, mirroring phenix/types/builder validate.go.
//
// The rules here are the same rules the server enforces, reported as issues the
// inspector and outline can surface before a save is attempted. Issues use the
// server's `path` form (nodes[0].device.hostname) so a server rejection and a
// local rejection read identically. An issue about a node, a connection or a
// network also carries its id (nodeId, edgeId, networkId), which still finds
// it once an edit has moved the indexes in the path.
//
// testdata/validation-corpus.json in types/builder holds documents both
// sides must agree on. Two errors are the editor's own, stricter than the
// server: an interface connection point with no interface of that name in
// the node's spec, and a spec hostname that is not the device's. The server
// publishes such a device with the device's hostname and without that
// connection, which the editor would not show.

import { isIconKey } from './catalog.js';
import { contentDigestSync, isDigest } from './digest.js';
import {
  SCHEMA_REVISION,
  SCHEMA_URI,
  deviceHandles,
  edgeEndpoints,
  sizeOf,
  specInterfaces,
} from './model.js';

export const MAX_VLAN_ALIAS = 4094;

// The longest document name the draft service records as a title, in UTF-8
// bytes (MaxNameBytes in validate.go).
export const MAX_NAME_BYTES = 512;

// The apiVersion of scenario content a reference carries: the latest stored
// scenario version (ScenarioAPIVersion in document.go).
export const SCENARIO_API_VERSION = 'phenix.sandia.gov/v2';

// The characters a name must not contain: those validate.go refuses with
// strings.ContainsAny(name, " \t\n").
const WHITESPACE = /[ \t\n]/;

// A canonical RFC 4122 UUID with a known version and the RFC 4122 variant, in
// either case (IsUUID in types/builder/uuid.go).
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function fold(value) {
  return String(value ?? '')
    .trim()
    .toLowerCase();
}

function finite(value) {
  return typeof value === 'number' && Number.isFinite(value);
}

function issue(issues, path, message, level = 'error', extra = {}) {
  issues.push({ path, message, level, ...extra });
}

// Every identifier is a UUID: crypto.randomUUID mints the editor's, and the
// server derives name based ones (validateID in validate.go). A missing one
// is reported where it is required.
function validateUUID(issues, path, kind, id) {
  if (String(id || '').trim() && !UUID_PATTERN.test(String(id))) {
    issue(issues, path, `${kind} ID "${id}" is not a valid UUID`);
  }
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

  validateUUID(issues, 'id', 'document', doc.id);

  const name = String(doc.name ?? '');

  if (new TextEncoder().encode(name).length > MAX_NAME_BYTES) {
    issue(
      issues,
      'name',
      `document name must be at most ${MAX_NAME_BYTES} bytes`,
    );
  } else if (
    [...name].some((ch) => ch.charCodeAt(0) < 0x20 || ch.charCodeAt(0) === 0x7f)
  ) {
    issue(issues, 'name', 'document name must not contain control characters');
  }

  const viewport = doc.viewport || {};

  if (!finite(viewport.x) || !finite(viewport.y) || !finite(viewport.zoom)) {
    issue(issues, 'viewport', 'viewport values must be finite numbers');
  } else if (viewport.zoom <= 0) {
    issue(issues, 'viewport.zoom', 'zoom must be a positive number');
  }

  const grid = doc.grid || {};

  if (!finite(grid.size) || grid.size <= 0) {
    issue(issues, 'grid.size', 'grid size must be a positive finite number');
  }

  // Any string, or none: an id the editor does not know is ignored (see
  // documentLayout in layouts/index.js).
  if (
    doc.layout !== undefined &&
    doc.layout !== null &&
    typeof doc.layout !== 'string'
  ) {
    issue(issues, 'layout', 'layout must be a string');
  }
}

// The fewest points an edge route may hold: its two ends (minRoutePoints in
// validate.go).
const MIN_ROUTE_POINTS = 2;

// Absent, or at least its two ends, every point a finite coordinate
// (validateRoute in validate.go). Null is none, as Go decodes it.
function validateRoute(route, path, issues) {
  if (route === undefined || route === null) {
    return;
  }

  if (!Array.isArray(route) || route.length < MIN_ROUTE_POINTS) {
    issue(
      issues,
      path,
      `a route must have at least ${MIN_ROUTE_POINTS} points`,
    );

    return;
  }

  if (!route.every((point) => finite(point?.x) && finite(point?.y))) {
    issue(issues, path, 'route points must be finite numbers');
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

    validateUUID(issues, `${path}.id`, 'network', network.id);

    if (!String(network.name || '').trim()) {
      issue(issues, `${path}.name`, 'network name is required');
    } else if (WHITESPACE.test(network.name)) {
      issue(
        issues,
        `${path}.name`,
        `network name "${network.name}" must not contain whitespace`,
      );
    } else if (seenNames.has(network.name)) {
      // Exactly: minimega VLAN names are case sensitive, so networks
      // differing only by case are different VLANs.
      issue(
        issues,
        `${path}.name`,
        `conflicting network name "${network.name}" (also networks[${seenNames.get(network.name)}])`,
      );
    } else {
      seenNames.set(network.name, index);
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

    validateUUID(issues, `${handlePath}.id`, 'interface handle', handle.id);

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
        `interface "${handle.name}" has no matching entry in the node's Interfaces`,
      );
    }
  });
}

// A device included from another topology is left out of the published
// topology on the strength of the document's includeTopologies, so a
// document that includes nothing must not carry one.
function validateIncludedFrom(doc, name, path, issues) {
  if (name === undefined || name === '') {
    return;
  }

  if (typeof name !== 'string' || !name.trim() || WHITESPACE.test(name)) {
    issue(
      issues,
      `${path}.device.includedFrom`,
      `included topology name "${name}" must not be blank or contain whitespace`,
    );
  } else if (!(doc.source?.includeTopologies || []).length) {
    issue(
      issues,
      `${path}.device.includedFrom`,
      `device is included from topology "${name}", but the document includes no topologies`,
    );
  }
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

    validateUUID(issues, `${path}.id`, 'node', node.id);

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
        size.width <= 0 ||
        size.height <= 0
      ) {
        issue(
          issues,
          `${path}.size`,
          'size values must be positive finite numbers',
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
      } else if (WHITESPACE.test(hostname)) {
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
          'Node hostname must match the device Hostname',
        );
      }

      if (node.device.iconKey && !isIconKey(node.device.iconKey)) {
        issue(
          issues,
          `${path}.device.iconKey`,
          `unknown icon key "${node.device.iconKey}"`,
        );
      }

      validateIncludedFrom(doc, node.device.includedFrom, path, issues);
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

    validateUUID(issues, `${path}.id`, 'edge', edge.id);
    validateRoute(edge.route, `${path}.route`, issues);

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

  if (!String(ref.apiVersion || '').trim()) {
    issue(
      issues,
      'scenario.apiVersion',
      'scenario reference requires an apiVersion',
    );
  }

  if (validateScenarioDigest(ref, issues)) {
    validateScenarioContent(ref, issues);
  }
}

function hasScenarioContent(ref) {
  return Boolean(ref.content) && Object.keys(ref.content).length > 0;
}

// Every reference carries a well-formed digest, including a stored reference
// without content: GET /builder/sources lists stored scenarios by apiVersion
// and digest, never by content. The digest must match content only when the
// reference carries some. Returns whether the digest can be trusted.
function validateScenarioDigest(ref, issues) {
  if (!String(ref.digest || '').trim()) {
    issue(
      issues,
      'scenario.digest',
      'scenario reference requires a content digest',
    );

    return false;
  }

  if (!isDigest(ref.digest)) {
    issue(
      issues,
      'scenario.digest',
      `malformed scenario digest "${ref.digest}" (expected sha256:<64 hex>)`,
    );

    return false;
  }

  if (!hasScenarioContent(ref)) {
    return true;
  }

  const digest = contentDigestSync(ref.content);

  if (ref.digest !== digest) {
    issue(
      issues,
      'scenario.digest',
      `content digest mismatch (expected ${digest})`,
    );

    return false;
  }

  return true;
}

// The server also validates the content against the phenix scenario schema,
// which the editor does not carry; a schema failure is reported on save.
function validateScenarioContent(ref, issues) {
  if (hasScenarioContent(ref) && ref.apiVersion !== SCENARIO_API_VERSION) {
    issue(
      issues,
      'scenario.apiVersion',
      `unsupported scenario apiVersion "${ref.apiVersion ?? ''}" (expected "${SCENARIO_API_VERSION}")`,
    );
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

  const includes = doc.source.includeTopologies;

  // The server refuses any other value when it decodes the document.
  if (includes != null && !Array.isArray(includes)) {
    issue(
      issues,
      'source.includeTopologies',
      'included topologies must be a list of topology names',
    );
  }

  (Array.isArray(includes) ? includes : []).forEach((name, index) => {
    const path = `source.includeTopologies[${index}]`;

    if (typeof name !== 'string' || !name.trim()) {
      issue(issues, path, 'included topology name is required');
    } else if (WHITESPACE.test(name)) {
      issue(
        issues,
        path,
        `included topology name "${name}" must not contain whitespace`,
      );
    }
  });

  // null is no digest, as the server decodes it.
  const digest = doc.source.digest;

  if (digest != null && digest !== '' && !isDigest(digest)) {
    issue(
      issues,
      'source.digest',
      `malformed source digest "${digest}" (expected sha256:<64 hex>)`,
    );
  }
}

// What a device's spec is checked against: the folded names of the
// diagram's networks, and the disk images the server has (null while they
// are unknown, and drive images then go unchecked).
function specContext(doc, disks) {
  return {
    networks: new Set(
      (doc?.networks || [])
        .map((network) => fold(network?.name))
        .filter(Boolean),
    ),
    disks: Array.isArray(disks) ? new Set(disks) : null,
  };
}

function trimmed(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function arrayOf(value) {
  return Array.isArray(value) ? value : [];
}

/**
 * The fields of a device spec that name something outside the device and
 * name nothing there: an interface VLAN that is no network of the diagram,
 * and a drive image that is no disk image of the server. A VLAN naming a
 * network in another case is not reported here: typing one connects the
 * interface to that network (see connectByVLAN in model.js), and one an
 * unconnected interface already has is reported with its connection (see
 * interfaceWarnings). Images are matched by file name, as the server lists
 * them. An external node runs no image of its own.
 *
 * @param {object} spec
 * @param {string} hostname the device's, for the issue text
 * @param {object} context see specContext
 * @returns {{field: string, path: string, warning: string, issue: string}[]}
 *   field is the JSON Forms data path of the field (spec.hardware.drives.0.
 *   image) and path its path under the node (device.spec.hardware.drives[0].
 *   image); warning is worded for the field, and issue for the diagram
 */
function specFindings(spec, hostname, { networks, disks }) {
  const findings = [];
  const of = hostname ? ` of "${hostname}"` : '';

  arrayOf(spec?.network?.interfaces).forEach((iface, index) => {
    const vlan = trimmed(iface?.vlan);

    if (!vlan || networks.has(fold(vlan))) {
      return;
    }

    const name = trimmed(iface?.name)
      ? `interface "${iface.name}"`
      : `interface #${index + 1}`;

    findings.push({
      field: `spec.network.interfaces.${index}.vlan`,
      path: `device.spec.network.interfaces[${index}].vlan`,
      warning: `No network in this diagram is named "${vlan}".`,
      issue: `${name}${of} uses VLAN "${vlan}", which is not a network in this diagram`,
    });
  });

  if (!disks || spec?.external === true) {
    return findings;
  }

  arrayOf(spec?.hardware?.drives).forEach((drive, index) => {
    const image = trimmed(drive?.image);

    if (!image || disks.has(image.split('/').pop())) {
      return;
    }

    findings.push({
      field: `spec.hardware.drives.${index}.image`,
      path: `device.spec.hardware.drives[${index}].image`,
      warning: `The server has no disk image named "${image}".`,
      issue: `drive image "${image}"${of} is not among the server's disk images`,
    });
  });

  return findings;
}

/**
 * Warnings about the fields of a device spec, for the Inspector's working
 * copy: the checks validateDocument makes of a device's spec, made before
 * an edit is applied.
 *
 * @param {object} doc the diagram the device is in
 * @param {object} spec the device spec
 * @param {object} [options] disks: file names of the server's disk images,
 *   or null while they are unknown
 * @returns {Object<string, string[]>} messages, keyed by the JSON Forms data
 *   path of the field (spec.network.interfaces.0.vlan)
 */
export function deviceFieldWarnings(doc, spec, { disks = null } = {}) {
  const warnings = {};

  specFindings(spec, '', specContext(doc, disks)).forEach((finding) => {
    warnings[finding.field] = [
      ...(warnings[finding.field] || []),
      finding.warning,
    ];
  });

  return warnings;
}

// Whether a spec interface names a VLAN, as the server decides it (hasVLAN
// in types/builder/topology.go): a VLAN that is not a string is left to the
// schema.
function hasVLAN(iface) {
  const vlan = iface?.vlan;

  return vlan != null && (typeof vlan !== 'string' || vlan.trim() !== '');
}

/**
 * What publishing does with each interface of a device that is not
 * connected on the canvas. These are the device spec's interfaces, as the
 * server checks them, so they include those with no connection point: an
 * unnamed one, a second one of the same name (a connection point is the
 * first one's, see specInterfaceFor in model.js), and one an uploaded
 * document gave none. One is named by its position when its name does not
 * tell it apart.
 *
 * An interface's VLAN is published as it is, and phenix matches VLAN names
 * exactly, so only a network of that very name is the one it is put on.
 *
 * @param {object} doc
 * @param {object} node device node
 * @param {Set<string>} connected ids of the connected interface handles
 * @returns {{index: number, message: string, blocksPublish?: true}[]} index
 *   is the interface's in the spec
 */
function interfaceWarnings(doc, node, connected) {
  const interfaces = specInterfaces(node);
  const names = interfaces.map((iface) =>
    typeof iface?.name === 'string' ? iface.name : '',
  );
  const handles = new Map(
    deviceHandles(node).map((handle) => [handle.name, handle]),
  );
  const external = node.device.spec?.external != null;
  const networks = (doc.networks || []).filter((network) => network?.name);
  const warnings = [];

  interfaces.forEach((iface, index) => {
    // An entry of the wrong shape is left to the schema, as on the server.
    if (!iface || typeof iface !== 'object') {
      return;
    }

    const name = names[index];
    const handle =
      name && names.indexOf(name) === index ? handles.get(name) : undefined;

    if (handle && connected.has(handle.id)) {
      return;
    }

    const unnamed = !name.trim();
    const shared = !unnamed && names.indexOf(name) !== names.lastIndexOf(name);
    let label = `"${name}"`;

    if (unnamed) {
      label = `#${index + 1}`;
    } else if (shared) {
      label = `"${name}" (#${index + 1})`;
    }

    const subject = `interface ${label} of "${node.device.hostname}"`;
    const warn = (message, extra = {}) =>
      warnings.push({ index, message, ...extra });
    const blocks = { blocksPublish: true };

    if (!hasVLAN(iface)) {
      if (external) {
        warn(`${subject} is not connected to a network`);
      } else if (handle) {
        warn(
          `${subject} is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it`,
          blocks,
        );
      } else if (unnamed) {
        warn(
          `${subject} has no name and no VLAN, so it cannot be published: name it, then connect it or type a VLAN for it`,
          blocks,
        );
      } else if (shared) {
        warn(
          `${subject} has no VLAN, and another interface has its name, so it cannot be connected or published: rename it, then connect it or type a VLAN for it`,
          blocks,
        );
      } else {
        warn(
          `${subject} has no VLAN and no connection point on the canvas, so it cannot be published: type a VLAN for it`,
          blocks,
        );
      }

      return;
    }

    const vlan = String(iface.vlan).trim();
    const network = networks.find((entry) => entry.name === vlan);
    const cased = networks.find((entry) => fold(entry.name) === fold(vlan));

    if (network) {
      warn(
        `${subject} is not connected on the canvas, but its VLAN "${vlan}" puts it on network ${network.name} when published`,
      );
    } else if (cased) {
      warn(
        `${subject} is not connected on the canvas, and its VLAN "${vlan}" differs from network ${cased.name} only in case: phenix treats them as different VLANs, so publishing does not put it on network ${cased.name}`,
      );
    } else {
      warn(`${subject} is not connected to a network`);
    }
  });

  return warnings;
}

/**
 * Advisory checks that are not server errors but are worth surfacing while
 * editing. None blocks saving. One blocks publishing, and says so with
 * `blocksPublish`: an interface with no VLAN, which phenix stores but
 * minimega refuses when the experiment starts. The server refuses to
 * publish it too (PublishTopologyConfig in types/builder/topology.go),
 * checking every interface in the device spec, as interfaceWarnings does.
 * An external device is not started, so its interfaces need none.
 *
 * @param {object} doc
 * @param {object[]} issues
 * @param {object} context see specContext
 */
function collectWarnings(doc, issues, context) {
  const connected = new Set(
    (doc.edges || []).flatMap((edge) =>
      [edge.sourceHandleId, edge.targetHandleId].filter(Boolean),
    ),
  );

  (doc.nodes || []).forEach((node, index) => {
    // An included device is its own topology's to fix, not this diagram's.
    if (node.kind !== 'device' || node.device?.includedFrom) {
      return;
    }

    specFindings(node.device?.spec, node.device?.hostname, context).forEach(
      (finding) => {
        issue(
          issues,
          `nodes[${index}].${finding.path}`,
          finding.issue,
          'warning',
        );
      },
    );

    if (specInterfaces(node).length === 0) {
      issue(
        issues,
        `nodes[${index}]`,
        `device "${node.device?.hostname}" has no interfaces`,
        'warning',
      );

      return;
    }

    interfaceWarnings(doc, node, connected).forEach(
      ({ index: position, message, ...extra }) => {
        issue(
          issues,
          `nodes[${index}].device.spec.network.interfaces[${position}]`,
          message,
          'warning',
          extra,
        );
      },
    );
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

const ID_KEYS = { nodes: 'nodeId', edges: 'edgeId', networks: 'networkId' };

// Adds the id of the node, connection or network an issue's path starts at.
function locate(doc, entry) {
  const match = /^(nodes|edges|networks)\[(\d+)\]/.exec(entry.path);
  const id = match ? doc[match[1]]?.[Number(match[2])]?.id : '';

  return typeof id === 'string' && id.trim()
    ? { ...entry, [ID_KEYS[match[1]]]: id }
    : entry;
}

/**
 * Validates a document exactly as the server does, plus editing warnings.
 *
 * @param {object} doc
 * @param {object} [options] disks: file names of the server's disk images,
 *   to check drive images against, or null while they are unknown
 * @returns {{path: string, message: string, level: 'error'|'warning',
 *   nodeId?: string, edgeId?: string, networkId?: string}[]} issues sorted
 *   by path
 */
export function validateDocument(doc, { disks = null } = {}) {
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
  collectWarnings(doc, issues, specContext(doc, disks));

  return issues
    .map((entry) => locate(doc, entry))
    .sort((a, b) => a.path.localeCompare(b.path));
}

/**
 * @param {object} doc
 * @returns {boolean} true when the document has no validation errors
 */
export function isValidDocument(doc) {
  return !validateDocument(doc).some((entry) => entry.level === 'error');
}
