// A selected node gets is-selected: a border and inner ring in the selection
// color, which keeps 3:1 against its background in each theme.

import { readFileSync } from 'node:fs';

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
import { colorChannels, contrastRatio } from '@/builder/colors.js';
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

const CSS = readFileSync(
  new URL('../../src/builder/builder.css', import.meta.url),
  'utf8',
).replace(/\/\*[\s\S]*?\*\//g, '');

// The custom properties a rule block sets, by name.
function tokensOf(selector) {
  const start = CSS.indexOf(`${selector} {`);
  const body = CSS.slice(start, CSS.indexOf('}', start));

  return Object.fromEntries(
    [...body.matchAll(/(--bx-[\w-]+):\s*([^;]+);/g)].map(([, name, value]) => [
      name,
      value.trim(),
    ]),
  );
}

// The body of the rule with exactly this selector list, spaces collapsed.
function ruleBody(selector) {
  const rule = [...CSS.matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(
    ([, list]) => list.replace(/\s+/g, ' ').trim() === selector,
  );

  return rule?.[2].replace(/\s+/g, ' ').trim();
}

const LIGHT = tokensOf('.builder-root');
const DARK = {
  ...LIGHT,
  ...tokensOf(".builder-root[data-builder-theme='dark']"),
};

describe('a selected node on the canvas', () => {
  test.each(Object.keys(COMPONENTS))(
    'a %s has the is-selected class only while it is selected',
    async (kind) => {
      expect(classesOf(await render(kind, true))).toContain('is-selected');
      expect(classesOf(await render(kind, false))).not.toContain('is-selected');
    },
  );

  test('the style sheet draws no badge on a selected node', () => {
    expect(CSS).not.toMatch(/is-selected[^{}]*::after/);
    expect(CSS).not.toContain('has-resize-frame');
  });

  test.each([
    ['light', LIGHT],
    ['dark', DARK],
  ])(
    'in the %s theme, the selection color is the accent mixed with 10%% white and keeps 3:1',
    (theme, tokens) => {
      const accent = colorChannels(tokens['--bx-accent']);
      const ring = colorChannels(tokens['--bx-selected-ring']);

      accent.slice(0, 3).forEach((channel, index) => {
        expect(
          Math.abs(ring[index] - (channel * 0.9 + 255 * 0.1)),
          theme,
        ).toBeLessThanOrEqual(1);
      });

      for (const behind of [
        '--bx-bg',
        '--bx-grid',
        '--bx-surface',
        '--bx-bg-alt',
        '--bx-selected-bg',
      ]) {
        expect(
          contrastRatio(tokens['--bx-selected-ring'], tokens[behind]),
          `${theme} ${behind}`,
        ).toBeGreaterThanOrEqual(3);
      }
    },
  );

  test('the ring and the stroke of a selection are 10% wider than 2px and 4px', () => {
    expect(LIGHT['--bx-selected-ring-width']).toBe('2.2px');
    expect(LIGHT['--bx-selected-stroke']).toBe('4.4px');
  });

  test('every kind of node draws its selection in the selection color', () => {
    const ring =
      'inset 0 0 0 var(--bx-selected-ring-width) var(--bx-selected-ring)';

    expect(ruleBody('.builder-node.is-selected')).toContain(
      'border-color: var(--bx-selected-ring);',
    );
    expect(ruleBody('.builder-node.is-selected')).toContain(ring);
    expect(ruleBody('.builder-node.is-selected')).toContain(
      'background: var(--bx-selected-bg);',
    );
    // A shape and an icon remove the shadow of a node, so they draw the
    // ring again, without the shadow. They keep the selection fill.
    const drawing = ruleBody(
      '.builder-node--shape.is-selected, .builder-node--icon.is-selected',
    );

    expect(drawing).toContain('border-color: var(--bx-selected-ring);');
    expect(drawing).toContain(ring);
    expect(drawing).not.toContain('--bx-shadow');
    expect(drawing).not.toContain('background');
    expect(ruleBody('.builder-canvas .builder-edge.is-selected')).toBe(
      'stroke-width: var(--bx-selected-stroke);',
    );
  });

  test('in forced colors, a selected node has a Highlight border', () => {
    const forced = CSS.slice(CSS.indexOf('@media (forced-colors: active)'));

    expect(forced.replace(/\s+/g, ' ')).toContain(
      '.builder-node.is-selected { border-color: Highlight; }',
    );
  });
});
