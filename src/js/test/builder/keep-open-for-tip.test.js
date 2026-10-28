// @vitest-environment happy-dom
// Escape with a dialog's tooltip shown hides the tooltip only: the dialog
// does not get the key (useKeepOpenForTip in fixedTooltip.js).

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';

import { useKeepOpenForTip } from '@/components/builder/fixedTooltip.js';

enableAutoUnmount(afterEach);

const state = { shown: false };

const Dialog = defineComponent({
  emits: ['escape'],
  setup(_, { emit }) {
    useKeepOpenForTip(() => state.shown);

    return () =>
      h('button', {
        onKeydown: (event) => event.key === 'Escape' && emit('escape'),
      });
  },
});

function press(wrapper, key) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
  });

  wrapper.get('button').element.dispatchEvent(event);

  return event;
}

describe('useKeepOpenForTip', () => {
  test('stops Escape while a tooltip shows, and lets it through after', () => {
    const wrapper = mount(Dialog, { attachTo: document.body });

    state.shown = true;
    expect(press(wrapper, 'Escape').defaultPrevented).toBe(true);
    expect(wrapper.emitted('escape')).toBeUndefined();

    expect(press(wrapper, 'Enter').defaultPrevented).toBe(false);

    state.shown = false;
    expect(press(wrapper, 'Escape').defaultPrevented).toBe(false);
    expect(wrapper.emitted('escape')).toHaveLength(1);
  });

  test('stops nothing once the dialog is gone', () => {
    const wrapper = mount(Dialog, { attachTo: document.body });
    const button = wrapper.get('button').element;

    state.shown = true;
    wrapper.unmount();
    document.body.append(button);

    const event = new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    });

    button.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    button.remove();
  });
});
