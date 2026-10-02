// Import, Upload and Download: each button opens the dialog its label
// names, and each dialog's ids and test ids start with that label. Import
// and Upload share the names of several controls (a file, an error, a
// submit button), so an id of one must never be found in the other.

import { readFileSync } from 'node:fs';

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { getCommand } from '@/builder/commands.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

const DIALOGS = [
  {
    name: 'Import',
    component: ImportDialog,
    file: 'ImportDialog.vue',
    prefix: 'import',
    title: 'Import topology or experiment',
  },
  {
    name: 'Upload',
    component: UploadDialog,
    file: 'UploadDialog.vue',
    prefix: 'upload',
    title: 'Upload diagram',
  },
  {
    name: 'Download',
    component: DownloadDialog,
    file: 'DownloadDialog.vue',
    prefix: 'download',
    title: 'Download diagram',
  },
];

// The prefixes the three dialogs have, and the two they had before.
const PREFIXES = ['import', 'upload', 'download', 'generate', 'export'];

async function render(component, props = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);
  useBuilderStore(pinia).doc = sampleDocument().doc;

  return renderToString(app);
}

// The text of the one element with the test id.
function textOf(html, testId) {
  const found = html.match(
    new RegExp(
      `<button\\b[^>]*data-testid="${testId}"[^>]*>([\\s\\S]*?)</button>`,
    ),
  );

  expect(found, testId).not.toBeNull();

  return found[1].replace(/<[^>]*>/g, '').trim();
}

// Every id, test id, radio group name and label target in the template of
// a dialog, in all its branches, that starts with one of PREFIXES.
function templateNames(file) {
  const source = readFileSync(
    new URL(`../../src/components/builder/dialogs/${file}`, import.meta.url),
    'utf8',
  );
  const template = source.slice(0, source.indexOf('<script setup>'));
  const names = [
    ...template.matchAll(
      /\s(?:id|data-testid|name|for|title-id)="([a-z]+)-[a-z-]+"/g,
    ),
  ].map((match) => match[1]);

  return [...new Set(names)].filter((name) => PREFIXES.includes(name));
}

describe('the buttons that open Import, Upload and Download', () => {
  test('the drafts page names Import and Upload as their test ids do', async () => {
    const html = await render(BuilderDrafts, { canCreate: true });

    expect(textOf(html, 'drafts-import')).toBe('Import');
    expect(textOf(html, 'drafts-upload')).toBe('Upload');
  });

  test('the toolbar names Download and Upload as their test ids do', async () => {
    const html = await render(BuilderToolbar);

    expect(textOf(html, 'toolbar-download')).toBe('Download');
    expect(textOf(html, 'toolbar-upload')).toBe('Upload');
  });

  test('the commands open the dialog their title names', () => {
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

    expect(getCommand('draft.download').title).toBe('Download…');
    expect(getCommand('draft.upload').title).toBe('Upload…');
    expect(getCommand('drafts.import').title).toBe('Import…');
    expect(getCommand('drafts.upload').title).toBe('Upload…');
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

  test('names its controls after its own label only', () => {
    expect(templateNames(dialog.file)).toEqual([dialog.prefix]);
  });
});

describe('the sources of Import and Upload', () => {
  test('Import reads a stored config or a config file', async () => {
    const html = await render(ImportDialog);
    const labels = [...html.matchAll(/name="import-source"[^>]*>([^<]*)</g)];

    expect(labels.map((match) => match[1].trim())).toEqual([
      'Stored config',
      'Config file',
    ]);
  });

  test('Upload reads a file, pasted text, a published diagram or a legacy diagram', async () => {
    const html = await render(UploadDialog);
    const labels = [...html.matchAll(/name="upload-source"[^>]*>([^<]*)</g)];

    expect(labels.map((match) => match[1].trim())).toEqual([
      'File',
      'Paste text',
      'Published diagram',
      'Legacy Builder diagram or Topology',
    ]);
  });
});
