// @vitest-environment happy-dom
// Keys and status messages of two Inspector controls, mounted in the
// Inspector: Escape in a color field with its picker open
// (InspectorColorControl), and the count of the suggestions that match
// (InspectorComboboxControl).

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { resetCollapsed } from '@/components/builder/inspector/collapse.js';

import { COUNT_DELAY_MS } from '@/builder/announce.js';

import { sampleDocument } from './fixtures.js';
import { fieldAt, keydown, mountInspector, type } from './inspector-mount.js';

enableAutoUnmount(afterEach);
afterEach(resetCollapsed);

const DISKS = ['ubuntu.qc2', 'kali.qc2', 'ubuntu-22.qc2', 'win10.qc2'];

async function openAlpha() {
  const sample = sampleDocument();

  return mountInspector({
    doc: sample.doc,
    element: sample.alpha,
    prepare(store) {
      store.disks = DISKS;
    },
  });
}

// Records the keydowns that reach the page, past the Inspector.
function pageKeys() {
  const seen = [];
  const listener = (event) => seen.push(event.key);

  document.body.addEventListener('keydown', listener);

  return {
    seen,
    stop: () => document.body.removeEventListener('keydown', listener),
  };
}

describe('Escape in a color field', () => {
  async function openColor() {
    const opened = await openAlpha();
    const root = opened.wrapper.get('.inspector-color');
    const popup = () => root.find('[data-testid="inspector-color-popup"]');

    return {
      ...opened,
      trigger: root.get('[data-testid="inspector-color-picker"]'),
      text: root.get('input[type="text"]'),
      popup,
    };
  }

  test('with the picker open and focus in the text field, it closes the picker and goes no further', async () => {
    const { trigger, text, popup } = await openColor();
    const page = pageKeys();

    await trigger.trigger('click');
    await flushPromises();
    expect(popup().exists()).toBe(true);

    text.element.focus();

    const escape = keydown(text, 'Escape');

    await flushPromises();
    page.stop();

    expect(popup().exists()).toBe(false);
    expect(escape.defaultPrevented).toBe(true);
    expect(page.seen).toEqual([]);
    // Focus stays where the user was typing.
    expect(document.activeElement).toBe(text.element);
  });

  test('with focus in the picker, it closes the picker and gives focus to its button', async () => {
    const { trigger, popup } = await openColor();
    const page = pageKeys();

    await trigger.trigger('click');
    await flushPromises();

    const swatch = popup().get('.inspector-color__option');

    swatch.element.focus();
    keydown(swatch, 'Escape');
    await flushPromises();
    page.stop();

    expect(popup().exists()).toBe(false);
    expect(page.seen).toEqual([]);
    expect(document.activeElement).toBe(trigger.element);
  });

  test('with the picker closed, it goes on to the page', async () => {
    const { text, popup } = await openColor();
    const page = pageKeys();

    text.element.focus();
    keydown(text, 'Escape');
    await flushPromises();
    page.stop();

    expect(popup().exists()).toBe(false);
    expect(page.seen).toEqual(['Escape']);
  });
});

describe('the count of matching suggestions', () => {
  const IMAGE = 'spec.hardware.drives.0.image';
  const pause = () =>
    new Promise((resolve) => setTimeout(resolve, COUNT_DELAY_MS + 50));

  test('says how many suggestions match once typing pauses, and nothing once the list closes', async () => {
    const { wrapper } = await openAlpha();
    const field = fieldAt(wrapper, IMAGE);
    const status = wrapper
      .get(`[data-path="${IMAGE}"]`)
      .get('[data-testid="inspector-combobox-count"]');

    expect(status.attributes('role')).toBe('status');
    expect(status.classes()).toContain('builder-visually-hidden');
    expect(status.text()).toBe('');

    field.element.focus();
    await type(field, 'ub');
    // Not for each letter: it waits.
    expect(status.text()).toBe('');
    await pause();
    expect(status.text()).toBe('2 suggestions');

    await type(field, 'kal');
    await pause();
    expect(status.text()).toBe('1 suggestion');

    await type(field, 'zzz');
    await pause();
    expect(field.attributes('aria-expanded')).toBe('false');
    expect(status.text()).toBe('No suggestions');

    await type(field, 'ub');
    keydown(field, 'Escape');
    await flushPromises();
    expect(status.text()).toBe('');
  });
});
