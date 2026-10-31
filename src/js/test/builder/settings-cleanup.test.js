// @vitest-environment happy-dom
// Clean up… in the Builder settings, mounted and used: the dry run, the
// confirmation over the settings, the cleanup, the spinner and what the
// settings' status region says.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';

import BuilderSettings from '@/components/builder/BuilderSettings.vue';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

// What the server's dry run finds, and then what the cleanup removes.
const PLAN = { drafts: ['d1', 'd2'], documents: 1, content: 2 };

// A promise and the functions that settle it, so a test can look at the
// button while the server works.
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((done, fail) => {
    resolve = done;
    reject = fail;
  });

  return { promise, resolve, reject };
}

function openSettings({ plan = PLAN, draftId = '' } = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.draftId = draftId;
  store.planStorageCleanup = vi.fn(async () => plan);
  store.cleanUpStorage = vi.fn(async () => ({
    ...plan,
    open: plan.drafts.includes(draftId),
  }));

  const wrapper = mount(BuilderSettings, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

const button = (wrapper) => wrapper.get('[data-testid="settings-cleanup"]');
const status = (wrapper) =>
  wrapper.get('[data-testid="settings-status"]').text();
const confirm = () => document.querySelector('[data-testid="builder-confirm"]');
const confirmButton = (testid) =>
  confirm().querySelector(`[data-testid="${testid}"]`);
const question = () =>
  document.getElementById(confirm().getAttribute('aria-describedby'))
    .textContent;

describe('Clean up… in the Builder settings', () => {
  test('is a button under Storage that says what it deletes', () => {
    const { wrapper } = openSettings();
    const headings = wrapper.findAll('h3').map((heading) => heading.text());
    const cleanup = button(wrapper);

    expect(headings.at(-1)).toBe('Storage');
    expect(cleanup.text()).toBe('Clean up…');
    expect(cleanup.attributes('aria-haspopup')).toBe('dialog');
    expect(
      document.getElementById(cleanup.attributes('aria-describedby'))
        .textContent,
    ).toMatch(
      /^\s*Deletes the drafts of all users that are published and unchanged, and\s+the data the server stores that nothing uses\. Published topologies\s+stay\. The server holds at most 2000 drafts\.\s*$/,
    );
  });

  test('says so when there is nothing to clean up, and asks nothing', async () => {
    const { wrapper, store } = openSettings({
      plan: { drafts: [], documents: 0, content: 0 },
    });

    await button(wrapper).trigger('click');
    await flushPromises();

    expect(store.planStorageCleanup).toHaveBeenCalledTimes(1);
    expect(confirm()).toBeNull();
    expect(status(wrapper)).toBe('There is nothing to clean up.');
    expect(store.cleanUpStorage).not.toHaveBeenCalled();
  });

  test('asks first, with what it would delete, and Cancel deletes nothing', async () => {
    const { wrapper, store } = openSettings();
    const pending = deferred();

    store.planStorageCleanup.mockReturnValueOnce(pending.promise);
    button(wrapper).element.focus();
    await button(wrapper).trigger('click');

    // The spinner shows while the server checks, and the button keeps focus.
    expect(button(wrapper).attributes('aria-busy')).toBe('true');
    expect(button(wrapper).attributes('aria-label')).toBe('Checking storage');
    expect(document.activeElement).toBe(button(wrapper).element);

    pending.resolve(PLAN);
    await flushPromises();

    expect(button(wrapper).attributes('aria-busy')).toBeUndefined();
    expect(question()).toBe(
      'Delete 2 published, unchanged drafts and 3 unused stored items? They can be drafts of any user. Their published topologies stay.',
    );
    // Focus starts on the button that deletes nothing.
    expect(document.activeElement).toBe(confirmButton('confirm-cancel'));

    confirmButton('confirm-cancel').click();
    await flushPromises();

    expect(confirm()).toBeNull();
    expect(store.cleanUpStorage).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(button(wrapper).element);
  });

  test('cleans up once confirmed, with a spinner, and says what it deleted', async () => {
    const { wrapper, store } = openSettings();
    const pending = deferred();

    store.cleanUpStorage.mockReturnValueOnce(pending.promise);
    await button(wrapper).trigger('click');
    await flushPromises();
    confirmButton('confirm-accept').click();
    await flushPromises();

    expect(confirm()).toBeNull();
    expect(store.cleanUpStorage).toHaveBeenCalledTimes(1);
    expect(button(wrapper).attributes('aria-busy')).toBe('true');
    expect(button(wrapper).attributes('aria-label')).toBe(
      'Cleaning up storage',
    );

    // A press while busy does nothing.
    await button(wrapper).trigger('click');
    expect(store.planStorageCleanup).toHaveBeenCalledTimes(1);

    pending.resolve({ drafts: ['d1'], documents: 0, content: 4, open: false });
    await flushPromises();

    expect(button(wrapper).attributes('aria-busy')).toBeUndefined();
    expect(status(wrapper)).toBe('Deleted 1 draft and 4 unused stored items.');
  });

  test('names the open draft when it is one of the drafts', async () => {
    const { wrapper } = openSettings({ draftId: 'd2' });

    await button(wrapper).trigger('click');
    await flushPromises();

    expect(question()).toBe(
      'Delete 2 published, unchanged drafts and 3 unused stored items? The open draft is one of them. They can be drafts of any user. Their published topologies stay.',
    );

    confirmButton('confirm-accept').click();
    await flushPromises();

    expect(status(wrapper)).toBe(
      'Deleted 2 drafts and 3 unused stored items. The open draft was one of them.',
    );
  });

  test('asks of unused stored items alone without naming topologies', async () => {
    const { wrapper } = openSettings({
      plan: { drafts: [], documents: 0, content: 1 },
    });

    await button(wrapper).trigger('click');
    await flushPromises();

    expect(question()).toBe('Delete 1 unused stored item?');
  });

  test('says when the open draft must save first, or a part failed', async () => {
    const { wrapper, store } = openSettings({ draftId: 'd1' });

    store.cleanUpStorage.mockResolvedValueOnce(null);
    await button(wrapper).trigger('click');
    await flushPromises();
    confirmButton('confirm-accept').click();
    await flushPromises();

    expect(status(wrapper)).toBe(
      'Clean up deleted nothing, because your latest changes are not saved yet. Wait for the save to finish, or select Retry saving.',
    );
    expect(button(wrapper).attributes('aria-busy')).toBeUndefined();

    store.cleanUpStorage.mockResolvedValueOnce({
      drafts: ['d2'],
      documents: 0,
      content: 0,
      incomplete: true,
      open: false,
    });
    await button(wrapper).trigger('click');
    await flushPromises();
    confirmButton('confirm-accept').click();
    await flushPromises();

    expect(status(wrapper)).toBe(
      'Deleted 1 draft. Clean up could not remove some stored data. Try again later.',
    );
  });

  test('says when a draft it would delete has changes not saved yet', async () => {
    const { wrapper, store } = openSettings();

    for (const [unsent, said] of [
      [1, 'a draft that it would delete has changes'],
      [2, '2 drafts that it would delete have changes'],
    ]) {
      store.cleanUpStorage.mockResolvedValueOnce({ unsent });
      await button(wrapper).trigger('click');
      await flushPromises();
      confirmButton('confirm-accept').click();
      await flushPromises();

      expect(status(wrapper)).toBe(
        `Clean up deleted nothing, because ${said} on this device that are not saved yet. Wait for the saves to finish, and try again.`,
      );
    }
  });

  test('says why a check or a cleanup failed', async () => {
    const { wrapper, store } = openSettings();
    const refused = Object.assign(new Error('forbidden'), {
      response: { status: 403, data: {} },
    });

    store.planStorageCleanup.mockRejectedValueOnce(refused);
    await button(wrapper).trigger('click');
    await flushPromises();

    expect(confirm()).toBeNull();
    expect(status(wrapper)).toBe(
      'Could not check the storage. Your role does not include the Builder.',
    );

    store.cleanUpStorage.mockRejectedValueOnce(
      Object.assign(new Error('offline'), {}),
    );
    await button(wrapper).trigger('click');
    await flushPromises();
    confirmButton('confirm-accept').click();
    await flushPromises();

    expect(status(wrapper)).toBe(
      'Could not clean up the storage. The Builder could not reach the server. Check the connection and try again.',
    );
    expect(button(wrapper).attributes('aria-busy')).toBeUndefined();
  });
});
