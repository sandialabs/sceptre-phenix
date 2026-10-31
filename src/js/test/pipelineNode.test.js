// @vitest-environment happy-dom
import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import PipelineNode from '@/components/pipeline/PipelineNode.vue';

enableAutoUnmount(afterEach);

function mountNode(status) {
  return mount(PipelineNode, {
    props: { index: 2, status, label: 'scan', x: 0, y: 0, node: { id: 2 } },
  });
}

describe('PipelineNode', () => {
  test('a SCORCH step is a named button that Enter and Space open', async () => {
    const wrapper = mountNode('success');
    const root = wrapper.find('g');

    expect(root.attributes('tabindex')).toBe('0');
    expect(root.attributes('role')).toBe('button');
    expect(root.attributes('aria-label')).toBe('scan, status success');

    await root.trigger('keydown', { key: 'Enter' });
    await root.trigger('keydown', { key: ' ' });

    expect(wrapper.emitted('click')).toEqual([
      [2, { id: 2 }],
      [2, { id: 2 }],
    ]);
  });

  // happy-dom does not track :focus-visible, so each test sets its result.
  async function focusWith(focusVisible) {
    const wrapper = mountNode('running');
    const root = wrapper.find('g');
    const original = root.element.matches.bind(root.element);
    root.element.matches = vi.fn((selector) =>
      selector === ':focus-visible' ? focusVisible() : original(selector),
    );

    expect(wrapper.find('.pipeline-selection-highlight').exists()).toBe(false);
    await root.trigger('focus');
    expect(root.element.matches).toHaveBeenCalledWith(':focus-visible');
    return wrapper.find('.pipeline-selection-highlight').exists();
  }

  test('keyboard focus shows the selection ring', async () => {
    expect(await focusWith(() => true)).toBe(true);
  });

  test('focus from a mouse click does not show the selection ring', async () => {
    expect(await focusWith(() => false)).toBe(false);
  });

  test('focus shows the ring when the browser has no :focus-visible', async () => {
    const ring = await focusWith(() => {
      throw new SyntaxError('unknown pseudo-class');
    });
    expect(ring).toBe(true);
  });

  test('the start and end markers are not in the tab order', () => {
    for (const status of ['start', 'end']) {
      const root = mountNode(status).find('g');

      expect(root.attributes('tabindex')).toBeUndefined();
      expect(root.attributes('role')).toBeUndefined();
    }
  });
});
