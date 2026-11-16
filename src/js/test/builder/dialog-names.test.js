// @vitest-environment node
// Import, Upload and Download: each button opens the dialog it names, and each
// dialog's title has an id of that name.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { getCommand } from '@/builder/commands.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, textOf } from './fixtures.js';

const DIALOGS = [
  {
    name: 'Import',
    component: ImportDialog,
    prefix: 'import',
    title: 'Import topology or experiment',
  },
  {
    name: 'Upload',
    component: UploadDialog,
    prefix: 'upload',
    title: 'Upload diagram',
  },
  {
    name: 'Download',
    component: DownloadDialog,
    prefix: 'download',
    title: 'Download diagram',
  },
];

async function render(component, props = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);
  useBuilderStore(pinia).doc = sampleDocument().doc;

  return renderToString(app);
}

// The text of the button with the test id.
function buttonText(html, testId) {
  const found = html.match(
    new RegExp(
      `<button\\b[^>]*data-testid="${testId}"[^>]*>([\\s\\S]*?)</button>`,
    ),
  );

  expect(found, testId).not.toBeNull();

  return textOf(found[1]);
}

describe('the buttons that open Import, Upload and Download', () => {
  test('the drafts page names Import and Upload as their test ids do', async () => {
    const html = await render(BuilderDrafts, { canCreate: true });

    expect(buttonText(html, 'drafts-import')).toBe('Import');
    expect(buttonText(html, 'drafts-upload')).toBe('Upload');
  });

  test('the toolbar names Download and Upload as their test ids do', async () => {
    const html = await render(BuilderToolbar);

    expect(buttonText(html, 'toolbar-download')).toBe('Download');
    expect(buttonText(html, 'toolbar-upload')).toBe('Upload');
  });

  test('the commands open the dialog of their name', () => {
    const opened = [];
    const view = {
      openDialog: (name) => opened.push(['dialog', name]),
      openLanding: (name) => opened.push(['landing', name]),
    };

    for (const id of [
      'draft.download',
      'draft.upload',
      'drafts.import',
      'drafts.upload',
    ]) {
      getCommand(id).run({ view });
    }

    expect(opened).toEqual([
      ['dialog', 'download'],
      ['dialog', 'upload'],
      ['landing', 'import'],
      ['landing', 'upload'],
    ]);
  });
});

describe.each(DIALOGS)('the $name dialog', (dialog) => {
  test('has its title, under an id of its own prefix', async () => {
    const html = await render(dialog.component);

    expect(html).toContain(`aria-labelledby="${dialog.prefix}-dialog-title"`);
    expect(html).toMatch(
      new RegExp(
        `<h2 id="${dialog.prefix}-dialog-title"[^>]*>${dialog.title}</h2>`,
      ),
    );
  });
});
