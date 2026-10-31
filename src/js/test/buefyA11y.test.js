// @vitest-environment happy-dom
import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import Buefy, { BDialog } from 'buefy';
import buefyA11y from '@/plugins/buefyA11y.js';

enableAutoUnmount(afterEach);

const global = { plugins: [Buefy, buefyA11y] };

const ModalHost = {
  data: () => ({ active: false }),
  template: `
    <div>
      <button id="opener" @click="active = true">Open</button>
      <b-modal v-model="active" has-modal-card>
        <div class="modal-card">
          <header class="modal-card-head">
            <h2 class="modal-card-title">Edit thing</h2>
          </header>
          <section class="modal-card-body">
            <button id="inside" @click="active = false">Done</button>
          </section>
        </div>
      </b-modal>
    </div>`,
};

async function openModal() {
  const wrapper = mount(ModalHost, { global, attachTo: document.body });
  const opener = wrapper.get('#opener');
  opener.element.focus();
  await opener.trigger('click');
  await flushPromises();
  return wrapper;
}

describe('modals', () => {
  test('are named dialogs with a named close button', async () => {
    const wrapper = await openModal();
    const modal = wrapper.get('.modal');

    expect(modal.attributes('role')).toBe('dialog');
    expect(modal.attributes('aria-modal')).toBe('true');
    const title = wrapper.get('.modal-card-title');
    expect(modal.attributes('aria-labelledby')).toBe(title.attributes('id'));
    expect(wrapper.get('.modal-close').attributes('aria-label')).toBe('Close');
  });

  test('return focus to the opener when they close', async () => {
    const wrapper = await openModal();
    const inside = wrapper.get('#inside');
    inside.element.focus();
    expect(document.activeElement).toBe(inside.element);

    await inside.trigger('click');
    await flushPromises();

    expect(document.activeElement).toBe(wrapper.get('#opener').element);
  });

  test('return focus to the opener after Escape', async () => {
    const wrapper = await openModal();
    wrapper.get('#inside').element.focus();

    document.dispatchEvent(new KeyboardEvent('keyup', { key: 'Escape' }));
    await flushPromises();

    expect(wrapper.vm.active).toBe(false);
    expect(document.activeElement).toBe(wrapper.get('#opener').element);
  });

  test('dialogs are modal dialogs too', async () => {
    const wrapper = mount(BDialog, {
      global,
      props: { title: 'Delete?', message: 'Delete it?' },
      attachTo: document.body,
    });
    await flushPromises();
    const dialog = wrapper.get('.dialog');

    expect(dialog.attributes('role')).toBe('dialog');
    expect(dialog.attributes('aria-modal')).toBe('true');
    const title = wrapper.get('.modal-card-title');
    expect(dialog.attributes('aria-labelledby')).toBe(title.attributes('id'));
  });

  test('dialogs without a title are named by their message', async () => {
    const wrapper = mount(BDialog, {
      global,
      props: { message: 'Rename the disk?', hasInput: true },
      attachTo: document.body,
    });
    await flushPromises();
    const dialog = wrapper.get('.dialog');
    const id = dialog.attributes('aria-labelledby');

    expect(wrapper.find('.modal-card-title').exists()).toBe(false);
    expect(id).toBeTruthy();
    expect(document.getElementById(id).textContent.trim()).toBe(
      'Rename the disk?',
    );
  });
});

const TableHost = {
  data: () => ({
    rows: [{ name: 'b' }, { name: 'a' }, { name: 'c' }],
  }),
  template: `
    <b-table :data="rows">
      <b-table-column field="name" label="Name" sortable v-slot="props">
        {{ props.row.name }}
      </b-table-column>
      <b-table-column field="note" label="Note" v-slot="props">-</b-table-column>
    </b-table>`,
};

describe('sortable table headers', () => {
  const names = (wrapper) =>
    wrapper.findAll('tbody tr td:first-child').map((td) => td.text());

  test('can be focused, but other headers cannot', async () => {
    const wrapper = mount(TableHost, { global });
    await flushPromises();
    const [name, note] = wrapper.findAll('thead th');

    expect(name.attributes('tabindex')).toBe('0');
    expect(note.attributes('tabindex')).toBeUndefined();
    expect(name.attributes('aria-sort')).toBeUndefined();
  });

  test('sort with Enter and Space and report the order', async () => {
    const wrapper = mount(TableHost, { global });
    await flushPromises();
    const name = () => wrapper.findAll('thead th')[0];

    await name().trigger('keydown', { key: 'Enter' });
    await nextTick();
    expect(names(wrapper)).toEqual(['a', 'b', 'c']);
    expect(name().attributes('aria-sort')).toBe('ascending');

    await name().trigger('keydown', { key: ' ' });
    await nextTick();
    expect(names(wrapper)).toEqual(['c', 'b', 'a']);
    expect(name().attributes('aria-sort')).toBe('descending');
  });

  test('ignore other keys', async () => {
    const wrapper = mount(TableHost, { global });
    await flushPromises();
    await wrapper.findAll('thead th')[0].trigger('keydown', { key: 'a' });

    expect(names(wrapper)).toEqual(['b', 'a', 'c']);
  });
});
