import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import {
  absolutePosition,
  EDGE_HINT_ID,
  FLOW_NODE_TYPES,
  freeSpot,
  fromFlowConnection,
  handlesFor,
  hiddenArea,
  NEW_INTERFACE_HANDLE_ID,
  NODE_HINT_ID,
  networkStyle,
  NETWORK_PATTERNS,
  nodeAriaLabel,
  relativePosition,
  SWITCH_HANDLE_ID,
  toFlowEdges,
  toFlowNodes,
} from '@/builder/adapters/vueflow.js';
import {
  addNetwork,
  addNode,
  DEFAULT_NETWORK_COLORS,
  groupNodes,
  updateEdge,
  updateNode,
} from '@/builder/model.js';
import { outlineLabel } from '@/builder/outline.js';
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

  // Vue Flow spreads domAttributes over its focusable wrapper, the node's
  // only Tab stop.
  test('the wrapper is a toggle button pressed while the node is selected', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const nodes = toFlowNodes(doc, { selectedIds: [alpha.id] });
    const attrs = (id) => nodes.find((node) => node.id === id).domAttributes;

    expect(attrs(alpha.id)).toMatchObject({
      role: 'button',
      'aria-pressed': 'true',
      'aria-describedby': NODE_HINT_ID,
    });
    expect(attrs(bravo.id)['aria-pressed']).toBe('false');
    // Present and undefined, so Vue removes Vue Flow's "node" description.
    expect(attrs(alpha.id)).toHaveProperty('aria-roledescription', undefined);
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
    expect(edge.ariaLabel).toContain('Network EXP from alpha to');
  });

  // Vue Flow writes tabIndex in camel case, which SVG ignores.
  test('connections take focus and report their selection', () => {
    const { doc, edge: model } = sampleDocument();
    const [plain] = toFlowEdges(doc);
    const [selected] = toFlowEdges(doc, { selectedIds: [model.id] });

    expect(plain.domAttributes).toMatchObject({
      tabindex: 0,
      role: 'button',
      'aria-pressed': 'false',
      'aria-describedby': EDGE_HINT_ID,
    });
    expect(selected.domAttributes['aria-pressed']).toBe('true');
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

// R93: the colors people give networks, notes and groups are drawn.
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
