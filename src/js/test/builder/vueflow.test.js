import { readFileSync } from 'node:fs';

import { getTransformForBounds } from '@vue-flow/core';
import { describe, expect, test } from 'vitest';

import {
  absolutePosition,
  connectionStep,
  EDGE_HINT_ID,
  edgeLanes,
  fitPadding,
  fitZoom,
  flowChanges,
  FLOW_NODE_TYPES,
  freeSpot,
  fromFlowConnection,
  handlesFor,
  hiddenArea,
  itemPoint,
  keepFit,
  keptFit,
  nearestNode,
  NEW_INTERFACE_HANDLE_ID,
  NODE_HINT_ID,
  networkStyle,
  NETWORK_PATTERNS,
  nodeAriaLabel,
  nodeInDirection,
  nodeIssueId,
  nodePoint,
  relativePosition,
  SWITCH_HANDLE_ID,
  toFlowEdges,
  toFlowNodes,
  withSelection,
  withTabStop,
  zoomFloor,
} from '@/builder/adapters/vueflow.js';
import {
  addNetwork,
  addNode,
  connect,
  DEFAULT_NETWORK_COLORS,
  groupNodes,
  moveNode,
  removeElements,
  updateEdge,
  updateNode,
} from '@/builder/model.js';
import { outlineLabel } from '@/builder/outline.js';
import { keepUnchanged } from '@/builder/stable.js';
import {
  contrastRatio,
  customNetworkColor,
  drawnColor,
  drawnNetworkColor,
  needsCasing,
  networkColorToken,
} from '@/builder/colors.js';

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

  // Vue Flow spreads domAttributes over its focusable wrapper, out of the
  // Tab order until it is the canvas's Tab stop.
  test('the wrapper is a toggle button pressed while the node is selected', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const nodes = toFlowNodes(doc, { selectedIds: [alpha.id] });
    const attrs = (id) => nodes.find((node) => node.id === id).domAttributes;

    expect(attrs(alpha.id)).toMatchObject({
      tabindex: -1,
      role: 'button',
      'aria-pressed': 'true',
      'aria-describedby': NODE_HINT_ID,
    });
    expect(attrs(bravo.id)['aria-pressed']).toBe('false');
    // Present and undefined, so Vue removes Vue Flow's "node" description.
    expect(attrs(alpha.id)).toHaveProperty('aria-roledescription', undefined);
  });

  test('a selection copies only the nodes and connections it changes', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const nodes = toFlowNodes(doc);
    const selected = withSelection(nodes, [alpha.id]);
    const find = (list, id) => list.find((node) => node.id === id);

    expect(find(selected, alpha.id)).toMatchObject({
      selected: true,
      domAttributes: { role: 'button', 'aria-pressed': 'true' },
    });
    expect(find(selected, alpha.id).data).toBe(find(nodes, alpha.id).data);
    // The rest are the very same objects, so Vue Flow redraws none of them.
    expect(find(selected, bravo.id)).toBe(find(nodes, bravo.id));
    expect(withSelection(selected, [])).toEqual(nodes);
    expect(withSelection(toFlowEdges(doc), [edge.id])[0].domAttributes).toEqual(
      toFlowEdges(doc, { selectedIds: [edge.id] })[0].domAttributes,
    );
  });

  // The canvas is one Tab stop, which moves as focus does (a roving
  // tabindex): one item at a time takes tabindex 0.
  test('the Tab stop copies only the one item it moves to', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const nodes = toFlowNodes(doc);
    const stopped = withTabStop(nodes, alpha.id);
    const find = (list, id) => list.find((node) => node.id === id);

    expect(find(stopped, alpha.id).domAttributes).toEqual({
      ...find(nodes, alpha.id).domAttributes,
      tabindex: 0,
    });
    expect(find(stopped, bravo.id)).toBe(find(nodes, bravo.id));
    expect(
      stopped.filter((node) => node.domAttributes.tabindex === 0),
    ).toHaveLength(1);
    // Moved on, the old stop is the very item it was before.
    const moved = keepUnchanged(withTabStop(nodes, bravo.id), stopped);
    expect(find(moved, alpha.id)).toBe(find(nodes, alpha.id));
    expect(withTabStop(nodes, null)).toBe(nodes);
    expect(
      withTabStop(toFlowEdges(doc), edge.id)[0].domAttributes.tabindex,
    ).toBe(0);
  });

  test('a node the diagram checks flag carries what they found, and is described by it', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const issue = { level: 'error', text: '1 error: hostname is required.' };
    const nodes = toFlowNodes(doc, { issues: new Map([[alpha.id, issue]]) });
    const find = (id) => nodes.find((node) => node.id === id);

    expect(find(alpha.id).data.issue).toBe(issue);
    expect(find(alpha.id).domAttributes['aria-describedby']).toBe(
      `${nodeIssueId(alpha.id)} ${NODE_HINT_ID}`,
    );
    expect(find(bravo.id).data.issue).toBeNull();
    expect(find(bravo.id).domAttributes['aria-describedby']).toBe(NODE_HINT_ID);
  });

  test('canvas and outline name every kind of node the same way', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const withNote = addNode(grouped, {
      kind: 'note',
      text: 'Firewall rules pending review',
    }).doc;

    for (const node of withNote.nodes) {
      expect(nodeAriaLabel(withNote, node)).toBe(outlineLabel(withNote, node));
    }

    const group = withNote.nodes.find((node) => node.kind === 'group');
    expect(nodeAriaLabel(withNote, group)).toContain('2 members');
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
    expect(edge.ariaLabel).toBe('Network EXP from alpha (eth0) to EXP');
  });

  // Two connections from one device to one switch differ only by the
  // interface at the device.
  test('a connection names the interface at its device end', () => {
    const { doc, sw } = sampleDocument();
    const dual = addNode(doc, {
      kind: 'device',
      hostname: 'dual',
      position: { x: 0, y: 400 },
      interfaces: [{ name: 'eth0' }, { name: 'eth1' }],
    });
    let next = dual.doc;

    for (const handle of dual.node.device.interfaces) {
      next = connect(next, {
        sourceNodeId: dual.node.id,
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
      }).doc;
    }

    expect(
      toFlowEdges(next)
        .filter((edge) => edge.source === dual.node.id)
        .map((edge) => edge.ariaLabel),
    ).toEqual([
      'Network EXP from dual (eth0) to EXP',
      'Network EXP from dual (eth1) to EXP',
    ]);
  });

  // Vue Flow writes tabIndex in camel case, which SVG ignores. A connection
  // is in the Tab order only as the canvas's Tab stop (withTabStop).
  test('connections take focus and report their selection', () => {
    const { doc, edge: model } = sampleDocument();
    const [plain] = toFlowEdges(doc);
    const [selected] = toFlowEdges(doc, { selectedIds: [model.id] });

    expect(plain.domAttributes).toMatchObject({
      tabindex: -1,
      role: 'button',
      'aria-pressed': 'false',
      'aria-describedby': EDGE_HINT_ID,
    });
    expect(selected.domAttributes['aria-pressed']).toBe('true');
  });

  // Lines drawn without a route bend each in a column of their own; with
  // the other end nearest the switch's handle bending first, none crosses.
  test('connections that share a switch bend in lanes of their own', () => {
    const { doc, sw, bravo, edge } = sampleDocument();
    let next = addNode(doc, {
      kind: 'device',
      hostname: 'charlie',
      position: { x: 0, y: -400 },
    }).doc;
    const charlie = next.nodes.find((node) => node.label === 'charlie');

    next = connect(next, { sourceNodeId: bravo.id, targetNodeId: sw.id }).doc;
    next = connect(next, { sourceNodeId: sw.id, targetNodeId: charlie.id }).doc;

    const into = (id) =>
      next.edges.find(
        (entry) => entry.sourceNodeId === id && entry.targetNodeId === sw.id,
      ).id;
    const lanes = edgeLanes(next);

    // Alpha sits level with the switch, bravo 200 below it.
    expect(lanes.get(edge.id)).toEqual({ index: 0, count: 2 });
    expect(lanes.get(into(bravo.id))).toEqual({ index: 1, count: 2 });
    // The only connection out of the switch has a column to itself.
    expect(
      lanes.has(next.edges.find((e) => e.targetNodeId === charlie.id).id),
    ).toBe(false);
    expect(
      toFlowEdges(next).find((entry) => entry.id === edge.id).data.lane,
    ).toEqual({ index: 0, count: 2 });
    expect(toFlowEdges(doc)[0].data.lane).toBeNull();
  });

  test('a connection label is part of its name', () => {
    const { doc, edge: model } = sampleDocument();
    const [edge] = toFlowEdges(updateEdge(doc, model.id, { label: 'uplink' }));

    expect(edge.ariaLabel).toMatch(/, labelled uplink$/);
  });

  test('network styling adds a dash pattern, not colour alone', () => {
    const { doc, network } = sampleDocument();
    const style = networkStyle(doc, network.id);

    expect(NETWORK_PATTERNS).toContain(style.pattern);
    expect(networkStyle(doc, network.id)).toEqual(style);
    expect(style.label).toBe('EXP');
  });

  test('no two of the first 32 networks share both color and pattern', () => {
    const doc = {
      networks: Array.from({ length: 32 }, (_, index) => ({
        id: `net-${index}`,
        name: `N${index}`,
      })),
    };
    const looks = doc.networks.map((network) => {
      const style = networkStyle(doc, network.id);

      return `${style.token}/${style.pattern}`;
    });

    expect(new Set(looks).size).toBe(32);
  });

  test('the new-interface handle asks the model for a new interface', () => {
    expect(
      fromFlowConnection({
        source: 'switch',
        sourceHandle: SWITCH_HANDLE_ID,
        target: 'device',
        targetHandle: NEW_INTERFACE_HANDLE_ID,
      }),
    ).toEqual({
      sourceNodeId: 'switch',
      targetNodeId: 'device',
      sourceHandleId: null,
      targetHandleId: null,
    });
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

// The hue, in degrees, and the saturation of a #rrggbb color.
function hsl(hex) {
  const [r, g, b] = hex
    .slice(1)
    .match(/../g)
    .map((pair) => parseInt(pair, 16) / 255);
  const [max, min] = [Math.max(r, g, b), Math.min(r, g, b)];
  const delta = max - min;
  const sector =
    delta === 0
      ? 0
      : max === r
        ? (g - b) / delta
        : max === g
          ? (b - r) / delta + 2
          : (r - g) / delta + 4;

  return {
    hue: (sector * 60 + 360) % 360,
    saturation: delta === 0 ? 0 : delta / (1 - Math.abs(max + min - 1)),
  };
}

// The colors people give networks, notes and groups are drawn.
describe('colors', () => {
  test('a network color the user chose is drawn; the picked ones use tokens', () => {
    expect(customNetworkColor('#ff0000')).toBe('#ff0000');
    expect(customNetworkColor(' #F00 ')).toBe('#F00');
    // addNetwork's picks, in any case, and nothing at all.
    expect(customNetworkColor('#2f6fbf')).toBe('');
    expect(customNetworkColor('#2F6FBF')).toBe('');
    expect(customNetworkColor('')).toBe('');
    // Not a color: never drawn, so the line keeps its token.
    expect(drawnColor('not a color')).toBe('');
  });

  // A suggested color chosen in the picker changed nothing on the canvas:
  // the line kept the token of the network's place.
  test("a network given a suggested color is drawn in that color's token", () => {
    const doc = {
      networks: [
        { id: 'a', name: 'A', color: '#2f6fbf' },
        { id: 'b', name: 'B', color: '#A3273F' },
        { id: 'c', name: 'C' },
        { id: 'd', name: 'D', color: '#ff0000' },
      ],
    };

    expect(networkStyle(doc, 'a').token).toBe(0);
    expect(networkStyle(doc, 'b').token).toBe(4);
    // No color, or one the user chose, which is drawn as chosen: the token
    // of its place.
    expect(networkStyle(doc, 'c').token).toBe(2);
    expect(networkStyle(doc, 'd').token).toBe(3);

    expect(networkColorToken('#8A4B8F')).toBe(7);
    expect(networkColorToken('#ff0000')).toBe(-1);
    expect(networkColorToken('')).toBe(-1);
    // The switch's swatch and the Inspector's chip draw it the same way.
    expect(drawnNetworkColor(' #a3273f ')).toBe('var(--bx-net-4)');
    expect(drawnNetworkColor('#ff0000')).toBe('#ff0000');
    expect(drawnNetworkColor('not a color')).toBe('');
  });

  // Plum was drawn in slate: --bx-net-7 was a grey.
  test("each theme's network token is its suggested color's hue, and keeps 3:1", () => {
    const css = readFileSync(
      new URL('../../src/builder/builder.css', import.meta.url),
      'utf8',
    );
    // The light theme's, then the dark theme's.
    const tokens = [...css.matchAll(/--bx-net-(\d): (#[0-9a-f]{6});/gi)];

    expect(tokens).toHaveLength(2 * DEFAULT_NETWORK_COLORS.length);
    tokens.forEach(([, index, value], at) => {
      const theme = at < DEFAULT_NETWORK_COLORS.length ? 'light' : 'dark';
      const token = hsl(value);
      const suggested = hsl(DEFAULT_NETWORK_COLORS[index]);
      const apart = Math.abs(token.hue - suggested.hue);

      expect(Math.min(apart, 360 - apart), value).toBeLessThan(25);
      expect(token.saturation, value).toBeGreaterThan(0.3);
      expect(needsCasing(value)[theme], value).toBe(false);
    });
  });

  test('networks given their colors in turn look as they did', () => {
    let doc = { networks: [] };

    for (let index = 0; index < 10; index += 1) {
      doc = addNetwork(doc, { name: `N${index}` }).doc;
    }

    expect(doc.networks[7].color).toBe(DEFAULT_NETWORK_COLORS[7]);
    expect(
      doc.networks.map((network) => networkStyle(doc, network.id).token),
    ).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 0, 1]);
  });

  test("a line that would fade into a theme's canvas is cased there", () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21);
    // Black fades into the dark canvas, white into the light one, and
    // neither needs a casing where it stands out.
    expect(needsCasing('#000000')).toEqual({ light: false, dark: true });
    expect(needsCasing('#ffffff')).toEqual({ light: true, dark: false });
    expect(needsCasing('rgb(255, 255, 0)')).toEqual({
      light: true,
      dark: false,
    });
    // A contrast that cannot be read is cased in both.
    expect(needsCasing('#ff000080')).toEqual({ light: true, dark: true });
    // Measured against the canvas and its grid, not white: this blue keeps
    // 3.15:1 against white, but 2.91:1 against the light canvas.
    expect(needsCasing('#3498db').light).toBe(true);
  });
});

describe('keeping keyboard focus in view', () => {
  const box = (left, top, width, height) => ({
    left,
    top,
    right: left + width,
    bottom: top + height,
    width,
    height,
  });
  const pane = box(100, 50, 600, 400);
  // Vue Flow's default corners: zoom controls bottom left, minimap bottom
  // right.
  const controls = box(115, 360, 30, 75);
  const minimap = box(485, 285, 200, 150);

  test('an item in the pane and clear of the overlays is not hidden', () => {
    expect(hiddenArea(box(300, 100, 100, 50), pane, [controls, minimap])).toBe(
      0,
    );
  });

  test('parts outside the pane or under an overlay count as hidden', () => {
    // 20px of 100 sticks out on the left.
    expect(hiddenArea(box(80, 100, 100, 50), pane)).toBe(20 * 50);
    // The bottom right corner lies under the minimap.
    expect(hiddenArea(box(450, 250, 100, 50), pane, [minimap])).toBe(65 * 15);
  });

  test('a spot clear of the overlays is the pane centre when that is free', () => {
    expect(freeSpot(pane, 100, 50, [controls, minimap])).toEqual({
      x: 300,
      y: 200,
      hidden: 0,
    });
  });

  test('when the centre is covered, the nearest free spot is chosen', () => {
    // A minimap in a small pane covers its centre.
    const small = box(0, 0, 400, 300);
    const cover = box(185, 135, 200, 150);
    const spot = freeSpot(small, 120, 60, [cover]);

    expect(spot.hidden).toBe(0);
    expect(
      hiddenArea(box(spot.x - 60, spot.y - 30, 120, 60), small, [cover]),
    ).toBe(0);
  });

  test('an item that cannot be clear anywhere is placed where least is hidden', () => {
    const small = box(0, 0, 200, 100);
    const spot = freeSpot(small, 150, 80, [box(0, 50, 200, 50)]);

    expect(spot.hidden).toBe(150 * 30);
    expect(spot.y).toBe(40);
  });
});

describe('moving keyboard focus between nodes', () => {
  // Nodes 100 by 100 at the given top left corners, named by id.
  const node = (
    id,
    x,
    y,
    kind = 'device',
    size = { width: 100, height: 100 },
  ) => ({
    id,
    kind,
    position: { x, y },
    size,
  });
  // A row of three, a node below the middle one, and one down and far to
  // the right, off the row's line.
  const grid = {
    nodes: [
      node('left', 0, 0),
      node('middle', 200, 0),
      node('right', 400, 0),
      node('below', 200, 200),
      node('far', 700, 90),
    ],
    edges: [
      { id: 'e1', sourceNodeId: 'left', targetNodeId: 'middle' },
      { id: 'e2', sourceNodeId: 'below', targetNodeId: 'middle' },
      { id: 'e3', sourceNodeId: 'middle', targetNodeId: 'right' },
    ],
  };
  const from = (id, key) =>
    nodeInDirection(grid, itemPoint(grid, { kind: 'nodes', id }), key, id);

  test('an arrow key goes to the nearest node that way, in line first', () => {
    expect(from('left', 'ArrowRight')).toBe('middle');
    expect(from('middle', 'ArrowRight')).toBe('right');
    expect(from('middle', 'ArrowDown')).toBe('below');
    expect(from('below', 'ArrowUp')).toBe('middle');
    expect(from('middle', 'ArrowLeft')).toBe('left');
    // The node in line comes before a nearer one off to the side.
    expect(from('right', 'ArrowRight')).toBe('far');
    expect(from('below', 'ArrowRight')).toBe('right');
    // Nothing lies that way: focus stays.
    expect(from('left', 'ArrowLeft')).toBeNull();
    expect(from('middle', 'ArrowUp')).toBeNull();
    expect(nodeInDirection(grid, { x: 0, y: 0 }, 'Tab')).toBeNull();
  });

  test('from a connection, the arrow keys start halfway along it', () => {
    const point = itemPoint(grid, { kind: 'edges', id: 'e2' });

    expect(point).toEqual({ x: 250, y: 150 });
    expect(nodeInDirection(grid, point, 'ArrowDown')).toBe('below');
    expect(nodeInDirection(grid, point, 'ArrowUp')).toBe('middle');
    expect(itemPoint(grid, { kind: 'edges', id: 'gone' })).toBeNull();
    expect(itemPoint(grid, { kind: 'nodes', id: 'gone' })).toBeNull();
  });

  test('a group is where its title is, so Up from a member reaches it', () => {
    const doc = {
      nodes: [
        node('group', 0, 0, 'group', { width: 400, height: 300 }),
        node('member', 150, 60),
        node('other', 280, 60),
      ],
      edges: [],
    };
    const up = (id) =>
      nodeInDirection(
        doc,
        itemPoint(doc, { kind: 'nodes', id }),
        'ArrowUp',
        id,
      );

    expect(nodePoint(doc.nodes[0])).toEqual({ x: 200, y: 0 });
    expect(up('member')).toBe('group');
    expect(up('other')).toBe('group');
    expect(
      nodeInDirection(doc, nodePoint(doc.nodes[0]), 'ArrowDown', 'group'),
    ).toBe('member');
  });

  test('nodes at the same point are reached in the document order', () => {
    const doc = {
      nodes: [node('a', 0, 0), node('b', 0, 0), node('c', 0, 0)],
      edges: [],
    };
    const go = (id, key) => nodeInDirection(doc, { x: 50, y: 50 }, key, id);

    expect(go('a', 'ArrowRight')).toBe('b');
    expect(go('b', 'ArrowDown')).toBe('c');
    expect(go('c', 'ArrowLeft')).toBe('b');
    expect(go('b', 'ArrowUp')).toBe('a');
    expect(go('c', 'ArrowRight')).toBeNull();
  });

  test('from the canvas itself, the node nearest a point', () => {
    expect(nearestNode(grid, { x: 260, y: 260 })).toBe('below');
    expect(nearestNode(grid, { x: -500, y: -500 })).toBe('left');
    expect(nearestNode({ nodes: [] }, { x: 0, y: 0 })).toBeNull();
  });

  test('Page Down and Page Up go round a node’s connections', () => {
    expect(connectionStep(grid, 'middle', null, 1)).toBe('e1');
    expect(connectionStep(grid, 'middle', 'e1', 1)).toBe('e2');
    expect(connectionStep(grid, 'middle', 'e3', 1)).toBe('e1');
    expect(connectionStep(grid, 'middle', null, -1)).toBe('e3');
    expect(connectionStep(grid, 'middle', 'e1', -1)).toBe('e3');
    // A connection of another node starts from the first.
    expect(connectionStep(grid, 'left', 'e3', 1)).toBe('e1');
    expect(connectionStep(grid, 'far', null, 1)).toBeNull();
  });
});

describe('handing Vue Flow only what changed', () => {
  // The flow nodes of an edited document, keeping those it left alone.
  const after = (before, doc, options) =>
    keepUnchanged(toFlowNodes(doc, options), before);
  const ids = (items) => items.map((item) => item.id);
  const NODE_KEYS = ['type', 'parentNode'];

  test('a new selection changes only the nodes it presses or releases', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const nodes = toFlowNodes(doc);
    const first = withSelection(nodes, [alpha.id, sw.id]);
    // As the canvas does: a node still selected stays the same object.
    const changes = flowChanges(
      first,
      keepUnchanged(withSelection(nodes, [bravo.id, sw.id]), first),
      NODE_KEYS,
    );

    expect(ids(changes.changed)).toEqual([alpha.id, bravo.id]);
    expect(changes).toMatchObject({
      added: [],
      removed: [],
      reordered: false,
      rebuild: false,
    });
  });

  test('an edit changes only the nodes it touched', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const before = toFlowNodes(doc);
    const moved = after(before, moveNode(doc, alpha.id, { x: 48, y: 96 }));
    const removed = after(before, removeElements(doc, { nodes: [bravo.id] }));
    const added = addNode(doc, { kind: 'note', text: 'later' });

    expect(ids(flowChanges(before, moved, NODE_KEYS).changed)).toEqual([
      alpha.id,
    ]);
    expect(flowChanges(before, removed, NODE_KEYS)).toMatchObject({
      changed: [],
      removed: [bravo.id],
      reordered: false,
      rebuild: false,
    });
    expect(
      flowChanges(before, after(before, added.doc), NODE_KEYS),
    ).toMatchObject({
      added: [{ id: added.node.id }],
      changed: [],
      reordered: false,
      rebuild: false,
    });
    // Nothing changed: the same list.
    expect(after(before, doc)).toBe(before);
  });

  test('a new group goes before the nodes, so the list is put in order', () => {
    const { doc } = sampleDocument();
    const before = toFlowNodes(doc);
    const { doc: next } = addNode(doc, { kind: 'group' });

    expect(flowChanges(before, after(before, next), NODE_KEYS)).toMatchObject({
      reordered: true,
      rebuild: false,
    });
  });

  test('a node that moves into a group, or a repeated id, takes the whole list', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const before = toFlowNodes(doc);
    const grouped = after(before, groupNodes(doc, [alpha.id, bravo.id]).doc);

    expect(flowChanges(before, grouped, NODE_KEYS).rebuild).toBe(true);
    expect(flowChanges(before, [...before, before[0]], NODE_KEYS).rebuild).toBe(
      true,
    );
  });

  test('a connection that changes its ends takes the whole list', () => {
    const { doc, edge } = sampleDocument();
    const before = toFlowEdges(doc);
    const relabelled = keepUnchanged(
      toFlowEdges(updateEdge(doc, edge.id, { label: 'uplink' })),
      before,
    );
    const ends = ['source', 'target', 'sourceHandle', 'targetHandle'];

    expect(flowChanges(before, relabelled, ends)).toMatchObject({
      changed: [{ id: edge.id }],
      rebuild: false,
    });
    expect(
      flowChanges(before, [{ ...before[0], target: 'elsewhere' }], ends)
        .rebuild,
    ).toBe(true);
  });
});

describe('fitting large diagrams', () => {
  const pane = { width: 790, height: 646 };

  test('the fitted zoom is the one Vue Flow takes', () => {
    for (const bounds of [
      { x: 0, y: 0, width: 400, height: 300 },
      { x: -120, y: 40, width: 9000, height: 2400 },
      { x: 0, y: 0, width: 1800, height: 7000 },
    ]) {
      const { zoom } = getTransformForBounds(
        bounds,
        pane.width,
        pane.height,
        0,
        Infinity,
        0.1,
      );

      expect(fitZoom(bounds, pane)).toBeCloseTo(zoom, 10);
    }

    expect(fitZoom({ width: 0, height: 0 }, pane)).toBe(Infinity);
    expect(fitZoom({ width: 10, height: 10 }, { width: 0, height: 0 })).toBe(
      Infinity,
    );
  });

  // Vue Flow's fit view takes a padding for each side, in pixels.
  test('the fitted zoom with room on each side is the one Vue Flow takes', () => {
    const bounds = { x: -120, y: 40, width: 9000, height: 2400 };
    const room = { top: 30, right: 250, bottom: 36, left: 70 };
    const { zoom } = getTransformForBounds(
      bounds,
      pane.width,
      pane.height,
      0,
      Infinity,
      {
        top: `${room.top}px`,
        right: `${room.right}px`,
        bottom: `${room.bottom}px`,
        left: `${room.left}px`,
      },
    );

    expect(fitZoom(bounds, pane, room)).toBeCloseTo(zoom, 10);
    expect(fitZoom(bounds, pane, { ...room, left: 600 })).toBe(0);
  });

  test('a fit keeps the nodes clear of the minimap and the zoom controls', () => {
    const screen = { left: 100, top: 50, width: 790, height: 646 };
    Object.assign(screen, {
      right: screen.left + screen.width,
      bottom: screen.top + screen.height,
    });
    const box = (left, top, width, height) => ({
      left,
      top,
      width,
      height,
      right: left + width,
      bottom: top + height,
    });
    // Vue Flow's corners, 15px in: controls bottom left, minimap bottom
    // right.
    const controls = box(115, 625, 20, 56);
    const minimap = box(675, 531, 200, 150);
    const clear = (bounds, overlays) => {
      const room = fitPadding(bounds, screen, overlays);
      const view = getTransformForBounds(
        bounds,
        screen.width,
        screen.height,
        0,
        2,
        Object.fromEntries(
          Object.entries(room).map(([side, px]) => [side, `${px}px`]),
        ),
      );
      const shown = box(
        screen.left + view.x + bounds.x * view.zoom,
        screen.top + view.y + bounds.y * view.zoom,
        bounds.width * view.zoom,
        bounds.height * view.zoom,
      );

      return {
        room,
        zoom: view.zoom,
        hidden: hiddenArea(shown, screen, overlays),
      };
    };

    for (const bounds of [
      { x: 0, y: 0, width: 7360, height: 2756 },
      { x: 0, y: 0, width: 800, height: 3000 },
      { x: -50, y: -50, width: 400, height: 300 },
    ]) {
      const plain = fitZoom(bounds, screen);
      const fit = clear(bounds, [controls, minimap]);

      expect(fit.hidden, JSON.stringify(bounds)).toBe(0);
      expect(fit.zoom).toBeLessThanOrEqual(plain);
    }

    // A wide diagram keeps the room below the overlays; a tall one, beside
    // them, where the room costs less.
    const wide = clear({ x: 0, y: 0, width: 7360, height: 2756 }, [
      controls,
      minimap,
    ]).room;
    expect(wide.bottom).toBeGreaterThan(150);
    expect(wide.right).toBeLessThan(50);
    const tall = clear({ x: 0, y: 0, width: 800, height: 3000 }, [
      controls,
      minimap,
    ]).room;
    expect(tall.right).toBeGreaterThan(200);
    expect(tall.bottom).toBeLessThan(50);

    // With the minimap hidden, only the controls take room, on one side.
    const bare = fitPadding({ x: 0, y: 0, width: 7360, height: 2756 }, screen, [
      controls,
    ]);
    const base = fitPadding({ x: 0, y: 0, width: 7360, height: 2756 }, screen);
    expect(bare.right).toBe(base.right);
    expect(bare.top).toBe(base.top);
    expect(bare.left + bare.bottom).toBeGreaterThan(base.left + base.bottom);
  });

  test('the least zoom goes below its usual limit only for a diagram that needs it', () => {
    const small = { width: 800, height: 600 };
    const large = { width: 9000, height: 2400 };

    expect(zoomFloor(small, pane, 0.2)).toBe(0.2);
    expect(zoomFloor(large, pane, 0.2)).toBeLessThan(0.2);
    // Zooming out by hand reaches past the fitted zoom.
    expect(zoomFloor(large, pane, 0.2)).toBeLessThan(fitZoom(large, pane));
    // Before the pane has a size, the usual limit.
    expect(zoomFloor(large, { width: 0, height: 0 }, 0.2)).toBe(0.2);
  });

  test('the least zoom leaves room to fit clear of the minimap at its size', () => {
    const square = { x: 0, y: 0, width: 4000, height: 3270 };
    const corner = (width, height) => ({
      left: pane.width - 15 - width,
      top: pane.height - 15 - height,
      right: pane.width - 15,
      bottom: pane.height - 15,
    });
    const plain = zoomFloor(square, pane, 0.2);
    const small = zoomFloor(square, pane, 0.2, [corner(202, 152)]);
    const large = zoomFloor(square, pane, 0.2, [corner(397, 297)]);
    const box = { left: 0, top: 0, right: pane.width, bottom: pane.height };
    const clearOf = (overlay) =>
      fitZoom(square, pane, fitPadding(square, { ...pane, ...box }, [overlay]));

    // A larger minimap takes more room, so the floor is lower, and always
    // half the zoom that fits the diagram clear of it.
    expect(small).toBeLessThan(plain);
    expect(large).toBeLessThan(small);
    expect(large).toBeCloseTo(clearOf(corner(397, 297)) / 2, 10);
    // A small diagram keeps the usual limit.
    expect(
      zoomFloor({ width: 800, height: 600 }, pane, 0.2, [corner(397, 297)]),
    ).toBe(0.2);
    // An overlay that leaves no room is no floor: the plain one applies.
    expect(
      zoomFloor(square, pane, 0.2, [
        { left: -10, top: -10, right: 800, bottom: 700 },
      ]),
    ).toBe(plain);
  });
});

describe('Fit, and back to the view before it', () => {
  const before = { x: 40, y: -12, zoom: 1.44 };
  const fitted = { x: 212.5, y: 96.25, zoom: 0.31 };

  test('Fit keeps the view from before it, and the view it made', () => {
    const kept = keepFit({ ...before, extra: true }, fitted);

    expect(kept).toEqual({ before, fitted });
    // Copies: Vue Flow replaces its viewport object on each change.
    expect(kept.fitted).not.toBe(fitted);

    // A Fit that changed nothing has nothing to go back to.
    expect(keepFit(fitted, { ...fitted, x: fitted.x + 0.2 })).toBeNull();
  });

  test('the view Fit made keeps it; any other pan or zoom forgets it', () => {
    const kept = keepFit(before, fitted);

    expect(keptFit(kept, { ...fitted })).toBe(kept);
    // Rounding in Vue Flow is no change.
    expect(keptFit(kept, { ...fitted, y: fitted.y - 0.3 })).toBe(kept);
    // A pan, a zoom, and going back to the view from before.
    expect(keptFit(kept, { ...fitted, x: fitted.x + 24 })).toBeNull();
    expect(keptFit(kept, { ...fitted, zoom: fitted.zoom * 1.2 })).toBeNull();
    expect(keptFit(kept, before)).toBeNull();
    // Nothing kept stays so.
    expect(keptFit(null, fitted)).toBeNull();
  });
});
