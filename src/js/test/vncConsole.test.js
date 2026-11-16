// The console component: one RFB for each connection value, the settings it
// gives RFB, the RFB events it passes on, and what it lets a parent send.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
const load = vi.hoisted(() => ({ fails: false }));
vi.mock('@/vnc/loadRfb.js', () => ({
  loadRfb: async () => {
    if (load.fails) throw new TypeError('Failed to fetch');
    return FakeRfb;
  },
}));

import VncConsole from '@/vnc/VncConsole.vue';
import { mountComponent } from './helpers/mount.js';

const connection = (attempt = 1) => ({
  url: `ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws?token=t&n=${attempt}`,
  attempt,
});

async function mountConsole(props = {}) {
  const wrapper = await mountComponent(VncConsole, {
    props: {
      connection: connection(),
      label: 'Console of VM web in experiment demo',
      ...props,
    },
    attachTo: document.body,
  });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  resetRfbs();
  load.fails = false;
});

afterEach(() => vi.restoreAllMocks());

describe('the RFB connection', () => {
  it('connects one RFB to the URL, shared, in its own element', async () => {
    const wrapper = await mountConsole();
    expect(rfbs).toHaveLength(1);
    expect(rfbs[0].url).toBe(connection().url);
    expect(rfbs[0].options).toEqual({ shared: true });
    expect(wrapper.element.contains(rfbs[0].target)).toBe(true);
  });

  it('connects a new RFB for each new connection, and drops the old one first', async () => {
    const wrapper = await mountConsole();
    const first = rfbs[0];
    await wrapper.setProps({ connection: connection(2) });
    await flushPromises();
    expect(first.disconnected).toBe(true);
    expect(rfbs).toHaveLength(2);
    expect(rfbs[1].url).toBe(connection(2).url);
    // the old RFB's own disconnect event does not reach the parent
    expect(wrapper.emitted('disconnect')).toBeUndefined();
  });

  it('disconnects when the connection becomes null, and on unmount', async () => {
    const wrapper = await mountConsole();
    await wrapper.setProps({ connection: null });
    expect(rfbs[0].disconnected).toBe(true);
    await wrapper.setProps({ connection: connection(2) });
    await flushPromises();
    wrapper.unmount();
    expect(rfbs[1].disconnected).toBe(true);
  });

  it('connects nothing without a connection', async () => {
    await mountConsole({ connection: null });
    expect(rfbs).toHaveLength(0);
  });

  it('reports a failed load of noVNC as a disconnect', async () => {
    load.fails = true;
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const wrapper = await mountConsole();
    expect(wrapper.emitted('disconnect')).toEqual([[{ clean: false }]]);
  });

  it('passes on the RFB events', async () => {
    const wrapper = await mountConsole();
    const rfb = rfbs[0];
    rfb.connect();
    rfb.emit('clipboard', { text: 'hello' });
    rfb.emit('bell');
    rfb.emit('desktopname', { name: 'web:0' });
    rfb.emit('securityfailure', { status: 1, reason: 'no auth' });
    rfb.drop();
    expect(wrapper.emitted('connect')).toHaveLength(1);
    expect(wrapper.emitted('clipboard')).toEqual([['hello']]);
    expect(wrapper.emitted('bell')).toHaveLength(1);
    expect(wrapper.emitted('desktopname')).toEqual([['web:0']]);
    expect(wrapper.emitted('securityfailure')).toEqual([['no auth']]);
    expect(wrapper.emitted('disconnect')).toEqual([[{ clean: false }]]);
  });
});

describe('the RFB settings', () => {
  it('starts with Fit, quality 6, compression 2 and input on', async () => {
    await mountConsole();
    expect(rfbs[0]).toMatchObject({
      viewOnly: false,
      scaleViewport: true,
      clipViewport: false,
      resizeSession: false,
      qualityLevel: 6,
      compressionLevel: 2,
    });
  });

  it.each([
    ['fit', { scaleViewport: true, resizeSession: false }],
    ['actual', { scaleViewport: false, resizeSession: false }],
    ['remote', { scaleViewport: false, resizeSession: true }],
  ])('maps the scaling %s', async (scaling, expected) => {
    await mountConsole({ scaling });
    expect(rfbs[0]).toMatchObject({ clipViewport: false, ...expected });
  });

  it('changes the settings of the open RFB', async () => {
    const wrapper = await mountConsole();
    await wrapper.setProps({
      viewOnly: true,
      scaling: 'actual',
      quality: 3,
      compression: 6,
    });
    expect(rfbs).toHaveLength(1);
    expect(rfbs[0]).toMatchObject({
      viewOnly: true,
      scaleViewport: false,
      qualityLevel: 3,
      compressionLevel: 6,
    });
  });

  it('opens a tile as view only and not shared exclusively', async () => {
    await mountConsole({ viewOnly: true, quality: 3, compression: 6 });
    expect(rfbs[0]).toMatchObject({
      viewOnly: true,
      qualityLevel: 3,
      compressionLevel: 6,
    });
    expect(rfbs[0].options.shared).toBe(true);
  });
});

describe('what a parent can do', () => {
  it('names the console for screen readers', async () => {
    const wrapper = await mountConsole();
    const root = wrapper.find('.vnc-console');
    expect(root.attributes('role')).toBe('application');
    expect(root.attributes('aria-label')).toBe(
      'Console of VM web in experiment demo',
    );
  });

  it('sends keys, Ctrl+Alt+Del and pasted text', async () => {
    const wrapper = await mountConsole();
    wrapper.vm.sendKeys([
      [0xff1b, 'Escape', true],
      [0xff1b, 'Escape', false],
    ]);
    wrapper.vm.sendCtrlAltDel();
    wrapper.vm.paste('ls -l\n');
    expect(rfbs[0].keys).toEqual([
      [0xff1b, 'Escape', true],
      [0xff1b, 'Escape', false],
    ]);
    expect(rfbs[0].ctrlAltDel).toBe(1);
    expect(rfbs[0].pasted).toEqual(['ls -l\n']);
  });

  it('gives the screen as a PNG blob, and its size', async () => {
    const wrapper = await mountConsole();
    const blob = await wrapper.vm.toBlob();
    expect(blob.type).toBe('image/png');
    expect(wrapper.vm.screenSize()).toMatchObject({
      width: 1024,
      height: 768,
    });
  });

  it('focuses the RFB canvas, also from the console element', async () => {
    const wrapper = await mountConsole();
    wrapper.vm.focus();
    expect(document.activeElement).toBe(rfbs[0].canvas);
    rfbs[0].canvas.blur();
    wrapper.find('.vnc-console').element.focus();
    expect(document.activeElement).toBe(rfbs[0].canvas);
  });

  it('does nothing without an RFB', async () => {
    const wrapper = await mountConsole({ connection: null });
    expect(() => {
      wrapper.vm.sendKeys([[0xff1b, 'Escape', true]]);
      wrapper.vm.sendCtrlAltDel();
      wrapper.vm.paste('x');
      wrapper.vm.focus();
    }).not.toThrow();
    expect(await wrapper.vm.toBlob()).toBeNull();
    expect(wrapper.vm.screenSize()).toEqual({
      width: 0,
      height: 0,
      scale: null,
    });
  });
});
