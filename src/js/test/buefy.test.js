// installBuefy registers only the Buefy components the UI uses. A b-* tag with
// no registered component renders as an unknown element in production builds
// (Vue only warns in development), so every template's b-* elements are
// checked, whether or not a test renders them. installBuefy also sets the
// defaults Buefy lacks, such as a name for the modal close button.
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { createApp } from 'vue';
import { babelParse, parse } from 'vue/compiler-sfc';
import { expect, test } from 'vitest';

import { installBuefy } from '@/utils/buefy.js';

import { renderSSR } from './helpers/render.js';

const SRC = join(import.meta.dirname, '..', 'src');

const vueFiles = (dir) =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return vueFiles(path);
    return entry.name.endsWith('.vue') ? [path] : [];
  });

const pascal = (tag) =>
  tag.replace(/(?:^|-)([a-z])/g, (_, c) => c.toUpperCase());

// the component names of the b-* elements in a template
function buefyTags(node, tags = new Set()) {
  const ELEMENT = 1;
  if (node.type == ELEMENT && node.tag.startsWith('b-')) {
    tags.add(pascal(node.tag));
  }
  for (const child of node.children ?? []) buefyTags(child, tags);
  return tags;
}

// the component names a script imports from buefy
const buefyImports = (script) =>
  babelParse(script?.content ?? '', { sourceType: 'module' })
    .program.body.filter(
      (node) =>
        node.type == 'ImportDeclaration' && node.source.value == 'buefy',
    )
    .flatMap((node) => node.specifiers.map((s) => s.local.name));

// the components installBuefy registers for every view
const app = createApp({});
installBuefy(app);
const GLOBAL = new Set(Object.keys(app._context.components));

const FILES = vueFiles(SRC);

// the b-* components a view uses that are neither registered nor imported
function unregistered(file) {
  const { descriptor } = parse(readFileSync(file, 'utf8'), { filename: file });
  if (!descriptor.template) return [];
  const local = new Set(
    buefyImports(descriptor.script ?? descriptor.scriptSetup),
  );
  return [...buefyTags(descriptor.template.ast)].filter(
    (name) => !GLOBAL.has(name) && !local.has(name),
  );
}

test('finds the views and the b-* tags in their templates', () => {
  expect(FILES.length).toBeGreaterThan(0);
  const disks = FILES.find(
    (f) => relative(SRC, f) == join('views', 'Disks.vue'),
  );
  const { descriptor } = parse(readFileSync(disks, 'utf8'));
  expect([...buefyTags(descriptor.template.ast)]).toContain('BTable');
});

test.each(FILES.map((file) => [relative(SRC, file), file]))(
  '%s uses only b-* tags that are registered or imported',
  (_, file) => {
    expect(unregistered(file)).toEqual([]);
  },
);

test('names the modal close button Close', async () => {
  const html = await renderSSR({
    template: '<b-modal :model-value="true"><p>x</p></b-modal>',
  });
  const close = html.match(/<button[^>]*\bmodal-close\b[^>]*>/g);
  expect(close).toHaveLength(1);
  expect(close[0]).toContain('aria-label="Close"');
});
