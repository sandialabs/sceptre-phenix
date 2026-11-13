// A no-restricted-syntax rule in eslint.config.js stops number toast
// durations and is-danger toasts in src/.
import { defineComponent, h } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';

import { TOAST_DURATION } from '@/utils/toast.js';
import { mountComponent, toasts } from './helpers/mount.js';

afterEach(() => {
  vi.useRealTimers();
});

it('shows a toast without a duration of its own for TOAST_DURATION', async () => {
  const Page = defineComponent({ render: () => h('div') });
  const wrapper = await mountComponent(Page);
  vi.useFakeTimers();

  wrapper.vm.$buefy.toast.open({ message: 'Saved', type: 'is-success' });
  vi.advanceTimersByTime(TOAST_DURATION - 1);
  expect(toasts()).toEqual([{ text: 'Saved', type: 'is-success' }]);
  // and the fade out after it
  vi.advanceTimersByTime(1 + 150);
  expect(toasts()).toEqual([]);
});
