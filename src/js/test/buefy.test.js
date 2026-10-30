// installBuefy registers only the Buefy components in use, so every b-* tag is
// checked, rendered or not: prod leaves an unknown one blank (dev only warns).
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { createApp } from 'vue';
import { babelParse, parse } from 'vue/compiler-sfc';
import { flushPromises } from '@vue/test-utils';
import { expect, test } from 'vitest';

import { installBuefy } from '@/utils/buefy.js';

import { dialogs, mountComponent, textOf } from './helpers/mount.js';

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
  const modal = await mountComponent({
    template: '<b-modal :model-value="true"><p>x</p></b-modal>',
  });
  const close = modal.findAll('button.modal-close');
  expect(close.map((b) => b.attributes('aria-label'))).toEqual(['Close']);
});

test('gives a confirm dialog the alertdialog role, as a modal', async () => {
  const wrapper = await mountComponent({ template: '<p>x</p>' });
  wrapper.vm.$buefy.dialog.confirm({ message: 'Sure?' });
  await flushPromises();
  const [dialog] = dialogs();
  expect(['role', 'aria-modal'].map((a) => dialog.getAttribute(a))).toEqual([
    'alertdialog',
    'true',
  ]);
});

test('gives a modal the dialog role, as a modal', async () => {
  const modal = await mountComponent({
    template: '<b-modal :model-value="true"><p>x</p></b-modal>',
  });
  const root = modal.find('.modal');
  expect(['role', 'aria-modal'].map((a) => root.attributes(a))).toEqual([
    'dialog',
    'true',
  ]);
});

test('sorts a table from the keyboard and says how it is sorted', async () => {
  const table = await mountComponent({
    template: `<b-table :data="[{ n: 2 }, { n: 1 }]">
      <b-table-column field="n" label="N" sortable v-slot="p">{{ p.row.n }}</b-table-column>
      <b-table-column field="m" label="M" v-slot="p">{{ p.row.n }}</b-table-column>
    </b-table>`,
  });
  const [n, m] = table.findAll('th');
  const values = () => table.findAll('tbody td:first-child').map(textOf);
  expect([n.attributes('tabindex'), m.attributes('tabindex')]).toEqual([
    '0',
    undefined,
  ]);
  expect(n.attributes('aria-sort')).toBe('none');
  await n.trigger('keydown', { key: 'Enter' });
  expect([n.attributes('aria-sort'), values()]).toEqual([
    'ascending',
    ['1', '2'],
  ]);
  await n.trigger('keydown', { key: ' ' });
  expect([n.attributes('aria-sort'), values()]).toEqual([
    'descending',
    ['2', '1'],
  ]);
  expect(table.find('.table-mobile-sort').exists()).toBe(false);
});

test.each([
  [
    'modal',
    '<b-modal ref="overlay" :model-value="on" @update:model-value="(v) => (on = v)"><p>x</p></b-modal>',
    (vm, on) => (vm.on = on),
  ],
  [
    'dropdown',
    '<b-dropdown ref="overlay"><template #trigger><span /></template></b-dropdown>',
    // Buefy opens a dropdown after a timeout
    (vm) => {
      vm.$refs.overlay.toggle();
      return new Promise((done) => setTimeout(done));
    },
  ],
])('returns focus to its opener when a %s closes', async (_, inner, set) => {
  const wrapper = await mountComponent(
    {
      template: `<div><button class="opener">open</button>${inner}</div>`,
      data: () => ({ on: false }),
    },
    { attachTo: document.body },
  );
  const opener = wrapper.find('.opener').element;
  opener.focus();
  await set(wrapper.vm, true);
  await flushPromises();
  document.activeElement.blur();
  await set(wrapper.vm, false);
  await flushPromises();
  expect(document.activeElement).toBe(opener);
});
