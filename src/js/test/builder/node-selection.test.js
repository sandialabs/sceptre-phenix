// @vitest-environment node
// A selected node gets is-selected. styles.test.js checks the rules that
// draw it.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import DeviceNode from '@/components/builder/nodes/DeviceNode.vue';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';
import IconNode from '@/components/builder/nodes/IconNode.vue';
import NoteNode from '@/components/builder/nodes/NoteNode.vue';
import ShapeNode from '@/components/builder/nodes/ShapeNode.vue';
import SwitchNode from '@/components/builder/nodes/SwitchNode.vue';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { addNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

// Rendered alone, a node has no canvas handles or tooltip wrapper.
vi.mock('@vue-flow/core', async (importOriginal) => {
  const { ref } = await import('vue');

  return {
    ...(await importOriginal()),
    Handle: { name: 'Handle', render: () => null },
    useNode: () => ({ id: '', nodeEl: ref(null) }),
  };
});

const COMPONENTS = {
  device: DeviceNode,
  switch: SwitchNode,
  note: NoteNode,
  group: GroupNode,
  shape: ShapeNode,
  icon: IconNode,
};

// The node of a kind in the sample document, with a note, a group, a shape
// and an icon added, drawn alone.
async function render(kind, selected) {
  const sample = sampleDocument();
  let { doc } = sample;
  const nodes = { device: sample.alpha, switch: sample.sw };

  for (const added of ['note', 'group', 'shape', 'icon']) {
    const result = addNode(doc, {
      kind: added,
      position: { x: 400, y: 400 },
    });

    doc = result.doc;
    nodes[added] = result.node;
  }

  const id = nodes[kind].id;
  const flow = toFlowNodes(doc).find((node) => node.id === id);
  const app = createSSRApp({
    render: () => h(COMPONENTS[kind], { id, data: flow.data, selected }),
  });

  return renderToString(app);
}

// The classes of the node's own element.
function classesOf(html) {
  const node = html.match(/<div class="([^"]*\bbuilder-node\b[^"]*)"/);

  return node[1].split(/\s+/);
}

describe('a selected node on the canvas', () => {
  test.each(Object.keys(COMPONENTS))(
    'a %s has the is-selected class only while it is selected',
    async (kind) => {
      expect(classesOf(await render(kind, true))).toContain('is-selected');
      expect(classesOf(await render(kind, false))).not.toContain('is-selected');
    },
  );
});
