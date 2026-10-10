// A selected note, group, shape or icon shows its resize frame on the
// canvas of a draft the user can change, and then hides its selection
// check mark, which the frame's top right handle covers. A read-only
// draft has no frame, so the node keeps its check mark, as devices and
// switches always do. The frame itself, and where its handles are, need a
// browser, so they are left to the Playwright specs.

import { readFileSync } from 'node:fs';

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import { CANVAS_EDITING } from '@/components/builder/nodes/canvasEditing.js';
import DeviceNode from '@/components/builder/nodes/DeviceNode.vue';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';
import IconNode from '@/components/builder/nodes/IconNode.vue';
import NoteNode from '@/components/builder/nodes/NoteNode.vue';
import ShapeNode from '@/components/builder/nodes/ShapeNode.vue';
import SwitchNode from '@/components/builder/nodes/SwitchNode.vue';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { addNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// A device's and a switch's connection handles, and the wrapper their
// tooltip listens on, are the canvas's; rendered alone, the nodes have
// neither.
vi.mock('@vue-flow/core', async (importOriginal) => {
  const { ref } = await import('vue');

  return {
    ...(await importOriginal()),
    Handle: { name: 'Handle', render: () => null },
    useNode: () => ({ id: '', nodeEl: ref(null) }),
  };
});

// The sample document with a note, a group, a shape and an icon.
function drawnDocument() {
  const sample = sampleDocument();
  let { doc } = sample;
  const nodes = { device: sample.alpha, switch: sample.sw };

  for (const kind of ['note', 'group', 'shape', 'icon']) {
    const added = addNode(doc, { kind, position: { x: 400, y: 400 } });

    doc = added.doc;
    nodes[kind] = added.node;
  }

  return { doc, nodes };
}

const COMPONENTS = {
  device: DeviceNode,
  switch: SwitchNode,
  note: NoteNode,
  group: GroupNode,
  shape: ShapeNode,
  icon: IconNode,
};

// The node of a kind, drawn as the canvas draws it. With `canvas`, the
// node has the canvas's editing context, of a draft that is read-only or
// not; without it, the node is drawn alone.
async function render(kind, { selected, canvas = null }) {
  const { doc, nodes } = drawnDocument();
  const id = nodes[kind].id;
  const flow = toFlowNodes(doc).find((node) => node.id === id);
  const app = createSSRApp({
    render: () => h(COMPONENTS[kind], { id, data: flow.data, selected }),
  });

  if (canvas) {
    app.provide(CANVAS_EDITING, {
      store: { doc, readOnly: canvas.readOnly },
      flowNode: () => undefined,
      zoom: () => 1,
    });
  }

  return renderToString(app);
}

// The classes of the node's own element.
function classesOf(html) {
  const node = html.match(/<div class="([^"]*\bbuilder-node\b[^"]*)"/);

  return node[1].split(/\s+/);
}

describe('the selection check mark of a node with a resize frame', () => {
  test.each(['note', 'group', 'shape', 'icon'])(
    'a selected %s on the canvas of a draft the user can change shows the frame in place of the check mark',
    async (kind) => {
      const classes = classesOf(
        await render(kind, { selected: true, canvas: { readOnly: false } }),
      );

      expect(classes).toContain('is-selected');
      expect(classes).toContain('has-resize-frame');
    },
  );

  test.each(['note', 'group', 'shape', 'icon'])(
    'a %s keeps the check mark when it is not selected, read-only or drawn alone',
    async (kind) => {
      const states = [
        { selected: false, canvas: { readOnly: false } },
        { selected: true, canvas: { readOnly: true } },
        { selected: true },
      ];

      for (const state of states) {
        const classes = classesOf(await render(kind, state));

        expect(classes, JSON.stringify(state)).not.toContain(
          'has-resize-frame',
        );
      }

      expect(
        classesOf(
          await render(kind, { selected: true, canvas: { readOnly: true } }),
        ),
      ).toContain('is-selected');
    },
  );

  test.each(['device', 'switch'])(
    'a selected %s has no frame and keeps the check mark',
    async (kind) => {
      const classes = classesOf(
        await render(kind, { selected: true, canvas: { readOnly: false } }),
      );

      expect(classes).toContain('is-selected');
      expect(classes).not.toContain('has-resize-frame');
    },
  );

  test('the style sheet removes the check mark only from a node with the frame', () => {
    const css = readFileSync(
      new URL('../../src/builder/builder.css', import.meta.url),
      'utf8',
    ).replace(/\/\*[\s\S]*?\*\//g, '');
    const rules = [
      ...css.matchAll(/([^{}]*has-resize-frame[^{}]*)\{([^}]*)\}/g),
    ].map(([, selector, body]) => [selector.trim(), body.trim()]);

    expect(rules).toEqual([
      ['.builder-node.is-selected.has-resize-frame::after', 'content: none;'],
    ]);
  });
});
