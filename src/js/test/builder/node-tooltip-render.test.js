// @vitest-environment happy-dom
// A device's or a switch's info tooltip, mounted: its rows of labels and
// values. Showing and placing it is left to e2e.

import { describe, expect, test } from 'vitest';
import { nextTick, ref } from 'vue';
import { mount } from '@vue/test-utils';

import BuilderNodeTooltip from '@/components/builder/BuilderNodeTooltip.vue';
import { deviceInfo } from '@/builder/nodeInfo.js';

function render(tip, setTipEl = () => {}) {
  const shown = ref(tip);

  return {
    shown,
    wrapper: mount(BuilderNodeTooltip, {
      props: { tooltip: { tip: shown, setTipEl } },
    }),
  };
}

const texts = (wrapper, selector) =>
  wrapper.findAll(selector).map((found) => found.text());

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

  test('renders nothing until a tooltip shows', () => {
    expect(render(null).wrapper.find('div').exists()).toBe(false);
  });

  test('is a fixed tooltip, hidden from assistive technology, where it was placed', () => {
    const root = render({ text: info, top: 120, left: 48 }).wrapper.find('div');

    expect(root.classes()).toEqual([
      'builder-tooltip',
      'builder-tooltip--fixed',
      'builder-tooltip--info',
    ]);
    expect(root.attributes('data-testid')).toBe('node-tooltip');
    expect(root.attributes('aria-hidden')).toBe('true');
    expect(root.element.style.top).toBe('120px');
    expect(root.element.style.left).toBe('48px');
  });

  test('lists each row as a label and its lines, as plain text', () => {
    const { wrapper } = render({ text: info, top: 0, left: 0 });
    const values = wrapper.findAll('dd.builder-info__value');

    expect(wrapper.find('dl.builder-info').exists()).toBe(true);
    expect(texts(wrapper, '.builder-info__label')).toEqual([
      'Description',
      'Interfaces',
      'OS type',
    ]);
    expect(texts(wrapper, '.builder-info__line')).toEqual([
      'Jump <host> & bastion',
      'eth0 — 10.0.0.5/24',
      'eth1 — DHCP',
      'linux',
    ]);
    // The description is text, never markup.
    expect(wrapper.find('host').exists()).toBe(false);
    // One value for each label, holding all its lines.
    expect(values).toHaveLength(3);
    expect(values[1].element.previousElementSibling.textContent).toBe(
      'Interfaces',
    );
    expect(texts(values[1], '.builder-info__line')).toEqual([
      'eth0 — 10.0.0.5/24',
      'eth1 — DHCP',
    ]);
  });

  test('hands its element to setTipEl, which places it, and follows the tip as it moves and hides', async () => {
    const elements = [];
    const { shown, wrapper } = render({ text: info, top: 0, left: 0 }, (el) =>
      elements.push(el),
    );

    expect(elements.at(-1)).toBe(wrapper.find('div').element);

    shown.value = { text: info, top: 30, left: 60 };
    await nextTick();
    expect(wrapper.find('div').element.style.top).toBe('30px');

    shown.value = null;
    await nextTick();
    expect(wrapper.find('div').exists()).toBe(false);
    expect(elements.at(-1)).toBeNull();
  });
});
