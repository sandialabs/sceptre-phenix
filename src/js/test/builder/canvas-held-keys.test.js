// @vitest-environment happy-dom
// A held Shift and arrow key (move) or Alt, Shift and arrow key (resize) on
// the canvas: the nodes follow each repeat at once, and the release makes
// the whole gesture one undo step, said once (holdNudge, holdResize and
// endKeyGesture in store.js).

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderCanvas from '@/components/builder/BuilderCanvas.vue';
import { findNode, sizeOf } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

let pinia;
let store;

beforeEach(() => {
  pinia = createPinia();
  store = useBuilderStore(pinia);
  store.newDocument({ name: 'Held keys' });
});

async function mountCanvas() {
  const wrapper = mount(BuilderCanvas, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await flushPromises();

  return wrapper.get('[data-testid="builder-canvas"]');
}

// Presses `key` and holds it: one keydown, then `repeats` more with
// repeat set, as a browser sends them.
async function hold(canvas, key, repeats, modifiers = { shiftKey: true }) {
  for (let press = 0; press <= repeats; press += 1) {
    await canvas.trigger('keydown', { key, ...modifiers, repeat: press > 0 });
  }
}

function placeOf(id) {
  return { ...findNode(store.doc, id).position };
}

describe('a held Shift and arrow key', () => {
  test('moves the nodes at once, and 60 repeats and the release make one undo step, said once', async () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const start = placeOf(node.id);
    const canvas = await mountCanvas();
    const steps = store.history.entries.length;
    const said = store.announcementSeq;

    await hold(canvas, 'ArrowRight', 59);

    expect(placeOf(node.id)).toEqual({ x: start.x + 600, y: start.y });
    expect(store.history.entries).toHaveLength(steps);
    expect(store.announcementSeq).toBe(said);

    await canvas.trigger('keyup', { key: 'ArrowRight', shiftKey: true });

    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.announcementSeq).toBe(said + 1);
    expect(store.announcement).toBe(
      `Moved alpha to x ${start.x + 600}, y ${start.y}`,
    );
    expect(placeOf(node.id)).toEqual({ x: start.x + 600, y: start.y });

    store.undo();
    expect(placeOf(node.id)).toEqual(start);
  });

  test('one press is one undo step, recorded when the key is released', async () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const start = placeOf(node.id);
    const canvas = await mountCanvas();
    const steps = store.history.entries.length;

    await hold(canvas, 'ArrowDown', 0);
    expect(placeOf(node.id)).toEqual({ x: start.x, y: start.y + 10 });
    expect(store.history.entries).toHaveLength(steps);

    await canvas.trigger('keyup', { key: 'ArrowDown', shiftKey: true });
    expect(store.history.entries).toHaveLength(steps + 1);
    await hold(canvas, 'ArrowDown', 0);
    // Shift released before the arrow key ends the move too.
    await canvas.trigger('keyup', { key: 'Shift' });

    expect(store.history.entries).toHaveLength(steps + 2);
    expect(placeOf(node.id)).toEqual({ x: start.x, y: start.y + 20 });
    expect(store.announcement).toBe(
      `Moved alpha to x ${start.x}, y ${start.y + 20}`,
    );
  });

  test('a move of several nodes in two directions says both', async () => {
    const one = store.addNode({ kind: 'device', hostname: 'alpha' });
    const two = store.addNode({ kind: 'device', hostname: 'beta' });

    store.select({ nodes: [one.id, two.id] });

    const canvas = await mountCanvas();
    const steps = store.history.entries.length;

    await hold(canvas, 'ArrowLeft', 2);
    await hold(canvas, 'ArrowUp', 0);
    await canvas.trigger('keyup', { key: 'ArrowUp', shiftKey: true });

    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.announcement).toBe('Moved 2 nodes left by 30 and up by 10');
  });

  test('focus leaving the canvas, the window losing focus, a pointer press or another key ends it', async () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const canvas = await mountCanvas();
    const steps = store.history.entries.length;

    await hold(canvas, 'ArrowRight', 3);
    await canvas.trigger('focusout');
    expect(store.history.entries).toHaveLength(steps + 1);

    await hold(canvas, 'ArrowRight', 3);
    window.dispatchEvent(new Event('blur'));
    expect(store.history.entries).toHaveLength(steps + 2);

    await hold(canvas, 'ArrowRight', 3);
    await canvas.trigger('pointerdown');
    expect(store.history.entries).toHaveLength(steps + 3);

    await hold(canvas, 'ArrowRight', 3);
    await canvas.trigger('keydown', { key: 'a' });
    expect(store.history.entries).toHaveLength(steps + 4);

    // A release after the end records nothing more.
    await canvas.trigger('keyup', { key: 'ArrowRight' });
    expect(store.history.entries).toHaveLength(steps + 4);
    expect(store.keyGesture).toBeNull();
    expect(findNode(store.doc, node.id)).toBeTruthy();
  });

  test('an undo while the key is held undoes the whole move', async () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const start = placeOf(node.id);
    const canvas = await mountCanvas();

    await hold(canvas, 'ArrowRight', 5);
    store.undo();

    expect(placeOf(node.id)).toEqual(start);
    expect(store.canRedo).toBe(true);

    // The release then records nothing.
    await canvas.trigger('keyup', { key: 'ArrowRight' });
    expect(placeOf(node.id)).toEqual(start);
    expect(store.canRedo).toBe(true);
  });

  test('a held key with nothing selected says so once', async () => {
    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.select({ nodes: [] });

    const canvas = await mountCanvas();
    const said = store.announcementSeq;

    await hold(canvas, 'ArrowRight', 10);

    expect(store.announcementSeq).toBe(said + 1);
    expect(store.announcement).toBe('Select the nodes to move first.');
  });
});

describe('a held Alt, Shift and arrow key', () => {
  test('resizes at once, and the release makes one undo step, said once', async () => {
    const note = store.addNode({ kind: 'note', text: 'Read me' });
    const size = sizeOf(findNode(store.doc, note.id));
    const canvas = await mountCanvas();
    const steps = store.history.entries.length;
    const said = store.announcementSeq;
    const modifiers = { shiftKey: true, altKey: true };

    await hold(canvas, 'ArrowRight', 19, modifiers);

    expect(sizeOf(findNode(store.doc, note.id)).width).toBe(size.width + 200);
    expect(store.history.entries).toHaveLength(steps);
    expect(store.announcementSeq).toBe(said);

    await canvas.trigger('keyup', { key: 'ArrowRight', ...modifiers });

    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.announcementSeq).toBe(said + 1);
    expect(store.announcement).toMatch(
      new RegExp(`^Resized .+ to ${size.width + 200} by ${size.height}$`),
    );
  });

  test('a move then a resize, while Shift is held, are two undo steps', async () => {
    const note = store.addNode({ kind: 'note', text: 'Read me' });
    const canvas = await mountCanvas();
    const steps = store.history.entries.length;

    await hold(canvas, 'ArrowRight', 3);
    await hold(canvas, 'ArrowDown', 3, { shiftKey: true, altKey: true });
    await canvas.trigger('keyup', { key: 'Alt' });

    expect(store.history.entries).toHaveLength(steps + 2);
    expect(findNode(store.doc, note.id)).toBeTruthy();
  });
});

describe('the store while a key is held', () => {
  test('a save answered meanwhile stamps the diagram, and the move goes on', () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const start = placeOf(node.id);
    const steps = store.history.entries.length;
    const stamp = { updatedBy: 'bob', updatedAt: '2026-10-26T10:00:00Z' };

    store.holdNudge(10, 0);
    store.stampEntry(store.history.currentEntry(), stamp);
    store.holdNudge(10, 0);

    expect(store.doc.metadata.updatedBy).toBe('bob');
    expect(store.endKeyGesture()).toBe(true);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(placeOf(node.id)).toEqual({ x: start.x + 20, y: start.y });
    expect(store.doc.metadata.updatedBy).toBe('bob');
  });

  test('another document opened meanwhile ends the move without a step', () => {
    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.holdNudge(10, 0);
    store.newDocument({ name: 'Other' });

    expect(store.endKeyGesture()).toBe(false);
    expect(store.history.entries).toHaveLength(1);
  });

  test('another edit meanwhile records the move first', () => {
    const node = store.addNode({ kind: 'device', hostname: 'alpha' });
    const start = placeOf(node.id);
    const steps = store.history.entries.length;

    store.holdNudge(0, 10);
    store.addNode({ kind: 'device', hostname: 'beta' });

    expect(store.history.entries).toHaveLength(steps + 2);
    expect(store.history.entries.at(-2).label).toBe(
      `Moved alpha to x ${start.x}, y ${start.y + 10}`,
    );
    expect(placeOf(node.id)).toEqual({ x: start.x, y: start.y + 10 });
    expect(store.keyGesture).toBeNull();
  });

  test('a move back to where it started records nothing', () => {
    store.addNode({ kind: 'device', hostname: 'alpha' });
    const steps = store.history.entries.length;

    store.holdNudge(10, 0);
    store.holdNudge(-10, 0);

    expect(store.endKeyGesture()).toBe(false);
    expect(store.history.entries).toHaveLength(steps);
  });

  test('a read-only draft refuses the move at once, as before', () => {
    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.readOnly = true;
    const doc = store.doc;

    store.holdNudge(10, 0);

    expect(store.doc).toBe(doc);
    expect(store.keyGesture).toBeNull();
    expect(store.error).toMatch(/read only/i);
  });
});
