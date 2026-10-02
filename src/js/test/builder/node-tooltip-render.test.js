// The info tooltip of a device or a switch, rendered on the server: its
// rows as a list of labels and values. Showing and placing it needs a
// browser, so that is left to the Playwright specs.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderNodeTooltip from '@/components/builder/BuilderNodeTooltip.vue';
import { deviceInfo } from '@/builder/nodeInfo.js';

function render(tip) {
  return renderToString(
    createSSRApp({
      render: () =>
        h(BuilderNodeTooltip, {
          tooltip: { tip: ref(tip), setTipEl: () => {} },
        }),
    }),
  );
}

// The text of each element with the class, in order.
function texts(html, name) {
  return [
    ...html.matchAll(
      new RegExp(`<[a-z]+ class="${name}"[^>]*>([^<]*)</[a-z]+>`, 'g'),
    ),
  ].map((match) => match[1].trim());
}

describe('the node info tooltip', () => {
  const info = deviceInfo({
    kind: 'device',
    device: {
      spec: {
        general: { description: 'Jump <host> & bastion' },
        hardware: { os_type: 'linux' },
        network: {
          interfaces: [
            { name: 'eth0', address: '10.0.0.5', mask: 24 },
            { name: 'eth1', proto: 'dhcp' },
          ],
        },
      },
    },
  });

  test('renders nothing until a tooltip shows', async () => {
    expect(await render(null)).toBe('<!---->');
  });

  test('is a fixed tooltip, hidden from assistive technology, where it was placed', async () => {
    const html = await render({ text: info, top: 120, left: 48 });
    const root = html.match(/^<div[^>]*>/)[0];

    expect(root).toContain(
      'class="builder-tooltip builder-tooltip--fixed builder-tooltip--info"',
    );
    expect(root).toContain('data-testid="node-tooltip"');
    expect(root).toContain('aria-hidden="true"');
    expect(root).toContain('style="top:120px;left:48px;"');
  });

  test('lists each row as a label and its lines, as plain text', async () => {
    const html = await render({ text: info, top: 0, left: 0 });

    expect(html).toContain('<dl class="builder-info">');
    expect(texts(html, 'builder-info__label')).toEqual([
      'Description',
      'Interfaces',
      'OS type',
    ]);
    expect(texts(html, 'builder-info__line')).toEqual([
      'Jump &lt;host&gt; &amp; bastion',
      'eth0 — 10.0.0.5/24',
      'eth1 — DHCP',
      'linux',
    ]);
    // One value for each label, holding all its lines.
    expect(html.match(/<dd class="builder-info__value">/g)).toHaveLength(3);
    expect(html).toMatch(
      /Interfaces<\/dt><dd class="builder-info__value">(?:<!--\[-->)?<span class="builder-info__line">eth0 — 10\.0\.0\.5\/24<\/span><span class="builder-info__line">eth1 — DHCP<\/span>/,
    );
  });
});
