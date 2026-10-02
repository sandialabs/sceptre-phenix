// The Publish dialog as a draft's card on the drafts page opens it
// (landing), rendered on the server: its title, Open draft, and the hint
// that goes with the diagram's errors. What a press does needs a browser,
// and is in builder-drafts.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
import { connect } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';

// The sample diagram with every interface connected, which publishes; or as
// it is, with an interface that has no VLAN, which Publish refuses.
function diagram({ publishable }) {
  const { doc, sw, bravo } = sampleDocument();

  return publishable
    ? connect(doc, {
        sourceNodeId: bravo.id,
        sourceHandleId: bravo.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc
    : doc;
}

async function render(props = {}, { publishable = true } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(PublishDialog, props) });

  app.use(pinia);
  useBuilderStore(pinia).doc = diagram({ publishable });

  return renderToString(app);
}

// The texts of the buttons of the form's last row, in order.
function actions(html) {
  const row = html.slice(html.lastIndexOf('builder-dialog__actions'));

  return [...row.matchAll(/<button\b[^>]*>([\s\S]*?)<\/button>/g)].map(
    ([, text]) => text.replace(/<[^>]+>/g, '').trim(),
  );
}

describe('the Publish dialog on the drafts page', () => {
  test('is named for the draft, and offers Open draft before Cancel', async () => {
    const html = await render({ landing: true, name: 'Network lab' });
    const [open] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="publish-open-draft"'),
    );

    expect(html).toMatch(
      /<h2 id="publish-dialog-title"[^>]*>Publish Network lab<\/h2>/,
    );
    expect(open).toContain('type="button"');
    expect(open).not.toContain('aria-disabled');
    expect(actions(html)).toEqual(['Open draft', 'Cancel', 'Create topology']);
    // The diagram has no error, so nothing sends the user to the editor.
    expect(html).not.toContain('publish-open-hint');
  });

  test('says where the diagram’s errors are fixed, and cannot be sent', async () => {
    const html = await render(
      { landing: true, name: 'Network lab' },
      { publishable: false },
    );
    const [submit] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="publish-submit"'),
    );

    expect(html).toMatch(/<strong[^>]*>\s*Error:\s*<\/strong>/);
    expect(html).toMatch(
      /<p[^>]*data-testid="publish-open-hint"[^>]*>\s*Open the draft to fix the errors, then publish\.\s*<\/p>/,
    );
    // The hint follows the list of what is wrong.
    expect(html.indexOf('publish-open-hint')).toBeGreaterThan(
      html.indexOf('builder-issues'),
    );
    expect(submit).toMatch(/\sdisabled\b/);
    expect(html).toContain('publish-open-draft');
  });

  test('without a name, keeps the editor’s title', async () => {
    const html = await render({ landing: true });

    expect(html).toContain('>Publish diagram</h2>');
    expect(html).toContain('publish-open-draft');
  });
});

describe('the Publish dialog in the editor', () => {
  test('has the editor’s title and no way to the editor', async () => {
    const html = await render({ name: 'Network lab' });

    expect(html).toContain('>Publish diagram</h2>');
    expect(html).not.toContain('publish-open-draft');
    expect(actions(html)).toEqual(['Cancel', 'Create topology']);
  });

  test('lists the diagram’s errors without the hint', async () => {
    const html = await render({}, { publishable: false });

    expect(html).toMatch(/<strong[^>]*>\s*Error:\s*<\/strong>/);
    expect(html).not.toContain('publish-open-hint');
    expect(html).not.toContain('Open the draft');
  });
});
