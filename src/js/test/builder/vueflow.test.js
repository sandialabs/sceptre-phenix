import { describe, expect, test } from 'vitest';

import {
  absolutePosition,
  FLOW_NODE_TYPES,
  fromFlowConnection,
  handlesFor,
  networkStyle,
  NETWORK_PATTERNS,
  nodeAriaLabel,
  relativePosition,
  SWITCH_HANDLE_ID,
  toFlowEdges,
  toFlowNodes,
} from '@/builder/adapters/vueflow.js';
import { groupNodes, updateNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('flow nodes', () => {
  test('every model kind maps to a custom node type', () => {
    expect(FLOW_NODE_TYPES).toEqual({
      device: 'builderDevice',
      switch: 'builderSwitch',
      note: 'builderNote',
      group: 'builderGroup',
    });
  });

  test('nodes carry icon key, comment and handles', () => {
    const { doc, alpha } = sampleDocument();
    const node = toFlowNodes(doc).find((entry) => entry.id === alpha.id);

    expect(node.type).toBe('builderDevice');
    expect(node.data.iconKey).toBe('linux');
    expect(node.data.handles).toHaveLength(1);
    expect(node.ariaLabel).toContain('1 connection');
  });

  test('groups are emitted before their children', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const flow = toFlowNodes(grouped);
    const groupIndex = flow.findIndex((node) => node.type === 'builderGroup');
    const childIndex = flow.findIndex((node) => node.id === alpha.id);

    expect(groupIndex).toBeLessThan(childIndex);
    expect(flow[childIndex].parentNode).toBe(flow[groupIndex].id);
  });

  test('child positions are relative to their parent and reversible', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const child = grouped.nodes.find((node) => node.id === alpha.id);
    const relative = relativePosition(grouped, child);

    expect(absolutePosition(grouped, child.parentId, relative)).toEqual(
      child.position,
    );
  });

  test('the comment shown on hover is the spec description', () => {
    const { doc, alpha } = sampleDocument();
    const next = updateNode(doc, alpha.id, {
      device: { spec: { general: { description: 'jump host' } } },
    });
    const node = toFlowNodes(next).find((entry) => entry.id === alpha.id);

    expect(node.data.comment).toBe('jump host');
    expect(nodeAriaLabel(next, next.nodes[2])).toBeTruthy();
  });
});

describe('handles', () => {
  test('a switch exposes a single bus handle named for its network', () => {
    const { doc, sw } = sampleDocument();
    const node = doc.nodes.find((entry) => entry.id === sw.id);
    const [handle] = handlesFor(doc, node);

    expect(handle.id).toBe(SWITCH_HANDLE_ID);
    expect(handle.label).toContain('EXP');
  });

  test('device handles report their connection state by name', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const [connected] = handlesFor(
      doc,
      doc.nodes.find((node) => node.id === alpha.id),
    );
    const [free] = handlesFor(
      doc,
      doc.nodes.find((node) => node.id === bravo.id),
    );

    expect(connected.connected).toBe(true);
    expect(connected.label).toContain('on network EXP');
    expect(free.connected).toBe(false);
    expect(free.label).toContain('not connected');
  });
});

describe('flow edges', () => {
  test('edges use the semantic network edge type and a label', () => {
    const { doc } = sampleDocument();
    const [edge] = toFlowEdges(doc);

    expect(edge.type).toBe('builderNetwork');
    expect(edge.label).toBe('EXP');
    expect(edge.targetHandle).toBe(SWITCH_HANDLE_ID);
    expect(edge.ariaLabel).toContain('Network EXP from alpha to');
  });

  test('network styling adds a dash pattern, not colour alone', () => {
    const { doc, network } = sampleDocument();
    const style = networkStyle(doc, network.id);

    expect(NETWORK_PATTERNS).toContain(style.pattern);
    expect(networkStyle(doc, network.id)).toEqual(style);
    expect(style.label).toBe('EXP');
  });

  test('a vue flow connection becomes a model connection', () => {
    expect(
      fromFlowConnection({
        source: 'a',
        target: 'b',
        sourceHandle: 'h1',
        targetHandle: SWITCH_HANDLE_ID,
      }),
    ).toEqual({
      sourceNodeId: 'a',
      targetNodeId: 'b',
      sourceHandleId: 'h1',
      targetHandleId: null,
    });
  });
});
