// @vitest-environment happy-dom
import { afterEach, expect, test, vi } from 'vitest';

import AppFooter from '@/components/AppFooter.vue';
import { mountPage, textOf } from './helpers/mount.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

test('shows the copyright and the server version, with no links: Docs is in the header', async () => {
  const version = {
    commit: 'abc1234',
    tag: 'v1.2.3',
    buildDate: '2026-10-01',
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve({ json: () => Promise.resolve(version) })),
  );
  const wrapper = await mountPage(AppFooter);

  expect(wrapper.findAll('a')).toHaveLength(0);
  expect(wrapper.find('nav').exists()).toBe(false);
  expect(textOf(wrapper)).toContain('Sandia National Laboratories');
  expect(textOf(wrapper.find('.app-footer__end'))).toBe(
    'Version v1.2.3 (commit abc1234, built on 2026-10-01)',
  );
});
