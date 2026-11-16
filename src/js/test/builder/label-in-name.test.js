// Each canvas node's accessible name holds the text the node shows, in one
// run and in its order (WCAG 2.5.3 Label in Name), as axe-core checks it.

import { describe, expect, test, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { h } from 'vue';

import DeviceNode from '@/components/builder/nodes/DeviceNode.vue';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';
import IconNode from '@/components/builder/nodes/IconNode.vue';
import LineNode from '@/components/builder/nodes/LineNode.vue';
import NoteNode from '@/components/builder/nodes/NoteNode.vue';
import ShapeNode from '@/components/builder/nodes/ShapeNode.vue';
import SwitchNode from '@/components/builder/nodes/SwitchNode.vue';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { addNode, findNode, groupNodes, updateNode } from '@/builder/model.js';

import { holdsShownText, sampleDocument, shownText } from './fixtures.js';

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
  line: LineNode,
};

const LONG = `Firewall rules pending review.\n\n${'Check each port. '.repeat(8)}`;

// A diagram with a node of each kind and the texts that change a name: a
// type, a comment (with words in parentheses), an included device, a group
// and its description, and notes with and without a heading or text.
function diagram() {
  const sample = sampleDocument();
  const { spec } = findNode(sample.doc, sample.alpha.id).device;
  let doc = updateNode(sample.doc, sample.alpha.id, {
    device: {
      iconKey: 'windows',
      spec: {
        ...spec,
        type: 'Router',
        general: { ...spec.general, description: 'Front end (web) server' },
      },
    },
  });
  const ids = {};
  const add = (key, options) => {
    const added = addNode(doc, { position: { x: 600, y: 600 }, ...options });

    doc = added.doc;
    ids[key] = added.node.id;
  };

  add('plain', { kind: 'device', hostname: 'device', spec: { general: {} } });
  add('hidden', {
    kind: 'device',
    hostname: 'quiet',
    hideDescription: true,
    spec: { type: 'VirtualMachine', general: { description: 'Not drawn' } },
  });
  add('blank-note', { kind: 'note' });
  add('note', { kind: 'note', text: LONG });
  add('headed-note', { kind: 'note', label: 'Plan', text: 'Plan the move' });
  add('rectangle', { kind: 'shape', shape: 'rectangle', label: 'Zone A' });
  add('circle', { kind: 'shape', shape: 'circle' });
  add('icon', { kind: 'icon', label: 'HQ' });
  add('line', { kind: 'line', label: 'uplink' });
  doc = updateNode(doc, ids.line, {
    line: { startArrow: true, endArrow: true },
  });
  add('group', { kind: 'group' });

  const grouped = groupNodes(doc, [sample.alpha.id], { title: 'DMZ' });

  doc = updateNode(grouped.doc, grouped.group.id, {
    group: { description: 'Public servers (edge)' },
  });
  doc = {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === sample.bravo.id
        ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
        : node,
    ),
  };

  return {
    doc,
    ids: {
      ...ids,
      device: sample.alpha.id,
      included: sample.bravo.id,
      switch: sample.sw.id,
      dmz: grouped.group.id,
    },
  };
}

// The text a node shows inside Vue Flow's wrapper, which carries the name.
function shown(flow) {
  const wrapper = mount({
    render: () =>
      h('div', [
        h(COMPONENTS[flow.data.node.kind], {
          id: flow.id,
          data: flow.data,
          selected: false,
        }),
      ]),
  });
  const text = shownText(wrapper.element).replace(/\s+/g, ' ').trim();

  wrapper.unmount();

  return text;
}

describe('a canvas node’s name holds the text it shows', () => {
  const { doc, ids } = diagram();
  const flows = toFlowNodes(doc);

  test.each(Object.keys(ids))('%s', (key) => {
    const flow = flows.find((node) => node.id === ids[key]);
    const text = shown(flow);

    expect(
      holdsShownText(flow.ariaLabel, text),
      `"${text}" in "${flow.ariaLabel}"`,
    ).toBe(true);
  });

  test('each node but an unlabeled shape shows text, so the check is not empty', () => {
    const empty = flows.filter((flow) => !shown(flow));

    expect(empty.map((flow) => flow.id)).toEqual([ids.circle]);
  });

  test('a description that the setting hides stays in the name', () => {
    const hidden = toFlowNodes(doc, { showDescriptions: false });
    const device = hidden.find((flow) => flow.id === ids.device);

    expect(shown(device)).not.toContain('Front end');
    expect(device.ariaLabel).toContain('Front end (web) server');
    expect(holdsShownText(device.ariaLabel, shown(device))).toBe(true);
  });
});

describe('the word check', () => {
  test.each([
    ['Device web-01, Server, 2 interfaces', 'web-01 Server 2 interfaces', true],
    ['Device web-01, 1 connection', 'web-01 Server', false],
    ['Add to server: icon-01', 'Add to server', true],
    ['Add icon-01 to the server', 'Add to server', false],
    ['Server, 3 templates', 'Server (3)', true],
    ['Rectangle', '', true],
  ])('"%s" holds "%s": %s', (name, text, holds) => {
    expect(holdsShownText(name, text)).toBe(holds);
  });
});
