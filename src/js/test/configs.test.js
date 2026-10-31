// @vitest-environment happy-dom
import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Configs from '@/views/Configs.vue';

vi.mock('@/components/configs/ConfigsEditor.vue', () => ({
  default: {
    name: 'ConfigsEditor',
    emits: ['is-done'],
    template: '<div class="editor-stub" />',
  },
}));

const ListStub = {
  name: 'ConfigsList',
  props: { focusConfig: { type: String, default: '' } },
  emits: ['edit', 'create'],
  template: '<div class="list-stub" />',
};

const site = { kind: 'Topology', metadata: { name: 'site' } };

enableAutoUnmount(afterEach);

async function mountView() {
  const wrapper = mount(Configs, {
    global: {
      stubs: { ConfigsList: ListStub },
      mocks: { $buefy: { toast: { open: vi.fn() } } },
    },
  });
  await flushPromises();
  return wrapper;
}

// Closes the open editor without a toast message.
async function closeEditor(wrapper) {
  wrapper.findComponent({ name: 'ConfigsEditor' }).vm.$emit('is-done', '');
  await flushPromises();
}

describe('Configs view', () => {
  test('returns focus to the edited config when the editor closes', async () => {
    const wrapper = await mountView();
    wrapper.findComponent(ListStub).vm.$emit('edit', site);
    await flushPromises();
    expect(wrapper.findComponent(ListStub).exists()).toBe(false);

    await closeEditor(wrapper);
    expect(wrapper.findComponent(ListStub).props('focusConfig')).toBe(
      'Topology/site',
    );
  });

  test('follows a renamed config when the editor saves it', async () => {
    const wrapper = await mountView();
    wrapper.findComponent(ListStub).vm.$emit('edit', site);
    await flushPromises();

    wrapper
      .findComponent({ name: 'ConfigsEditor' })
      .vm.$emit(
        'is-done',
        'config Topology/site has been edited',
        'Topology/hq',
      );
    await flushPromises();
    expect(wrapper.findComponent(ListStub).props('focusConfig')).toBe(
      'Topology/hq',
    );
  });

  test('clears the focus target after creating a config', async () => {
    const wrapper = await mountView();
    wrapper.findComponent(ListStub).vm.$emit('edit', site);
    await flushPromises();
    await closeEditor(wrapper);

    wrapper.findComponent(ListStub).vm.$emit('create');
    await flushPromises();
    await closeEditor(wrapper);
    expect(wrapper.findComponent(ListStub).props('focusConfig')).toBe('');
  });
});
