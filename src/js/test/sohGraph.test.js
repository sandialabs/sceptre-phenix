// Shared State of Health graph helpers: the VM and VLAN counts and the colors
// nodes are drawn and exported in.
import { describe, expect, it } from 'vitest';

import {
  countVlans,
  countVms,
  displayColor,
  nodeFill,
} from '@/utils/soh/graph.js';
import { sohFixture } from './sohFixture.js';

describe('counts', () => {
  const { nodes } = sohFixture();
  const running = nodes.filter(
    (n) => n.status === 'running' || n.image === 'switch',
  );

  it.each([
    // a firewall, a router, 16 hosts and the historian; 4 VLANs
    ['the whole graph', nodes, 19, 4],
    // the firewall, the router, 3 hosts and the historian
    ['the nodes a filter shows', running, 6, 4],
    [
      'a VLAN named in upper case',
      [{ id: 1, image: 'SWITCH' }, { id: 2 }],
      1,
      1,
    ],
    ['no nodes', [], 0, 0],
    ['a graph not loaded yet', null, 0, 0],
  ])('counts the VMs and VLANs of %s', (_, list, vms, vlans) => {
    expect(countVms(list)).toBe(vms);
    expect(countVlans(list)).toBe(vlans);
  });
});

describe('node colors', () => {
  it('matches the graph: status colors only while running, VLAN icon always', () => {
    expect(nodeFill({ status: 'running' }, true)).toBe('#4F8F00');
    expect(nodeFill({ status: 'running' }, false)).toBeUndefined();
    expect(nodeFill({ status: 'external' }, false)).toBe('#005493');
    expect(nodeFill({ status: 'ignore' }, false)).toBe('url(#switch)');
  });

  const linux = (status, style) => ({
    status,
    image: 'linux',
    tags: style ? { __sohStyle: style } : {},
  });

  it.each([
    ['the status color', linux('notrunning'), true, '#670b00'],
    ['a named status color', linux('notboot'), true, '#000000'],
    ['black for a VM with no status color', linux('running'), false, '#000000'],
    ['the VLAN color', { status: 'ignore', image: 'switch' }, true, '#9aa7b5'],
    [
      'a custom rgb() fill over the status color',
      linux('running', 'stroke: red; fill: rgb(1, 2, 3); '),
      true,
      '#010203',
    ],
    [
      'a custom rgba() fill',
      linux('running', 'fill: rgba(255, 0, 16, 0.5)'),
      true,
      '#ff0010',
    ],
    [
      'a short custom hex fill',
      linux('running', 'fill: #abc'),
      true,
      '#aabbcc',
    ],
    [
      'a custom hex fill in lower case',
      linux('running', 'fill:#4F8F00'),
      true,
      '#4f8f00',
    ],
    [
      'a custom hex fill without its alpha',
      linux('running', 'fill: #4f8f00ff'),
      true,
      '#4f8f00',
    ],
    [
      'the status color for a custom fill that is not a color',
      linux('running', 'fill: url(#linux)'),
      true,
      '#4f8f00',
    ],
  ])('exports %s', (_, node, running, want) => {
    expect(displayColor(node, running)).toBe(want);
  });
});
