// @vitest-environment happy-dom
import { afterEach, expect, test, vi } from 'vitest';

import AppFooter from '@/components/AppFooter.vue';
import { mountPage, tooltipPositions } from './helpers/mount.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

test('the documentation tooltip is light and opens to the right', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve({ json: () => Promise.resolve({}) })),
  );
  const wrapper = await mountPage(AppFooter);

  expect(tooltipPositions(wrapper)).toEqual(['is-right']);
  const tooltip = wrapper.findComponent({ name: 'BTooltip' });
  expect(tooltip.props('type')).toBe('is-light');
});
