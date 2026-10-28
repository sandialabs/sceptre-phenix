// The new-interface handle ("+" below a device): an editable draft draws it.
// A read-only diagram and an included device do not.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import DeviceNode from '@/components/builder/nodes/DeviceNode.vue';

import {
  NEW_INTERFACE_HANDLE_ID,
  toFlowNodes,
} from '@/builder/adapters/vueflow.js';

import { sampleDocument } from './fixtures.js';

// A handle is drawn as an element that keeps its id, so the test can find
// it. The wrapper the tooltip listens on is the canvas's.
vi.mock('@vue-flow/core', async (importOriginal) => {
  const { h: make, ref } = await import('vue');

  return {
    ...(await importOriginal()),
    Handle: {
      name: 'Handle',
      inheritAttrs: false,
      props: { id: { type: String, default: '' }, type: String },
      render() {
        return make('i', { 'data-handle': this.id, 'data-type': this.type });
      },
    },
    useNode: () => ({ id: '', nodeEl: ref(null) }),
  };
});

async function render(options) {
  const sample = sampleDocument();
  const flow = toFlowNodes(sample.doc, options).find(
    (node) => node.id === sample.alpha.id,
  );
  const app = createSSRApp({
    render: () =>
      h(DeviceNode, { id: sample.alpha.id, data: flow.data, selected: false }),
  });

  return renderToString(app);
}

const NEW_HANDLE = `data-handle="${NEW_INTERFACE_HANDLE_ID}"`;

describe('the handle for a new interface', () => {
  test('toFlowNodes says whether the draft is read only', () => {
    const { doc } = sampleDocument();

    expect(toFlowNodes(doc).every((node) => !node.data.readOnly)).toBe(true);
    expect(
      toFlowNodes(doc, { readOnly: true }).every((node) => node.data.readOnly),
    ).toBe(true);
  });

  test('a device of an editable draft draws it', async () => {
    expect(await render()).toContain(NEW_HANDLE);
  });

  test('a device of a read-only draft does not, and keeps its interface handles', async () => {
    const html = await render({ readOnly: true });

    expect(html).not.toContain(NEW_HANDLE);
    expect(html).toMatch(/data-handle="[^"]+" data-type="source"/);
  });
});
