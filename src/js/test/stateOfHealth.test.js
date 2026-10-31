// @vitest-environment happy-dom
// Calls the StateOfHealth methods with a stand-in for the component, so the
// test does not mount the D3 graph.
import * as d3 from 'd3';
import { afterEach, describe, expect, test, vi } from 'vitest';
import StateOfHealth from '@/views/StateOfHealth.vue';

vi.mock('@/utils/websocket', () => ({
  addWsHandler: vi.fn(),
  removeWsHandler: vi.fn(),
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));

const { methods } = StateOfHealth;

describe('StateOfHealth', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('a VM node is named with its status', () => {
    expect(
      methods.nodeLabel({ label: 'server-00', status: 'notrunning' }),
    ).toBe('server-00, status not running');
    expect(methods.nodeLabel({ label: 'plc', status: 'external' })).toBe(
      'plc, status external',
    );
  });

  test('a status broadcast updates only graph nodes, with their names', () => {
    document.body.innerHTML =
      '<svg id="legend"><circle /></svg><div id="graph"><svg></svg></div>';
    const nodes = [
      { label: 'server-00', image: 'Linux', status: 'notrunning' },
      { label: 'vlan', image: 'Switch', status: 'ignore' },
    ];
    d3.select('#graph svg')
      .selectAll('circle')
      .data(nodes)
      .join('circle')
      .attr('class', 'circle')
      .attr('aria-label', 'server-00, status running');
    const vm = {
      running: true,
      isVmNode: methods.isVmNode,
      nodeLabel: methods.nodeLabel,
    };
    // Vue binds methods; D3 calls the fill callback with the element as this.
    vm.updateNodeColor = methods.updateNodeColor.bind(vm);

    methods.refreshNodeStatus.call(vm);

    const [server, vlan] = document.querySelectorAll('#graph circle');
    expect(server.getAttribute('fill')).toBe('#ff6b5b');
    expect(server.getAttribute('aria-label')).toBe(
      'server-00, status not running',
    );
    expect(vlan.getAttribute('fill')).toBe('url(#switch)');
    expect(document.querySelector('#legend circle').hasAttribute('fill')).toBe(
      false,
    );
  });

  test('only VM nodes, not VLAN switches, take keyboard focus', () => {
    expect(methods.isVmNode({ image: 'Linux' })).toBe(true);
    expect(methods.isVmNode({ image: 'Switch' })).toBe(false);
    expect(methods.isVmNode({})).toBe(false);
  });

  test('a black "not booted" node gets a light ring', () => {
    const vm = { running: true, updateNodeColor: methods.updateNodeColor };

    expect(methods.updateNodeBorder.call(vm, { status: 'notboot' })).toBe(
      'whitesmoke',
    );
    expect(methods.updateNodeBorder.call(vm, { status: 'running' })).toBe(
      '#4F8F00',
    );
    expect(
      methods.updateNodeBorder.call(vm, {
        status: 'notboot',
        soh: { errors: ['x'] },
      }),
    ).toBe('#FF9900');
  });

  test('reduced motion skips the hover transition', () => {
    const selection = { transition: vi.fn(() => 'animated') };

    vi.stubGlobal('window', { matchMedia: () => ({ matches: true }) });
    expect(methods.transitionFor(selection)).toBe(selection);

    vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
    expect(methods.transitionFor(selection)).toBe('animated');
  });
});
