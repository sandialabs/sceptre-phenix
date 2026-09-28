// Leaving a draft (Back to drafts, another page, Upload, a reload) with
// edits the Inspector has not applied, or the server does not have yet.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import ExportDialog from '@/components/builder/dialogs/ExportDialog.vue';
import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';

import { SAVED_UNAPPLIED, savedAutomatically } from '@/builder/history.js';
import {
  LEAVE_SAVE_WAIT_MS,
  createLeaveGuard,
  unappliedBlock,
  unappliedText,
} from '@/builder/leave.js';
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
} = {}) {
  const store = {
    readOnly: false,
    historyVersion: 0,
    saveState: { status: pending ? 'idle' : 'saved', pending },
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
    editing: () => true,
    saveUnapplied,
    ask,
    sessionOver: () => session.over,
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

  test('marks the snapshots of edits saved automatically, and says what that means', async () => {
    const html = await renderDialog(HistoryDialog, {}, (store) => {
      store.serverHistory = history;
    });
    const list = /<ol[^>]*data-testid="history-list"[^>]*>[\s\S]*?<\/ol>/.exec(
      html,
    )[0];
    const buttons = [
      ...list.matchAll(/<button\b[^>]*>([\s\S]*?)<\/button>/g),
    ].map(([, inner]) =>
      inner
        .replace(/<[^>]+>/g, '')
        .replace(/\s+/g, ' ')
        .trim(),
    );

    expect(buttons).toHaveLength(3);
    expect(buttons[1]).toMatch(
      /^Restore Saved unapplied changes to Device alpha, .+, Automatic$/,
    );
    expect(buttons[0]).not.toContain('Automatic');
    expect(buttons[2]).not.toContain('Automatic');
    expect(html.match(/data-testid="history-automatic"/g)).toHaveLength(1);
    expect(html).toContain('data-testid="history-automatic-hint"');
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
