// @vitest-environment happy-dom
// Draft History while a snapshot is restored: a ring in place of its row's
// Restore icon, the other actions wait, and the dialog closes after.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

describe('Draft History', () => {
  test('restoring a snapshot shows a ring on its Restore, which keeps its name and focus', async () => {
    const pinia = createPinia();
    const store = useBuilderStore(pinia);
    let restored;

    vi.spyOn(store, 'fetchHistory').mockResolvedValue();
    const restore = vi.spyOn(store, 'restoreSnapshot').mockImplementation(
      () =>
        new Promise((resolve) => {
          restored = resolve;
        }),
    );

    store.serverHistory = [
      { id: 's1', summary: '', createdAt: '2026-09-27T10:00:00Z' },
      {
        id: 's2',
        summary: 'Added device',
        createdAt: '2026-09-27T10:01:00Z',
        current: true,
      },
    ];

    const wrapper = mount(HistoryDialog, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });
    // Newest first: the current snapshot, then the first one.
    const [current, first] = wrapper.findAll('[data-testid="history-restore"]');
    const name = first.attributes('aria-label');

    first.element.focus();
    await first.trigger('click');

    expect(restore).toHaveBeenCalledExactlyOnceWith('s1');
    expect(first.attributes('aria-busy')).toBe('true');
    expect(first.find('[data-testid="builder-spinner"]').exists()).toBe(true);
    expect(first.find('.builder-icon--restore').exists()).toBe(false);
    expect(first.attributes('aria-label')).toBe(name);
    expect(document.activeElement).toBe(first.element);
    expect(current.attributes('aria-busy')).toBeUndefined();
    for (const other of wrapper.findAll('[data-testid="history-delete"]')) {
      expect(other.attributes('aria-disabled')).toBe('true');
    }
    expect(wrapper.get('[data-testid="history-status"]').text()).toBe(
      'Restoring the snapshot…',
    );

    // A second press restores nothing more.
    await first.trigger('click');
    expect(restore).toHaveBeenCalledOnce();

    restored(true);
    await flushPromises();
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});
