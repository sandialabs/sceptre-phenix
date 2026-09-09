import { describe, expect, test } from 'vitest';

import {
  buildOutline,
  connectionsFor,
  edgeNetworkName,
  networkOutline,
  outlineLabel,
  outlineSummary,
} from '@/builder/outline.js';
import { addNode, groupNodes } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('semantic outline', () => {
  test('mirrors the canvas contents', () => {
    const { doc } = sampleDocument();
    const outline = buildOutline(doc);

    expect(outline.map((item) => item.kind)).toEqual([
      'switch',
      'device',
      'device',
    ]);
    expect(outline.map((item) => item.label)).toContain('alpha');
  });

  test('devices list their interfaces so connections can be made without a mouse', () => {
    const { doc, alpha } = sampleDocument();
    const item = buildOutline(doc).find((entry) => entry.id === alpha.id);

    expect(item.interfaces).toHaveLength(1);
    expect(item.interfaces[0].name).toBe('eth0');
  });

  test('groups nest their members', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const outline = buildOutline(grouped);
    const group = outline.find((item) => item.kind === 'group');

    expect(group.children.map((child) => child.id).sort()).toEqual(
      [alpha.id, bravo.id].sort(),
    );
    expect(outline.filter((item) => item.id === alpha.id)).toHaveLength(0);
  });

  test('accessible names name the network, not just a colour', () => {
    const { doc, sw, alpha } = sampleDocument();
    const switchNode = doc.nodes.find((node) => node.id === sw.id);
    const device = doc.nodes.find((node) => node.id === alpha.id);

    expect(outlineLabel(doc, switchNode)).toContain('network EXP');
    expect(outlineLabel(doc, switchNode)).toContain('VLAN alias 100');
    expect(outlineLabel(doc, device)).toContain('1 connection');
  });

  test('the comment is part of the accessible name', () => {
    const { doc, alpha } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === alpha.id);

    node.device.spec.general.description = 'jump host';

    expect(outlineLabel(doc, node)).toContain('comment: jump host');
  });

  test('connections are described from the node point of view', () => {
    const { doc, alpha, sw } = sampleDocument();
    const [connection] = connectionsFor(doc, alpha.id);

    expect(connection.peerId).toBe(sw.id);
    expect(connection.networkId).toBe(doc.networks[0].id);
    expect(edgeNetworkName(doc, doc.edges[0])).toBe('EXP');
  });

  test('the network outline lists members', () => {
    const { doc } = sampleDocument();
    const [network] = networkOutline(doc);

    expect(network.name).toBe('EXP');
    expect(network.members).toEqual(['alpha']);
    expect(network.accessibleName).toContain('VLAN alias 100');
  });

  test('the summary counts every element kind', () => {
    const { doc } = sampleDocument();
    const withNote = addNode(doc, { kind: 'note', text: 'hi' }).doc;

    expect(outlineSummary(withNote)).toBe(
      '2 devices, 1 switches, 1 networks, 1 connections, 0 groups, 1 notes',
    );
  });
});
