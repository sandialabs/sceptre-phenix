// A switch on the canvas: its icon, its network's name, its outline color,
// and an accessible name with its network and VLAN alias.

import { readFileSync } from 'node:fs';

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import SwitchNode from '@/components/builder/nodes/SwitchNode.vue';
import { useNodeColors } from '@/components/builder/nodes/nodeColors.js';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { contrastRatio, iconInk, needsCasing } from '@/builder/colors.js';
import { nodeAccessibleName } from '@/builder/diagramText.js';
import { updateNetwork, updateNode } from '@/builder/model.js';

import { sampleDocument, tags, textOf } from './fixtures.js';

// The switch's bus handles and the wrapper its tooltip listens on are the
// canvas's. Rendered alone, the switch has neither.
vi.mock('@vue-flow/core', async (importOriginal) => {
  const { ref } = await import('vue');

  return {
    ...(await importOriginal()),
    Handle: { name: 'Handle', render: () => null },
    useNode: () => ({ id: '', nodeEl: ref(null) }),
  };
});

async function renderSwitch(doc, id) {
  const flow = toFlowNodes(doc).find((node) => node.id === id);
  const html = await renderToString(
    createSSRApp({ render: () => h(SwitchNode, { id, data: flow.data }) }),
  );

  return {
    html,
    box: tags(html, 'div').find((tag) => tag.includes('builder-node ')),
  };
}

// The text the switch's box shows, without its hidden description.
function shownText(html) {
  return textOf(
    html.replace(
      /<span[^>]*class="builder-node__info"[^>]*>[\s\S]*?<\/span>/,
      '',
    ),
  );
}

describe('a switch on the canvas', () => {
  test('shows its name only: no kind, no network line and no swatch', async () => {
    const { doc, sw } = sampleDocument();
    const { html } = await renderSwitch(doc, sw.id);

    expect(shownText(html)).toBe('EXP');
    expect(html).not.toContain('builder-node__kind');
    expect(html).not.toContain('builder-node__meta');
    expect(html).not.toContain('builder-node__swatch');
    expect(html).not.toMatch(/Network EXP/);

    // Screen readers still hear what it is, its network and VLAN alias.
    const name = nodeAccessibleName(
      doc,
      doc.nodes.find((n) => n.id === sw.id),
    );

    expect(name).toMatch(/^Switch EXP\b/);
    expect(name).toContain('network EXP');
    expect(name).toContain('VLAN alias 100');
  });

  test('is outlined in its network’s token, as its connections are drawn', async () => {
    const { doc, sw } = sampleDocument();
    const { box } = await renderSwitch(doc, sw.id);

    expect(box).toContain('builder-node--outlined');
    expect(box).toMatch(/--bx-node-outline:var\(--bx-net-0\)/);
    // A token keeps 3:1 against the canvas: no ring.
    expect(box).not.toContain('is-low-light');
    expect(box).not.toContain('is-low-dark');
  });

  test('is outlined in a network color the user chose, with a ring where it fades', async () => {
    const { doc, sw } = sampleDocument();
    const network = doc.networks[0].id;
    const pale = updateNetwork(doc, network, { color: '#eeeeee' });
    const { box } = await renderSwitch(pale, sw.id);

    expect(needsCasing('#eeeeee')).toEqual({ light: true, dark: false });
    expect(box).toMatch(/--bx-node-outline:#eeeeee/);
    expect(box).toContain('is-low-light');
    expect(box).not.toContain('is-low-dark');
  });

  test('keeps an outline color of its own', async () => {
    const { doc, sw } = sampleDocument();
    const own = updateNode(doc, sw.id, { switch: { outlineColor: '#7b3fa0' } });
    const { box } = await renderSwitch(own, sw.id);

    expect(box).toMatch(/--bx-node-outline:#7b3fa0/);
    expect(box).not.toContain('--bx-net-');
  });
});

describe('a node’s default outline', () => {
  test('applies only without an outline of its own', () => {
    let payload = {};
    const fallback = { color: 'var(--bx-net-2)', low: null };
    const { colorClasses, colorStyle } = useNodeColors(
      () => payload,
      () => ({ defaultOutline: fallback }),
    );

    expect(colorStyle.value).toEqual({
      '--bx-node-outline': 'var(--bx-net-2)',
    });
    expect(colorClasses.value['builder-node--outlined']).toBe(true);

    payload = { outlineColor: '#1a212b' };
    // A fresh computed: the payload is a plain object here.
    const own = useNodeColors(
      () => payload,
      () => ({ defaultOutline: fallback }),
    );

    expect(own.colorStyle.value).toEqual({ '--bx-node-outline': '#1a212b' });
    expect(own.colorClasses.value['is-low-dark']).toBe(true);

    const none = useNodeColors(
      () => ({}),
      () => ({ defaultOutline: null }),
    );

    expect(none.colorStyle.value).toBeUndefined();
  });

  test('gives way to the layer color, which gives way to its own', () => {
    const fallback = { color: 'var(--bx-net-2)', low: null };
    const layered = useNodeColors(
      () => ({}),
      () => ({ layerColor: '#db2777', defaultOutline: fallback }),
    );

    expect(layered.colorStyle.value).toEqual({
      '--bx-node-outline': '#db2777',
    });

    const own = useNodeColors(
      () => ({ outlineColor: '#1a212b' }),
      () => ({ layerColor: '#db2777', defaultOutline: fallback }),
    );

    expect(own.colorStyle.value).toEqual({ '--bx-node-outline': '#1a212b' });
  });
});

// The icon is in the outline color, apart from the text, and keeps 3:1
// against what it is drawn over in each theme, or takes the text color.
describe('a node’s icon color', () => {
  const css = readFileSync(
    new URL('../../src/builder/builder.css', import.meta.url),
    'utf8',
  );
  // Each theme's value of a token: the light theme's first.
  const token = (name) =>
    [...css.matchAll(new RegExp(`${name}: (#[0-9a-f]{6});`, 'gi'))].map(
      (match) => match[1],
    );
  const [surface, selected, canvas, grid, text] = [
    '--bx-surface',
    '--bx-selected-bg',
    '--bx-bg-alt',
    '--bx-grid',
    '--bx-text',
  ].map(token);

  test('is the theme’s accent color without an outline, 3:1 on every node ground', () => {
    const icon = token('--bx-accent');

    expect(icon).toHaveLength(2);
    [0, 1].forEach((theme) => {
      for (const ground of [surface, selected, canvas, grid]) {
        expect(contrastRatio(icon[theme], ground[theme])).toBeGreaterThan(3);
      }
      // Apart from the text: not the text's color.
      expect(icon[theme]).not.toBe(text[theme]);
    });
    expect(iconInk('')).toEqual({ light: false, dark: false });
    expect(iconInk('var(--bx-net-3)')).toEqual({ light: false, dark: false });
  });

  test('is the text color where the outline fades, or on a fill it does not keep 3:1 on', () => {
    // A pale outline fades on the light theme's nodes, a dark one on the
    // dark theme's.
    expect(iconInk('#eeeeee')).toEqual({ light: true, dark: false });
    expect(iconInk('#1a212b')).toEqual({ light: false, dark: true });
    // A dark red keeps 3:1 on the light theme's nodes only.
    expect(iconInk('#a3273f')).toEqual({ light: false, dark: true });
    expect(iconInk('#7fb2f0')).toEqual({ light: true, dark: false });
    // A named color cannot be read: the text color is safe.
    expect(iconInk('red')).toEqual({ light: true, dark: true });
    // On a fill, against the fill alone. A theme token's contrast with a
    // fill is not known.
    expect(iconInk('#ffffff', '#1b2330')).toEqual({
      light: false,
      dark: false,
    });
    expect(iconInk('#a3273f', '#6b6f18')).toEqual({ light: true, dark: true });
    expect(iconInk('', '#ffd400')).toEqual({ light: true, dark: true });
    expect(iconInk('var(--bx-net-0)', '#ffd400')).toEqual({
      light: true,
      dark: true,
    });
  });

  test('the style sheet draws it in the outline, else the accent color, else the ink', () => {
    const flat = css.replace(/\s+/g, ' ');

    expect(flat).toContain(
      ':is(.builder-node--device, .builder-node--switch, .builder-node--group) > .builder-node__header > .builder-icon { color: var(--bx-node-outline, var(--bx-accent)); }',
    );
    expect(flat).toMatch(
      /\.builder-root:not\(\[data-builder-theme='dark'\]\) \.builder-node\.is-icon-ink-light > \.builder-node__header > \.builder-icon, \.builder-root\[data-builder-theme='dark'\] \.builder-node\.is-icon-ink-dark > \.builder-node__header > \.builder-icon \{ color: inherit; \}/,
    );
  });

  test('a switch whose network color fades takes the text color for its icon', async () => {
    const { doc, sw } = sampleDocument();
    const pale = updateNetwork(doc, doc.networks[0].id, { color: '#eeeeee' });
    const { box } = await renderSwitch(pale, sw.id);

    expect(box).toContain('is-icon-ink-light');
    expect(box).not.toContain('is-icon-ink-dark');
  });
});
