// Semantic outline: the accessible, non-drag mirror of the canvas.
//
// The outline is derived from the same document as the canvas, so anything a
// mouse user can do by dragging can also be done from the tree with the
// keyboard. Labels here are the accessible names announced by screen readers,
// which is why they spell out network membership rather than relying on the
// edge color used on the canvas.

import {
  deviceHandles,
  documentSummary,
  findNetwork,
  findNode,
  nodeComment,
  nodeLabel,
} from './model.js';

/**
 * Builds the outline tree: groups contain their members, ungrouped nodes are
 * top level, and each node lists its connections.
 *
 * @param {object} doc
 * @returns {object[]} outline items
 */
export function buildOutline(doc) {
  const nodes = (doc?.nodes || []).slice().sort(compareNodes);
  const byParent = new Map();

  nodes.forEach((node) => {
    const key = node.parentId || '';
    if (!byParent.has(key)) {
      byParent.set(key, []);
    }
    byParent.get(key).push(node);
  });

  const build = (parentId, depth, seen) =>
    (byParent.get(parentId) || [])
      .filter((node) => !seen.has(node.id))
      .map((node) => {
        seen.add(node.id);

        return {
          id: node.id,
          kind: node.kind,
          label: nodeLabel(node),
          depth,
          description: nodeComment(node),
          networkId:
            node.kind === 'switch' ? node.switch?.networkId : undefined,
          interfaces:
            node.kind === 'device'
              ? deviceHandles(node).map((handle) => ({
                  id: handle.id,
                  name: handle.name,
                }))
              : [],
          accessibleName: outlineLabel(doc, node),
          connections: connectionsFor(doc, node.id),
          children:
            node.kind === 'group' ? build(node.id, depth + 1, seen) : [],
        };
      });

  return build('', 0, new Set());
}

function compareNodes(a, b) {
  if (a.kind !== b.kind) {
    return kindRank(a.kind) - kindRank(b.kind);
  }

  return nodeLabel(a).localeCompare(nodeLabel(b), undefined, { numeric: true });
}

function kindRank(kind) {
  return { group: 0, switch: 1, device: 2, note: 3 }[kind] ?? 4;
}

/**
 * Network name for an edge, used in accessible descriptions.
 *
 * @param {object} doc
 * @param {object} edge
 * @returns {string}
 */
export function edgeNetworkName(doc, edge) {
  return findNetwork(doc, edge?.networkId)?.name || 'no network';
}

/**
 * Connections for a node, described from that node's point of view.
 *
 * @param {object} doc
 * @param {string} nodeId
 * @returns {object[]}
 */
export function connectionsFor(doc, nodeId) {
  const node = findNode(doc, nodeId);
  const handles = new Map(
    deviceHandles(node).map((handle) => [handle.id, handle.name]),
  );

  return (doc?.edges || [])
    .filter(
      (edge) => edge.sourceNodeId === nodeId || edge.targetNodeId === nodeId,
    )
    .map((edge) => {
      const outgoing = edge.sourceNodeId === nodeId;
      const peerId = outgoing ? edge.targetNodeId : edge.sourceNodeId;
      const peer = findNode(doc, peerId);
      const peerLabel = peer ? nodeLabel(peer) : peerId;
      const handleId = outgoing ? edge.sourceHandleId : edge.targetHandleId;
      const iface = handles.get(handleId) || '';
      const network = edgeNetworkName(doc, edge);

      return {
        id: edge.id,
        peerId,
        peerLabel,
        networkId: edge.networkId,
        networkName: network,
        handleId,
        interface: iface,
        accessibleName: iface
          ? `${iface} connected to ${peerLabel} on network ${network}`
          : `Connected to ${peerLabel} on network ${network}`,
      };
    })
    .sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true }));
}

/**
 * Accessible name for a node: kind, label, network membership and link count.
 *
 * @param {object} doc
 * @param {object} node
 * @returns {string}
 */
export function outlineLabel(doc, node) {
  const links = (doc?.edges || []).filter(
    (edge) => edge.sourceNodeId === node.id || edge.targetNodeId === node.id,
  );
  const kind = node.kind === 'switch' ? 'Switch' : capitalize(node.kind);
  const parts = [`${kind} ${nodeLabel(node)}`];

  if (node.kind === 'switch') {
    const network = findNetwork(doc, node.switch?.networkId);

    parts.push(`network ${network ? network.name : 'unassigned'}`);

    if (network?.alias) {
      parts.push(`VLAN alias ${network.alias}`);
    }
  }

  if (node.kind === 'device' || node.kind === 'switch') {
    parts.push(
      links.length === 1 ? '1 connection' : `${links.length} connections`,
    );
  }

  const comment = nodeComment(node);

  if (comment && node.kind !== 'note') {
    parts.push(`comment: ${comment}`);
  }

  return parts.join(', ');
}

function capitalize(value) {
  const str = String(value || '');
  return str.charAt(0).toUpperCase() + str.slice(1);
}

/**
 * Network list for the outline, with the switches and devices attached to each.
 *
 * @param {object} doc
 * @returns {object[]}
 */
export function networkOutline(doc) {
  return (doc?.networks || [])
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
    .map((network) => {
      const edges = (doc.edges || []).filter(
        (edge) => edge.networkId === network.id,
      );
      const members = new Set();

      edges.forEach((edge) => {
        [edge.sourceNodeId, edge.targetNodeId].forEach((id) => {
          const node = findNode(doc, id);

          if (node?.kind === 'device') {
            members.add(node.device.hostname);
          }
        });
      });

      const alias = network.alias ? `, VLAN alias ${network.alias}` : '';

      return {
        id: network.id,
        name: network.name,
        alias: network.alias,
        color: network.color,
        description: network.description,
        members: [...members].sort(),
        accessibleName:
          `Network ${network.name}${alias}, ` +
          (members.size === 1 ? '1 device' : `${members.size} devices`),
      };
    });
}

/**
 * One-line status used for the canvas live region.
 *
 * @param {object} doc
 * @returns {string}
 */
export function outlineSummary(doc) {
  const summary = documentSummary(doc);

  return (
    `${summary.devices} devices, ${summary.switches} switches, ` +
    `${summary.networks} networks, ${summary.links} connections, ` +
    `${summary.groups} groups, ${summary.notes} notes`
  );
}
