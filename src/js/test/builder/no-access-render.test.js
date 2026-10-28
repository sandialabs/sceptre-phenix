// The Builder view and header link for a role without builder use, rendered
// on the server: a notice in place of the drafts and the editor.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

const phenix = vi.hoisted(() => ({
  username: 'viewer',
  role: null,
  auth: true,
}));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => phenix,
}));
vi.mock('@/router', () => ({
  default: { replace: vi.fn() },
  pageTitle: (...parts) => [...parts.filter(Boolean), 'phēnix'].join(' - '),
}));
vi.mock('vue-router', () => ({
  useRoute: () => ({
    name: 'builder',
    fullPath: '/builder',
    query: {},
    meta: { title: 'Builder' },
  }),
  useRouter: () => ({ replace: vi.fn(), push: vi.fn(), resolve: vi.fn() }),
  onBeforeRouteLeave: () => {},
}));

import Builder from '@/views/Builder.vue';

// A role with these policies. roleAllowed remembers its answers by the
// role's name.
function roleWith(name, policies) {
  return { name, policies };
}

const GLOBAL_VIEWER = roleWith('Global Viewer', [
  { resources: ['*', '*/*'], resourceNames: ['*'], verbs: ['list', 'get'] },
]);
const BUILDER = roleWith('Builder', [
  { resources: ['builder'], resourceNames: [], verbs: ['use'] },
]);

async function render() {
  const app = createSSRApp({ render: () => h(Builder) });

  app.use(createPinia());

  return renderToString(app);
}

beforeEach(() => {
  // The view sets the page title as it is set up.
  vi.stubGlobal('document', { title: '' });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const READER = roleWith('Reader', [
  { resources: ['*'], resourceNames: ['*'], verbs: ['list', 'get'] },
]);

describe('the Builder of a role without the Builder permission', () => {
  test.each([
    ['Global Viewer', GLOBAL_VIEWER],
    ['a reader of every resource', READER],
  ])(
    'of %s is a notice under the page heading, with no drafts and no editor',
    async (_, role) => {
      phenix.role = role;

      const html = await render();
      const notice = html.match(
        /<section[^>]*data-testid="builder-no-access"[^>]*>[\s\S]*?<\/section>/,
      )?.[0];

      expect(notice).toBeTruthy();
      expect(notice).toContain('aria-labelledby="builder-no-access-title"');
      expect(notice).toMatch(
        /<h1 id="builder-no-access-title"[^>]*>Builder<\/h1>/,
      );
      expect(notice).toMatch(
        /<p[^>]*>Your role does not include the Builder\.<\/p>/,
      );
      expect(html).not.toContain('drafts-blank');
      expect(html).not.toContain('drafts-tab-');
      expect(html).not.toContain('builder-canvas');
    },
  );

  test('a role with builder use gets the drafts', async () => {
    phenix.role = BUILDER;

    const html = await render();

    expect(html).not.toContain('builder-no-access');
    expect(html).toContain('data-testid="drafts-blank"');
    expect(html).toContain('data-testid="drafts-tab-drafts"');
  });
});
