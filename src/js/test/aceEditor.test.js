// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';
import AceEditor from '@/components/configs/AceEditor.vue';

// A stand-in for Ace that records the options each editor is made with. The
// real Vim and language tools extensions register on the global ace.
const ace = vi.hoisted(() => {
  globalThis.ace = {
    config: { set: () => {}, setModuleUrl: () => {}, loadModule: () => {} },
    define: () => {},
    require: vi.fn(),
    edit: null,
  };
  return globalThis.ace;
});
vi.mock('ace-builds/src-noconflict/ace', () => ({ default: ace }));

function fakeEditor() {
  const session = { on: vi.fn(), setMode: vi.fn() };
  return {
    setValue: vi.fn(),
    getValue: () => '',
    getSession: () => session,
    session,
    setKeyboardHandler: vi.fn(),
    destroy: vi.fn(),
  };
}

enableAutoUnmount(afterEach);

function mountAce() {
  return mount(AceEditor, {
    attachTo: document.body,
    props: { value: 'kind: Role\n', lang: 'yaml' },
    global: { plugins: [Buefy] },
  });
}

describe('AceEditor', () => {
  let editor;

  beforeEach(() => {
    editor = fakeEditor();
    ace.edit = vi.fn((el) => {
      for (const suffix of ['-v', '-h']) {
        const bar = document.createElement('div');
        bar.className = `ace_scrollbar ace_scrollbar${suffix}`;
        el.appendChild(bar);
      }
      return editor;
    });
  });

  test('turns on keyboard accessibility, so Tab does not trap focus', async () => {
    const wrapper = mountAce();
    await vi.waitFor(() => expect(wrapper.vm.isLoading).toBe(false));

    expect(ace.edit).toHaveBeenCalledOnce();
    expect(ace.edit.mock.calls[0][1]).toMatchObject({
      enableKeyboardAccessibility: true,
    });
  });

  test('takes the scroll bars out of the tab order', async () => {
    const wrapper = mountAce();
    await vi.waitFor(() => expect(wrapper.vm.isLoading).toBe(false));

    const bars = wrapper.findAll('.ace_scrollbar');
    expect(bars).toHaveLength(2);
    for (const bar of bars) {
      expect(bar.attributes('tabindex')).toBe('-1');
    }
  });

  test('an editor removed before Ace loads makes no editor and does not throw', async () => {
    const wrapper = mountAce();
    wrapper.unmount();
    // mounted() goes on loading Ace after the unmount, and must stop there.
    await vi.waitFor(() => expect(ace.require).toHaveBeenCalled());
    await flushPromises();

    expect(ace.edit).not.toHaveBeenCalled();
  });

  test('removing the editor destroys the Ace editor', async () => {
    const wrapper = mountAce();
    await vi.waitFor(() => expect(wrapper.vm.isLoading).toBe(false));
    wrapper.unmount();

    expect(editor.destroy).toHaveBeenCalledOnce();
  });
});
