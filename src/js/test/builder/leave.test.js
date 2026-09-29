// Leaving a draft (Back to drafts, another page, Upload, a reload) with
// edits the Inspector has not applied, or the server does not have yet.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import ExportDialog from '@/components/builder/dialogs/ExportDialog.vue';
import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';

import { createAutosave } from '@/builder/autosave.js';
import { SAVED_UNAPPLIED, savedAutomatically } from '@/builder/history.js';
import { createMemoryStore } from '@/builder/idb.js';
import {
  LEAVE_SAVE_WAIT_MS,
  backgroundSaveAnnouncement,
  backgroundSaveCard,
  createBackgroundSaves,
  createLeaveGuard,
  queueBusy,
  unappliedBlock,
  unappliedText,
} from '@/builder/leave.js';
import { endBuilderSession } from '@/builder/session.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// A store whose saves take `saveMs`, and an Inspector holding `unapplied`
// edits: valid ones it applies as an edit, which the save queue counts, or
// ones it cannot apply (`blocked`).
function setup({
  unapplied = false,
  blocked = null,
  pending = 0,
  saveMs = 0,
  saveFails = false,
  answer = true,
  session = { over: false },
  editing = true,
  background = null,
  storageFailed = false,
} = {}) {
  const store = {
    readOnly: false,
    historyVersion: 0,
    saveState: { status: pending ? 'idle' : 'saved', pending, storageFailed },
    saveNow: vi.fn(
      () =>
        new Promise((resolve, reject) => {
          setTimeout(() => {
            if (saveFails) {
              store.saveState = { status: 'error', pending: 1 };
              reject(new Error('down'));

              return;
            }

            store.saveState = { status: 'saved', pending: 0 };
            resolve(store.saveState);
          }, saveMs);
        }),
    ),
  };
  let held = unapplied;
  const saveUnapplied = vi.fn(() => {
    if (held) {
      held = false;
      store.historyVersion += 1;
      store.saveState = { status: 'saving', pending: 1 };
    }

    return blocked;
  });
  const ask = vi.fn(async () => answer);
  const guard = createLeaveGuard({
    store,
    editing: () => editing,
    saveUnapplied,
    ask,
    sessionOver: () => session.over,
    background,
  });

  return { store, guard, saveUnapplied, ask };
}

afterEach(() => {
  vi.useRealTimers();
});

describe('leaving a draft', () => {
  test('with nothing unsaved, it leaves without saving or asking', async () => {
    const { store, guard, ask } = setup();

    await expect(guard.mayLeave()).resolves.toBe(true);
    expect(store.saveNow).not.toHaveBeenCalled();
    expect(ask).not.toHaveBeenCalled();
  });

  test('unapplied edits are saved and sent before it leaves', async () => {
    const { store, guard, saveUnapplied, ask } = setup({ unapplied: true });
    const saving = vi.fn();

    await expect(guard.mayLeave({ saving })).resolves.toBe(true);
    expect(saveUnapplied).toHaveBeenCalledTimes(1);
    expect(saving).toHaveBeenCalledTimes(1);
    expect(store.saveNow).toHaveBeenCalledTimes(1);
    expect(ask).not.toHaveBeenCalled();
  });

  test('a save that does not land in time asks, since the server lacks it', async () => {
    vi.useFakeTimers();
    const { guard, ask } = setup({
      unapplied: true,
      saveMs: LEAVE_SAVE_WAIT_MS * 2,
      answer: false,
    });
    const left = guard.mayLeave();

    await vi.advanceTimersByTimeAsync(LEAVE_SAVE_WAIT_MS);

    await expect(left).resolves.toBe(false);
    expect(ask).toHaveBeenCalledWith(null);
  });

  test('a failed save asks', async () => {
    const { guard, ask } = setup({ unapplied: true, saveFails: true });

    await expect(guard.mayLeave()).resolves.toBe(true);
    expect(ask).toHaveBeenCalledWith(null);
  });

  test('a link is followed as the draft is left, but not held up once the session is over', async () => {
    const blocked = { title: 'Device alpha', fields: ['Memory'] };
    const session = { over: false };
    const { guard, saveUnapplied, ask } = setup({
      blocked,
      pending: 2,
      answer: false,
      session,
    });

    await expect(guard.mayFollowLink()).resolves.toBe(false);
    expect(ask).toHaveBeenCalledTimes(1);

    // Logged out, or the token expired: the logout's warning covers the
    // same work, so the Builder does not ask as well.
    session.over = true;
    expect(guard.mayFollowLink()).toBe(true);
    expect(ask).toHaveBeenCalledTimes(1);
    expect(saveUnapplied).toHaveBeenCalledTimes(1);
  });

  test('edits that cannot be applied ask, naming their fields', async () => {
    const blocked = { title: 'Device alpha', fields: ['Memory'] };
    const { store, guard, ask } = setup({ blocked, answer: false });

    await expect(guard.mayLeave()).resolves.toBe(false);
    expect(ask).toHaveBeenCalledWith(blocked);
    // Nothing was applied, so there was nothing to wait for.
    expect(store.saveNow).not.toHaveBeenCalled();
  });

  test('asked twice at once, it saves and asks once', async () => {
    const blocked = { title: 'Device alpha', fields: ['Memory'] };
    const { guard, saveUnapplied, ask } = setup({ blocked });

    const [first, second] = await Promise.all([
      guard.mayLeave(),
      guard.mayLeave(),
    ]);

    expect([first, second]).toEqual([true, true]);
    expect(saveUnapplied).toHaveBeenCalledTimes(1);
    expect(ask).toHaveBeenCalledTimes(1);
  });

  test('a read-only draft has no unsaved work', () => {
    const { store, guard } = setup({ pending: 2 });

    expect(guard.unsavedWork()).toBe(true);
    store.readOnly = true;
    expect(guard.unsavedWork()).toBe(false);
  });
});

describe('closing a draft for the drafts', () => {
  test('the queue is not waited for: it goes on sending in the background', async () => {
    const { store, guard, saveUnapplied, ask } = setup({
      unapplied: true,
      pending: 2,
      saveMs: LEAVE_SAVE_WAIT_MS * 2,
    });

    await expect(guard.mayClose({ saving: vi.fn() })).resolves.toBe(true);
    expect(saveUnapplied).toHaveBeenCalledTimes(1);
    expect(store.saveNow).not.toHaveBeenCalled();
    expect(ask).not.toHaveBeenCalled();
  });

  test('edits that cannot be applied still ask', async () => {
    const blocked = { title: 'Device alpha', fields: ['Memory'] };
    const { guard, ask } = setup({ blocked, pending: 1, answer: false });

    await expect(guard.mayClose()).resolves.toBe(false);
    expect(ask).toHaveBeenCalledWith(blocked, 'close');
  });

  test('a queue this device cannot keep is sent first, as the page would lose it', async () => {
    const { store, guard } = setup({ unapplied: true, storageFailed: true });

    await expect(guard.mayClose()).resolves.toBe(true);
    expect(store.saveNow).toHaveBeenCalledTimes(1);
  });
});

describe('leaving the Builder while drafts closed before are being saved', () => {
  function behind({ pending = 1, drains = true } = {}) {
    const background = {
      left: pending,
      pending: () => background.left,
      flush: vi.fn(async () => {
        if (drains) {
          background.left = 0;
        } else {
          await new Promise(() => {});
        }
      }),
      keepForUnload: vi.fn(() => true),
    };

    return background;
  }

  test('waits for their saves, and asks while the server lacks some', async () => {
    const saved = behind();
    const first = setup({ editing: false, background: saved });

    await expect(first.guard.mayFollowLink()).resolves.toBe(true);
    expect(saved.flush).toHaveBeenCalledTimes(1);
    expect(first.ask).not.toHaveBeenCalled();

    vi.useFakeTimers();
    const stuck = behind({ pending: 2, drains: false });
    const second = setup({ editing: false, background: stuck, answer: false });
    const left = second.guard.mayFollowLink();

    await vi.advanceTimersByTimeAsync(LEAVE_SAVE_WAIT_MS);
    await expect(left).resolves.toBe(false);
    expect(second.ask).toHaveBeenCalledWith(null, 'all');
  });

  test('Upload, which leaves only the open draft, does not wait for them', async () => {
    const stuck = behind({ drains: false });
    const { guard, ask } = setup({ background: stuck });

    await expect(guard.mayLeave()).resolves.toBe(true);
    expect(stuck.flush).not.toHaveBeenCalled();
    expect(ask).not.toHaveBeenCalled();
  });

  test('closing the tab keeps what they may not have stored, and the browser asks', () => {
    const sending = behind();
    const { guard } = setup({ editing: false, background: sending });
    const event = { preventDefault: vi.fn(), returnValue: undefined };

    expect(guard.beforeUnload(event)).toBe(true);
    expect(sending.keepForUnload).toHaveBeenCalledTimes(1);
    expect(event.preventDefault).toHaveBeenCalled();
  });
});

describe('saves in the background', () => {
  const doc = { name: 'Lab', nodes: [], edges: [] };

  // A queue of a draft being edited, whose saves wait for `release` while
  // `holding`.
  async function editedQueue({ online = true } = {}) {
    const gate = { holding: false, waiting: [] };
    const api = {
      appendSnapshot: vi.fn(async () => {
        if (gate.holding) {
          await new Promise((resolve) => gate.waiting.push(resolve));
        }

        return { draft: { snapshotId: 's2' }, etag: '"2"' };
      }),
    };
    const device = createMemoryStore();
    const queue = createAutosave({
      api,
      store: device,
      actor: 'alice',
      isOnline: () => online,
      setTimeout: () => 0,
      clearTimeout: () => {},
    });

    await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    gate.release = () => {
      gate.holding = false;
      gate.waiting.splice(0).forEach((resolve) => resolve());
    };

    return { queue, api, device, gate };
  }

  function saves() {
    const seen = { cards: {}, said: [], saved: 0 };
    const background = createBackgroundSaves({
      onChange: (cards) => {
        seen.cards = cards;
      },
      announce: (message) => seen.said.push(message),
      saved: () => {
        seen.saved += 1;
      },
    });

    return { background, seen };
  }

  test('go on once the draft is closed, and its card says how, until they are done', async () => {
    const { queue, api, gate } = await editedQueue();
    const { background, seen } = saves();

    gate.holding = true;
    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    const second = queue.commit({ id: 'c2', label: 'two', snapshot: doc });

    await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalled());
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });

    expect(seen.cards).toEqual({
      'alice/d1': { kind: 'saving', text: 'Saving 2 changes…', pending: 2 },
    });
    expect(background.pending()).toBe(2);
    expect(seen.said).toEqual([]);

    gate.release();
    await second;
    await queue.idle();

    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(seen.cards['alice/d1']).toEqual({
      kind: 'saved',
      text: 'All changes saved.',
      pending: 0,
    });
    expect(seen.said).toEqual(['Saved your changes to Lab.']);
    expect(seen.saved).toBe(1);
    expect(background.pending()).toBe(0);
  });

  test('the card counts down as each change is saved', async () => {
    const { queue, api, gate } = await editedQueue();
    const { background, seen } = saves();

    gate.holding = true;
    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    queue.commit({ id: 'c2', label: 'two', snapshot: doc });
    const last = queue.commit({ id: 'c3', label: 'three', snapshot: doc });

    await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalled());
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });
    expect(seen.cards['alice/d1'].text).toBe('Saving 3 changes…');

    gate.waiting.shift()();
    await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalledTimes(2));
    expect(seen.cards['alice/d1']).toEqual({
      kind: 'saving',
      text: 'Saving 2 changes…',
      pending: 2,
    });

    gate.release();
    await last;
    await queue.idle();
    expect(seen.cards['alice/d1'].kind).toBe('saved');
  });

  // The edit Back to drafts applies (the Inspector's) is queued as the
  // draft closes, before the local store has it.
  test('an edit queued as the draft closes is sent too', async () => {
    const { queue, api } = await editedQueue();
    const { background, seen } = saves();

    const committing = queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(queueBusy(queue)).toBe(true);
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });
    expect(seen.cards['alice/d1']).toMatchObject({
      kind: 'saving',
      pending: 1,
    });

    await committing;
    await queue.idle();
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(seen.cards['alice/d1'].kind).toBe('saved');
  });

  test('offline, the card says the changes are kept; a problem only the user can solve ends them', async () => {
    const { queue } = await editedQueue({ online: false });
    const { background, seen } = saves();

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });
    expect(seen.cards['alice/d1']).toEqual({
      kind: 'retrying',
      text: 'Offline: 1 change kept on this device. Saving retries automatically.',
      pending: 1,
    });

    queue.conflict();
    expect(seen.cards['alice/d1']).toMatchObject({
      kind: 'stopped',
      text: 'Not saved: this draft changed on the server. Open it to keep your changes.',
      pending: 0,
    });
    expect(seen.said).toEqual([
      'Could not save your changes to Lab. Open it to see why.',
    ]);
    expect(background.pending()).toBe(0);
  });

  test("opening the draft again ends them once the send under way settles; the draft's queue finds the rest", async () => {
    const { queue, api, device, gate } = await editedQueue();
    const { background, seen } = saves();

    gate.holding = true;
    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    queue.commit({ id: 'c2', label: 'two', snapshot: doc });
    await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalled());
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });

    const released = background.release('alice', 'd1');

    expect(seen.cards).toEqual({});
    gate.release();
    await released;

    // The one under way was sent; the other is left to the draft's queue.
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    const reopened = createAutosave({ api, store: device, actor: 'alice' });

    await reopened.attach({ owner: 'alice', draftId: 'd1', etag: '"2"' });
    expect(reopened.record.queue.map((op) => op.opId)).toEqual(['c2']);
  });

  test('end with the session', async () => {
    const { queue } = await editedQueue({ online: false });
    const { background, seen } = saves();

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    background.take(queue, { owner: 'alice', id: 'd1', name: 'Lab' });
    await endBuilderSession({
      localStorage: null,
      sessionStorage: null,
      clearDatabase: async () => true,
    });

    expect(seen.cards).toEqual({});
    expect(background.pending()).toBe(0);
  });

  test('cards say what each state means for the changes', () => {
    const card = (state) => backgroundSaveCard({ pending: 2, ...state });

    expect(card({ status: 'saving' }).text).toBe('Saving 2 changes…');
    expect(
      card({
        status: 'error',
        retryable: true,
        message:
          'Could not save your changes. Down. Saving retries automatically.',
      }),
    ).toMatchObject({
      kind: 'retrying',
      text: 'Could not save your changes. Down. Saving retries automatically.',
    });
    expect(card({ status: 'error', retryable: false })).toMatchObject({
      kind: 'stopped',
      text: 'Not saved: 2 changes kept on this device. Open the draft to see why.',
    });
    // The session ended: nothing retries until the user signs in again,
    // which sends them.
    expect(
      card({ status: 'error', retryable: true, signInNeeded: true }),
    ).toEqual({
      kind: 'signin',
      text: 'Not saved yet: sign in again to save 2 changes.',
      stop: false,
    });
    expect(backgroundSaveAnnouncement('signin', 'Lab')).toBe(
      'Your changes to Lab are not saved yet: sign in again to save them.',
    );
    expect(card({ status: 'conflict', otherTab: true }).text).toBe(
      "Not saved: you chose another tab's changes. Open it to keep yours as a new draft.",
    );
    expect(card({ status: 'forbidden' }).text).toBe(
      'Not saved: you can no longer save changes to this draft. Open it to keep a copy.',
    );
    expect(card({ status: 'idle', heldForTabs: true })).toMatchObject({
      kind: 'waiting',
      stop: false,
    });
    expect(card({ status: 'saved', pending: 0 })).toMatchObject({
      kind: 'saved',
      stop: true,
    });
  });
});

describe('reloading or closing the tab', () => {
  function unload() {
    return { preventDefault: vi.fn(), returnValue: undefined };
  }

  test('with nothing unsaved, the browser does not ask', () => {
    const { guard } = setup();
    const event = unload();

    expect(guard.beforeUnload(event)).toBe(false);
    expect(event.preventDefault).not.toHaveBeenCalled();
  });

  // It cannot wait for the save, so the browser asks while it is sent.
  test('unapplied edits are applied at once, and the browser asks', () => {
    const { store, guard, saveUnapplied } = setup({ unapplied: true });
    const event = unload();

    expect(guard.beforeUnload(event)).toBe(true);
    expect(saveUnapplied).toHaveBeenCalledTimes(1);
    expect(store.historyVersion).toBe(1);
    expect(event.preventDefault).toHaveBeenCalled();
    expect(event.returnValue).toBe('');
  });

  test('edits that cannot be applied make the browser ask', () => {
    const { guard } = setup({
      blocked: { title: 'Device alpha', fields: ['Memory'] },
    });

    expect(guard.beforeUnload(unload())).toBe(true);
  });

  test('queued edits make the browser ask', () => {
    const { guard } = setup({ pending: 1 });

    expect(guard.beforeUnload(unload())).toBe(true);
  });

  // The local store's write of the edit just applied finishes after the
  // page may be gone, so what it may not hold is copied at once.
  test('the edit applied is copied at once, before the page goes', () => {
    const { store, guard, saveUnapplied } = setup({ unapplied: true });
    const keepForUnload = vi.fn(() => {
      expect(saveUnapplied).toHaveBeenCalled();

      return true;
    });

    store.autosave = { keepForUnload };

    expect(guard.beforeUnload(unload())).toBe(true);
    expect(keepForUnload).toHaveBeenCalledTimes(1);
  });

  test('a copy that does not fit makes the browser ask, so the user can stay', () => {
    const { store, guard } = setup();

    store.autosave = { keepForUnload: () => false };
    const event = unload();

    expect(guard.beforeUnload(event)).toBe(true);
    expect(event.preventDefault).toHaveBeenCalled();

    store.autosave = { keepForUnload: () => true };
    expect(guard.beforeUnload(unload())).toBe(false);
  });
});

describe('what leaving, Publish and Export say', () => {
  test('the fields that keep edits unsaved are named', () => {
    expect(unappliedText({ title: 'Device alpha', fields: ['Memory'] })).toBe(
      'Your changes to Device alpha in the Inspector cannot be saved until Memory is fixed.',
    );
    expect(
      unappliedText({
        title: 'Device alpha',
        fields: ['Memory', 'MAC address (Interface 1)'],
      }),
    ).toBe(
      'Your changes to Device alpha in the Inspector cannot be saved until Memory and MAC address (Interface 1) are fixed.',
    );
  });

  test('Publish and Export say what blocks them', () => {
    const unapplied = { title: 'Device alpha', fields: ['Memory'] };

    expect(unappliedBlock(unapplied, 'published')).toBe(
      'Your changes to Device alpha in the Inspector cannot be published until Memory is fixed. Fix or cancel them first.',
    );
    expect(unappliedBlock(unapplied, 'exported')).toMatch(
      /cannot be exported until Memory is fixed\./,
    );
  });
});

// Renders a dialog for the sample diagram, with `prepare` setting up the
// store first.
async function renderDialog(component, props = {}, prepare = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);

  const store = useBuilderStore(pinia);

  store.doc = sampleDocument().doc;
  prepare(store);

  return renderToString(app);
}

// Each opening tag of `name` in `html`.
function tags(html, name) {
  return html.match(new RegExp(`<${name}\\b[^>]*>`, 'g')) || [];
}

describe('Publish and Export with edits the Inspector cannot apply', () => {
  const unapplied = { title: 'Device alpha', fields: ['Memory'] };

  test('Publish says what blocks it, and cannot be sent', async () => {
    const html = await renderDialog(PublishDialog, { unapplied });
    const [submit] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="publish-submit"'),
    );

    expect(html).toContain('aria-describedby="publish-unapplied"');
    expect(html).toContain(unappliedBlock(unapplied, 'published'));
    expect(submit).toMatch(/\sdisabled\b/);
  });

  test('Publish without them is not blocked by them', async () => {
    const html = await renderDialog(PublishDialog);

    expect(html).not.toContain('publish-unapplied');
  });

  test('Export says what blocks it, and makes no export', async () => {
    const html = await renderDialog(ExportDialog, { unapplied });
    const exports = tags(html, 'button').filter((tag) =>
      /data-testid="export-(json|yaml|png|svg)"/.test(tag),
    );

    expect(html).toContain('aria-describedby="export-unapplied"');
    expect(html).toContain(unappliedBlock(unapplied, 'exported'));
    expect(exports).toHaveLength(4);
    expect(exports.filter((tag) => !/\sdisabled\b/.test(tag))).toEqual([]);
  });

  test('Export without them makes every export', async () => {
    const html = await renderDialog(ExportDialog);
    const disabled = tags(html, 'button').filter((tag) =>
      /\sdisabled\b/.test(tag),
    );

    expect(html).not.toContain('export-unapplied');
    expect(disabled).toEqual([]);
  });
});

describe('the History dialog', () => {
  const history = [
    { id: 's1', createdAt: '2026-09-27T10:00:00Z' },
    {
      id: 's2',
      summary: `${SAVED_UNAPPLIED} to Device alpha`,
      createdAt: '2026-09-27T10:01:00Z',
    },
    {
      id: 's3',
      summary: 'Applied changes to Device alpha',
      createdAt: '2026-09-27T10:02:00Z',
    },
  ];

  // The text of each cell of each body row.
  function cells(html) {
    const body = /<tbody\b[^>]*>([\s\S]*?)<\/tbody>/.exec(html)[1];

    return [...body.matchAll(/<tr\b[^>]*>([\s\S]*?)<\/tr>/g)].map(([, row]) =>
      [...row.matchAll(/<td\b[^>]*>([\s\S]*?)<\/td>/g)].map(([, cell]) =>
        cell
          .replace(/<[^>]+>/g, '')
          .replace(/\s+/g, ' ')
          .trim(),
      ),
    );
  }

  test('marks the snapshots of edits saved automatically, and says what that means', async () => {
    const html = await renderDialog(HistoryDialog, {}, (store) => {
      store.serverHistory = history;
    });
    const names = cells(html).map((row) => row[1]);

    expect(names).toHaveLength(3);
    expect(names[1]).toBe('Saved unapplied changes to Device alpha, Automatic');
    expect(names[0]).toBe('Applied changes to Device alpha');
    expect(names[2]).toBe('Draft created');
    expect(html.match(/data-testid="history-automatic"/g)).toHaveLength(1);
    expect(html).toContain('data-testid="history-automatic-hint"');
  });

  test('is a table of number, name, date and user, newest first, with actions named for their row', async () => {
    const html = await renderDialog(HistoryDialog, {}, (store) => {
      store.serverHistory = history.map((entry, index) => ({
        ...entry,
        createdBy: index === 2 ? 'bob' : 'alice',
        current: index === 1,
      }));
    });
    const table = tags(html, 'table')[0];
    const headers = [...html.matchAll(/<th scope="col"[^>]*>([\s\S]*?)<\/th>/g)]
      .map(([, inner]) => inner.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' '))
      .map((text) => text.trim());
    const rows = cells(html);

    expect(tags(html, 'h2')[0]).toContain('id="history-dialog-title"');
    expect(html).toMatch(/<h2 id="history-dialog-title"[^>]*>Draft History</);
    expect(table).toContain('aria-labelledby="history-dialog-title"');
    expect(headers).toEqual(['# Number', 'Name', 'Date', 'User', 'Actions']);
    // Numbered from the oldest, which is the draft as created.
    expect(rows.map((row) => row[0])).toEqual(['3', '2', '1']);
    expect(rows[2][1]).toBe('Draft created');
    expect(rows[0][1]).toBe('Applied changes to Device alpha');
    expect(rows[1][1]).toMatch(/, Current$/);
    expect(rows.map((row) => row[3])).toEqual(['bob', 'alice', 'alice']);
    expect(html).toContain('<time datetime="2026-09-27T10:02:00Z"');

    // The name restores too, from a click: the row's Restore is the
    // keyboard's way.
    const names = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="history-name"'),
    );

    expect(names).toHaveLength(3);
    for (const name of names) {
      expect(name).toContain('tabindex="-1"');
    }
    expect(names[0]).toContain('aria-describedby="history-name-note"');
    expect(names[0]).not.toContain('aria-disabled');
    // The current snapshot is the diagram already: restoring it would do
    // nothing, and its name and Restore say so.
    expect(names[1]).toContain('aria-disabled="true"');
    expect(names[1]).toContain('aria-describedby="history-current-restore"');

    const restore = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="history-restore"'),
    );
    const remove = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="history-delete"'),
    );

    expect(restore).toHaveLength(3);
    expect(restore[0]).toMatch(
      /aria-label="Restore Applied changes to Device alpha, [^"]+"/,
    );
    expect(restore[2]).not.toContain('aria-disabled');
    expect(restore[1]).toContain('aria-disabled="true"');
    expect(restore[1]).toContain('aria-describedby="history-current-restore"');
    expect(html).toMatch(
      /id="history-current-restore" hidden[^>]*>\s*This is the current version\.\s*</,
    );
    expect(remove[2]).toMatch(/aria-label="Delete Draft created, [^"]+"/);
    expect(remove[2]).not.toContain('aria-disabled');
    // The current snapshot cannot be deleted, and its Delete says why.
    expect(remove[1]).toContain('aria-disabled="true"');
    expect(remove[1]).toContain('aria-describedby="history-current-note"');
    expect(html).toMatch(
      /id="history-current-note" hidden[^>]*>The current snapshot cannot be deleted\.</,
    );
  });

  test('a user who may only view the draft gets neither action', async () => {
    const html = await renderDialog(HistoryDialog, {}, (store) => {
      store.serverHistory = history;
      store.readOnly = true;
    });

    expect(html).not.toContain('history-restore');
    expect(html).not.toContain('history-delete');
    expect(tags(html, 'button').join('')).not.toContain('history-name');
    expect(cells(html)[0]).toHaveLength(4);
  });

  test('says nothing of them when none are listed', async () => {
    const html = await renderDialog(HistoryDialog, {}, (store) => {
      store.serverHistory = [history[0], history[2]];
    });

    expect(html).not.toContain('history-automatic');
  });

  test('only a summary that starts with the name is marked', () => {
    expect(savedAutomatically(`${SAVED_UNAPPLIED} to Device alpha`)).toBe(true);
    expect(savedAutomatically('Updated Saved unapplied changes')).toBe(false);
    expect(savedAutomatically(SAVED_UNAPPLIED)).toBe(false);
    expect(savedAutomatically(undefined)).toBe(false);
  });
});
