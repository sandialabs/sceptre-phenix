// @vitest-environment happy-dom
// The Rename form of the Custom icons dialog (IconDialog.vue): a refused name
// marks the field invalid and links it to the dialog's error.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

import IconDialog from '@/components/builder/dialogs/IconDialog.vue';
import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';

import { ICON_DATA } from './png.js';

enableAutoUnmount(afterEach);

const ICONS = ['hmi', 'pump'].map((name) => ({
  name,
  owner: 'alice',
  width: 1,
  height: 1,
  bytes: 70,
  aliases: [],
  data: ICON_DATA,
  canRename: true,
  canDelete: true,
}));

// A library whose server refuses a name another icon has.
function library() {
  const state = reactive({
    status: 'ready',
    error: '',
    icons: [...ICONS],
    maxIcons: 2000,
  });

  return {
    state,
    load: vi.fn(async () => {}),
    ensure: vi.fn(async () => {}),
    lookup: (name) => state.icons.find((icon) => icon.name === name) || null,
    upload: vi.fn(),
    rename: vi.fn(async (_, to) => {
      if (state.icons.some((icon) => icon.name === to)) {
        throw new Error(`An icon named ${to} exists.`);
      }

      return { name: to };
    }),
    failure: (caught) => caught.message,
  };
}

function openRename() {
  const wrapper = mount(IconDialog, {
    attachTo: document.body,
    global: {
      provide: {
        [INSPECTOR_ICONS]: { entry: () => undefined, diagram: () => [] },
        [INSPECTOR_ICON_LIBRARY]: library(),
      },
    },
  });

  return wrapper;
}

const nameField = (wrapper) => wrapper.get('[data-testid="icon-rename-name"]');
const describedBy = (wrapper) =>
  (nameField(wrapper).attributes('aria-describedby') || '').split(' ');

async function submit(wrapper, name) {
  await nameField(wrapper).setValue(name);
  await wrapper.get('[data-testid="icon-rename-form"]').trigger('submit');
  await flushPromises();
}

describe('the Rename form', () => {
  test('a taken name marks the field invalid and describes it with the error', async () => {
    const wrapper = openRename();

    await wrapper.get('button[aria-label="Rename hmi"]').trigger('click');
    await flushPromises();
    expect(nameField(wrapper).attributes('aria-invalid')).toBeUndefined();
    expect(describedBy(wrapper)).toEqual(['icon-rename-hint']);

    await submit(wrapper, 'pump');

    const error = wrapper.get('#icon-dialog-error');

    expect(error.text()).toBe('An icon named pump exists.');
    expect(error.attributes('role')).toBe('alert');
    expect(nameField(wrapper).attributes('aria-invalid')).toBe('true');
    expect(describedBy(wrapper)).toEqual([
      'icon-rename-hint',
      'icon-dialog-error',
    ]);
  });

  test('a name the rule refuses is linked the same way, and the link goes with the form', async () => {
    const wrapper = openRename();

    await wrapper.get('button[aria-label="Rename hmi"]').trigger('click');
    await flushPromises();
    await submit(wrapper, 'not a name!');

    expect(nameField(wrapper).attributes('aria-invalid')).toBe('true');
    expect(describedBy(wrapper)).toContain('icon-dialog-error');

    await wrapper.get('[data-testid="icon-rename-cancel"]').trigger('click');
    await wrapper.get('button[aria-label="Rename pump"]').trigger('click');
    await flushPromises();

    expect(nameField(wrapper).attributes('aria-invalid')).toBeUndefined();
    expect(describedBy(wrapper)).toEqual(['icon-rename-hint']);
  });
});
