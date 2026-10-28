// @vitest-environment happy-dom
// The notice of the newer snapshots in the editor (BuilderNewerHistory.vue),
// and what the History dialog's Restore says of them (HistoryDialog.vue).

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { markRaw } from 'vue';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderNewerHistory from '@/components/builder/BuilderNewerHistory.vue';
import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
import { formatTimestamp } from '@/builder/format.js';
import { useBuilderStore } from '@/builder/store.js';

enableAutoUnmount(afterEach);

const NEWEST = {
  id: 's3',
  createdBy: 'bob',
  createdAt: '2026-10-26T10:00:00Z',
  summary: 'Added device',
};
const HISTORY = [
  { id: 's1', createdBy: 'alice', createdAt: '2026-10-26T09:00:00Z' },
  {
    id: 's2',
    createdBy: 'alice',
    createdAt: '2026-10-26T09:30:00Z',
    summary: 'Added note',
  },
  NEWEST,
];

let pinia;
let store;

beforeEach(() => {
  pinia = createPinia();
  store = useBuilderStore(pinia);
});

// The open draft's editor shows snapshot s1 of three: the next change
// removes two.
function behind({ listed = true } = {}) {
  store.autosave = markRaw({});
  store.draftId = 'd1';
  store.draftHead = { snapshotId: 's1', cursor: 0, snapshots: 3 };
  store.serverHistory = listed
    ? HISTORY.map((entry) => ({ ...entry, current: entry.id === 's1' }))
    : [];
  store.history.currentEntry().serverSnapshotId = 's1';
  store.historyChanged();
}

function mountNotice() {
  return mount(BuilderNewerHistory, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });
}

const TEXT = `Your next change removes 2 newer snapshots (latest by bob, ${formatTimestamp(NEWEST.createdAt)}).`;

describe('the notice of the newer snapshots', () => {
  test('shows nothing while the draft shows its newest snapshot', () => {
    const wrapper = mountNotice();

    expect(wrapper.find('[data-testid="builder-newer-history"]').exists()).toBe(
      false,
    );
  });

  test('says how many, who saved the newest and when, once', async () => {
    behind();
    const said = store.announcementSeq;
    const wrapper = mountNotice();

    await flushPromises();

    expect(wrapper.get('[data-testid="newer-history-text"]').text()).toBe(TEXT);
    expect(store.announcement).toBe(TEXT);
    expect(store.announcementSlot).toBe('newer');
    expect(store.announcementSeq).toBe(said + 1);

    // One undo more changes the count, and says nothing again.
    store.draftHead = { snapshotId: 's1', cursor: 0, snapshots: 4 };
    store.serverHistory = [...store.serverHistory, { id: 's4' }];
    await flushPromises();

    expect(wrapper.get('[data-testid="newer-history-text"]').text()).toMatch(
      /^Your next change removes 3 newer snapshots/,
    );
    expect(store.announcementSeq).toBe(said + 1);

    // Its buttons are in the Tab order, and not live.
    const buttons = wrapper.findAll('button');

    expect(buttons.map((button) => button.text())).toEqual([
      'Return to latest',
      'Save newer history as a new draft',
    ]);
    expect(buttons.every((button) => !button.attributes('tabindex'))).toBe(
      true,
    );
    expect(wrapper.find('[aria-live]').exists()).toBe(false);
    expect(wrapper.find('[role="status"]').exists()).toBe(false);
  });

  test('an undo hides it until its answer comes, and it is not said again', async () => {
    const list = ['s1', 's2', 's3', 's4', 's5'].map((id) => ({
      id,
      createdBy: 'bob',
      createdAt: NEWEST.createdAt,
    }));

    // The editor shows s4 of five snapshots, and shows the snapshot `id`
    // before the server's cursor is on it.
    function show(id, cursor = list.findIndex((entry) => entry.id === id)) {
      store.history.currentEntry().serverSnapshotId = id;
      store.historyChanged();
      store.draftHead = { snapshotId: list[cursor].id, cursor, snapshots: 5 };
      store.serverHistory = list.map((entry, index) => ({
        ...entry,
        current: index === cursor,
      }));
    }

    behind();
    show('s4');
    const said = store.announcementSeq;
    const wrapper = mountNotice();
    const notice = () =>
      wrapper.find('[data-testid="builder-newer-history"]').exists();

    await flushPromises();
    expect(store.announcementSeq).toBe(said + 1);

    for (const [id, before] of [
      ['s3', 3],
      ['s2', 2],
    ]) {
      show(id, before);
      await flushPromises();
      expect(notice()).toBe(false);

      show(id);
      await flushPromises();
      expect(notice()).toBe(true);
    }

    expect(store.announcementSeq).toBe(said + 1);

    // After the newest shows, the next undo says it again.
    show('s5');
    await flushPromises();
    show('s4');
    await flushPromises();

    expect(store.announcementSeq).toBe(said + 2);
    expect(store.announcement).toMatch(
      /^Your next change removes 1 newer snapshot \(/,
    );
  });

  test('reads the list first when it does not know the newest snapshot', async () => {
    behind({ listed: false });
    const read = vi
      .spyOn(store, 'readNewerHistory')
      .mockImplementation(async () => {
        store.serverHistory = HISTORY;

        return NEWEST;
      });

    mountNotice();
    await flushPromises();

    expect(read).toHaveBeenCalledOnce();
    expect(store.announcement).toBe(TEXT);
  });

  test('a list that does not name the newest is read once, and the count is said', async () => {
    behind({ listed: false });
    const read = vi.spyOn(store, 'readNewerHistory').mockResolvedValue(null);

    mountNotice();
    await flushPromises();
    store.historyChanged();
    await flushPromises();

    expect(read).toHaveBeenCalledOnce();
    expect(store.announcement).toBe(
      'Your next change removes 2 newer snapshots.',
    );
  });

  test('Save newer history as a new draft names the draft, and focus goes to Return to latest', async () => {
    behind();
    let done;
    const save = vi.spyOn(store, 'saveNewerAsDraft').mockImplementation(
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    const wrapper = mountNotice();

    await flushPromises();

    const button = wrapper.get('[data-testid="newer-history-save"]');

    button.element.focus();
    await button.trigger('click');

    expect(save).toHaveBeenCalledOnce();
    expect(button.attributes('aria-busy')).toBe('true');
    expect(
      wrapper
        .get('[data-testid="newer-history-latest"]')
        .attributes('aria-disabled'),
    ).toBe('true');

    // A second press waits for the first.
    await button.trigger('click');
    expect(save).toHaveBeenCalledOnce();

    store.newerSaved = {
      draftId: 'd1',
      snapshotId: 's3',
      name: 'Pump station (copy)',
    };
    done({ id: 'd9' });
    await flushPromises();

    expect(wrapper.find('[data-testid="newer-history-save"]').exists()).toBe(
      false,
    );
    expect(wrapper.get('[data-testid="newer-history-text"]').text()).toBe(
      `${TEXT} The latest is saved as draft Pump station (copy).`,
    );
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="newer-history-latest"]').element,
    );
  });

  test('Return to latest closes the notice and hands focus to the view', async () => {
    behind();
    const back = vi
      .spyOn(store, 'returnToLatest')
      .mockImplementation(async () => {
        store.draftHead = { snapshotId: 's3', cursor: 2, snapshots: 3 };
        store.history.currentEntry().serverSnapshotId = 's3';
        store.historyChanged();

        return true;
      });
    const wrapper = mountNotice();

    await flushPromises();

    const button = wrapper.get('[data-testid="newer-history-latest"]');

    button.element.focus();
    await button.trigger('click');
    await flushPromises();

    expect(back).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="builder-newer-history"]').exists()).toBe(
      false,
    );
    expect(wrapper.emitted('lost-focus')).toHaveLength(1);
  });

  test('a failed action keeps the notice and focus', async () => {
    behind();
    vi.spyOn(store, 'returnToLatest').mockResolvedValue(false);
    const wrapper = mountNotice();

    await flushPromises();

    const button = wrapper.get('[data-testid="newer-history-latest"]');

    button.element.focus();
    await button.trigger('click');
    await flushPromises();

    expect(document.activeElement).toBe(button.element);
    expect(wrapper.emitted('lost-focus')).toBeUndefined();
  });
});

describe('Restore in Draft History', () => {
  test('says how many newer snapshots a change after it removes', async () => {
    vi.spyOn(store, 'fetchHistory').mockResolvedValue();
    store.serverHistory = HISTORY.map((entry) => ({
      ...entry,
      current: entry.id === 's3',
    }));

    const wrapper = mount(HistoryDialog, {
      attachTo: document.body,
      global: { plugins: [pinia] },
    });
    // Newest first: s3 (current), s2, s1.
    const [current, second, first] = wrapper.findAll(
      '[data-testid="history-restore"]',
    );
    const description = (button) =>
      (button.attributes('aria-describedby') || '')
        .split(' ')
        .map((id) => document.getElementById(id)?.textContent.trim())
        .join(' ');

    expect(description(first)).toBe(
      'Your next change after this restore removes 2 newer snapshots.',
    );
    expect(description(second)).toBe(
      'Your next change after this restore removes 1 newer snapshot.',
    );
    expect(description(current)).toBe('This is the current version.');

    const names = wrapper.findAll('[data-testid="history-name"]');

    expect(description(names[2])).toBe(
      'Restores this snapshot. Your next change after this restore removes 2 newer snapshots.',
    );

    // happy-dom has no layout: the button gets a box, so that its tooltip
    // is in view.
    first.element.getBoundingClientRect = () => new DOMRect(0, 0, 28, 28);
    await first.trigger('focus');

    expect(wrapper.get('[data-testid="history-tooltip"]').text()).toBe(
      'Restore. Your next change after this restore removes 2 newer snapshots',
    );
  });
});
