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

test('every view uses only b-* tags that are registered or imported', () => {
  const missing = Object.fromEntries(
    FILES.map((file) => [relative(SRC, file), unregistered(file)]).filter(
      ([, tags]) => tags.length > 0,
    ),
  );
  expect(missing).toEqual({});
});

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

// a modal card with a title, opened and closed by its button and Done
const TITLED_MODAL = {
  template: `<div>
    <button class="opener" @click="on = true">open</button>
    <b-modal :model-value="on" @update:model-value="(v) => (on = v)" has-modal-card>
      <div class="modal-card">
        <header class="modal-card-head">
          <h2 v-if="title" class="modal-card-title">{{ title }}</h2>
        </header>
        <section class="modal-card-body">
          <button class="done" @click="on = false">Done</button>
        </section>
      </div>
    </b-modal>
  </div>`,
  data: () => ({ on: false, title: 'Edit thing' }),
};

async function openTitledModal() {
  const wrapper = await mountComponent(TITLED_MODAL, {
    attachTo: document.body,
  });
  const opener = wrapper.find('.opener');
  opener.element.focus();
  await opener.trigger('click');
  await flushPromises();
  return wrapper;
}

// the text of what names a modal or dialog through aria-labelledby
const nameOf = (el) =>
  el
    .getAttribute('aria-labelledby')
    ?.split(/\s+/)
    .map((id) => textOf(document.getElementById(id)))
    .join(' ');

test('names a modal with its title as it opens', async () => {
  const wrapper = await openTitledModal();
  expect(nameOf(wrapper.find('.modal').element)).toBe('Edit thing');
});

test('names a modal open from the start with its title', async () => {
  const wrapper = await mountComponent(TITLED_MODAL, {
    data: { on: true, title: 'Edit thing' },
    attachTo: document.body,
  });
  await flushPromises();
  expect(nameOf(wrapper.find('.modal').element)).toBe('Edit thing');
});

test('names a modal again when it opens with a new title', async () => {
  const wrapper = await openTitledModal();
  await wrapper.find('.done').trigger('click');
  wrapper.vm.title = null;
  await flushPromises();
  wrapper.vm.title = 'Rename thing';
  await wrapper.find('.opener').trigger('click');
  await flushPromises();
  expect(nameOf(wrapper.find('.modal').element)).toBe('Rename thing');
});

test('keeps the name a modal is given', async () => {
  const wrapper = await mountComponent({
    template: `<div>
      <b-modal :model-value="true" aria-label="Own name" has-modal-card>
        <div class="modal-card"><p class="modal-card-title">Title</p></div>
      </b-modal>
    </div>`,
  });
  await flushPromises();
  const modal = wrapper.find('.modal');
  expect(modal.attributes('aria-label')).toBe('Own name');
  expect(modal.attributes('aria-labelledby')).toBeUndefined();
});

test.each([
  ['its title', { title: 'Delete?', message: 'Delete it?' }, 'Delete?'],
  [
    'its message with no title',
    { message: 'Rename the disk?' },
    'Rename the disk?',
  ],
])('names a dialog with %s', async (_, options, name) => {
  const wrapper = await mountComponent({ template: '<p>x</p>' });
  wrapper.vm.$buefy.dialog.prompt(options);
  await flushPromises();
  const [dialog] = dialogs();
  expect(nameOf(dialog)).toBe(name);
});

test('returns focus to its opener when a modal closes on Escape', async () => {
  const wrapper = await openTitledModal();
  wrapper.find('.done').element.focus();
  document.dispatchEvent(new KeyboardEvent('keyup', { key: 'Escape' }));
  await flushPromises();
  expect(wrapper.vm.on).toBe(false);
  expect(document.activeElement).toBe(wrapper.find('.opener').element);
});

test('does not sort a table on other keys', async () => {
  const table = await mountComponent({
    template: `<b-table :data="[{ n: 2 }, { n: 1 }, { n: 3 }]">
      <b-table-column field="n" label="N" sortable v-slot="p">{{ p.row.n }}</b-table-column>
    </b-table>`,
  });
  const [n] = table.findAll('th');
  await n.trigger('keydown', { key: 'a' });
  expect(table.findAll('tbody td').map(textOf)).toEqual(['2', '1', '3']);
  expect(n.attributes('aria-sort')).toBe('none');
});
