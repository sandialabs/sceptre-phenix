// @vitest-environment happy-dom
// The editor's live region with a real Builder store: messages wait while a
// modal dialog is open, those the dialog said itself are not said again, and
// each message is held 750 ms.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderLiveRegion from '@/components/builder/BuilderLiveRegion.vue';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

const HOLD_MS = 750;

// happy-dom parses :modal but matches no element. An open dialog here is
// always modal.
function modalAsOpen() {
  const query = document.querySelector.bind(document);

  vi.spyOn(document, 'querySelector').mockImplementation((selector) =>
    query(selector === 'dialog:modal' ? 'dialog[open]' : selector),
  );
}

let dialog = null;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  modalAsOpen();
});

afterEach(() => {
  dialog?.remove();
  dialog = null;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function openDialog() {
  dialog = document.createElement('dialog');
  document.body.append(dialog);
  dialog.showModal();
}

function closeDialog() {
  dialog.close();
}

function mountRegion() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  const wrapper = mount(BuilderLiveRegion, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });
  const spoken = [];

  // Each message the region shows, in order.
  const record = async () => {
    await wrapper.vm.$nextTick();
    const text = wrapper.get('[data-testid="builder-live-region"]').text();

    if (text && text !== spoken.at(-1)) {
      spoken.push(text);
    }
  };

  const wait = async () => {
    for (let step = 0; step < 4; step += 1) {
      vi.advanceTimersByTime(HOLD_MS);
      await record();
    }
  };

  return { store, spoken, record, wait };
}

describe('the live region behind a modal dialog', () => {
  test('waits, and says what the dialog did not say when it closes', async () => {
    const { store, spoken, wait } = mountRegion();

    openDialog();
    store.announce('Moved alpha');
    await wait();
    expect(spoken).toEqual([]);

    closeDialog();
    await wait();
    expect(spoken).toEqual(['Moved alpha']);
  });

  test('does not say again what the dialog said', async () => {
    const { store, spoken, wait } = mountRegion();

    openDialog();
    store.announce('Renamed alpha to alpha-2');
    store.noteDialogSaid('Renamed alpha to alpha-2.');
    store.announce('Moved alpha');
    await wait();

    closeDialog();
    await wait();
    expect(spoken).toEqual(['Moved alpha']);
  });

  test('says only the part of a message that the dialog did not say', async () => {
    const { store, spoken, wait } = mountRegion();

    openDialog();
    store.announce(
      'Renamed alpha to alpha-2. Not saved: this draft changed on the server.',
    );
    store.noteDialogSaid('Renamed alpha to alpha-2.');
    closeDialog();
    await wait();

    expect(spoken).toEqual(['Not saved: this draft changed on the server.']);
  });

  test('says the same message again once the dialog is closed', async () => {
    const { store, spoken, wait } = mountRegion();

    openDialog();
    store.announce('Renamed alpha to alpha-2');
    store.noteDialogSaid('Renamed alpha to alpha-2.');
    closeDialog();
    await wait();
    expect(spoken).toEqual([]);

    store.announce('Renamed alpha to alpha-2');
    await wait();
    expect(spoken).toEqual(['Renamed alpha to alpha-2']);
  });

  test('keeps a save state the dialog did not say', async () => {
    const { store, spoken, wait } = mountRegion();

    openDialog();
    store.announce('Added Layer 3');
    store.noteDialogSaid('Added Layer 3.');
    store.announce('All changes saved.', { slot: 'save' });
    closeDialog();
    await wait();

    expect(spoken).toEqual(['All changes saved.']);
  });
});

describe('the live region’s pace', () => {
  test('holds each message 750 ms, then says those that waited together', async () => {
    const { store, spoken, record } = mountRegion();

    store.announce('Added device');
    await record();
    store.announce('Moved alpha');
    store.announce('Moved beta');
    vi.advanceTimersByTime(HOLD_MS - 1);
    await record();
    expect(spoken).toEqual(['Added device']);

    vi.advanceTimersByTime(1);
    await record();
    expect(spoken).toEqual(['Added device', 'Moved alpha. Moved beta.']);
  });
});
