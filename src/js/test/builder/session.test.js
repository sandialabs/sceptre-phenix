import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
// The app store's sign-in navigates; nothing here needs the real router.
vi.mock('@/router', () => ({ default: { replace: vi.fn() } }));

// The signed-in user, reactive as the real store is, so the permission
// getters follow a role change.
vi.mock('@/store.js', async () => {
  const { reactive } = await import('vue');
  const phenix = reactive({ username: 'alice', role: null });

  return { usePhenixStore: () => phenix };
});

const api = vi.hoisted(() => ({
  listDrafts: vi.fn(),
  listDocuments: vi.fn(),
  getSources: vi.fn(),
  listDisks: vi.fn(),
  getDraft: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import {
  keymapState,
  setShortcut,
  SHORTCUTS_STORAGE_KEY,
} from '@/builder/keymap.js';
import { clearBuilderDatabase, createDraftStore } from '@/builder/idb.js';
import { PANES_STORAGE_KEY } from '@/builder/panes.js';
import { SETTINGS_STORAGE_KEY } from '@/builder/settings.js';
import {
  readRecent,
  rememberCommand,
  RECENT_STORAGE_KEY,
} from '@/builder/recent.js';
import {
  BUILDER_PREFERENCE_KEYS,
  BUILDER_USER_KEY,
  endBuilderSession,
  startBuilderSession,
} from '@/builder/session.js';
import { useBuilderStore } from '@/builder/store.js';
import { THEME_STORAGE_KEY } from '@/builder/theme.js';
import { usePhenixStore } from '@/store.js';

// A Storage with the parts endBuilderSession uses.
function memoryStorage(entries = {}) {
  const map = new Map(Object.entries(entries));

  return {
    map,
    get length() {
      return map.size;
    },
    key: (index) => [...map.keys()][index] ?? null,
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    removeItem: (key) => map.delete(key),
  };
}

function end(options = {}) {
  return endBuilderSession({
    localStorage: memoryStorage(),
    sessionStorage: memoryStorage(),
    clearDatabase: async () => true,
    ...options,
  });
}

let store;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  store = useBuilderStore();
});

describe('logout', () => {
  test("clears what Builder Flow kept here of the user's: keys, lists, recent commands, the open diagram and local drafts, but not the preferences", async () => {
    // The preferences are the modules' own keys, and the recent commands,
    // which name drafts and nodes, are not among them.
    expect([...BUILDER_PREFERENCE_KEYS].sort()).toEqual(
      [
        PANES_STORAGE_KEY,
        SETTINGS_STORAGE_KEY,
        SHORTCUTS_STORAGE_KEY,
        THEME_STORAGE_KEY,
      ].sort(),
    );
    expect(BUILDER_PREFERENCE_KEYS).not.toContain(RECENT_STORAGE_KEY);

    const local = memoryStorage({
      'phenix.builder.theme': 'dark',
      'phenix.builder.panes': '{"start":300}',
      'phenix.builder.settings': '{"layoutAlgorithm":"dagre"}',
      'phenix.builder.unlisted': 'x',
      'phenix.user': 'alice',
      'alice.vimMode': 'true',
    });
    const session = memoryStorage({
      'phenix.builder.draft': 'x',
      'phenix.token': 'token',
    });
    const order = [];
    const dispose = vi.fn(() => order.push('autosave disposed'));
    const clearDatabase = vi.fn(async () => {
      order.push('database cleared');

      return true;
    });

    store.drafts = {
      mine: [{ id: 'd1', owner: 'alice', title: 'Secret lab' }],
      shared: [{ id: 'd2', owner: 'bob', title: 'Shared lab' }],
      published: [],
    };
    store.documents = [{ id: 'p1', target: 'core' }];
    store.sources = {
      images: [],
      topologies: ['core'],
      scenarios: [],
      experiments: [],
    };
    store.published = { id: 'p1', name: 'core', target: 'core' };
    store.readOnly = true;
    store.theme = 'dark';
    store.resolvedTheme = 'dark';
    store.autosave = { dispose };
    setShortcut('palette.open', ['Mod+J'], local);
    rememberCommand('structure.connect', ['device-1', 'switch-1'], local);
    expect(keymapState.overrides).toHaveProperty('palette.open');

    await expect(
      endBuilderSession({
        localStorage: local,
        sessionStorage: session,
        clearDatabase,
      }),
    ).resolves.toBe(true);

    // Only the Builder's keys go, and of those not the preferences; a key
    // not listed as one goes too.
    expect([...local.map.keys()].sort()).toEqual([
      'alice.vimMode',
      'phenix.builder.panes',
      'phenix.builder.settings',
      'phenix.builder.shortcuts',
      'phenix.builder.theme',
      'phenix.user',
    ]);
    expect([...session.map.keys()]).toEqual(['phenix.token']);
    // The queue stops before the database is emptied, so it cannot write
    // the draft back.
    expect(order).toEqual(['autosave disposed', 'database cleared']);
    expect(store.drafts).toEqual({ mine: [], shared: [], published: [] });
    expect(store.documents).toEqual([]);
    expect(store.sources.topologies).toEqual([]);
    expect(store.published).toBeNull();
    expect(store.readOnly).toBe(false);
    expect(store.autosave).toBeNull();
    expect(readRecent()).toEqual([]);
    // The preferences still apply.
    expect(store.theme).toBe('dark');
    expect(store.resolvedTheme).toBe('dark');
    expect(keymapState.overrides['palette.open']).toEqual(['Mod+J']);
  });

  test('a draft that opens after logout is not shown to the next user', async () => {
    let answer;
    api.getDraft.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );

    store.theme = 'light';
    const opening = store.loadDraft('alice', 'd1');
    await end();
    answer({
      draft: { id: 'd1', owner: 'alice' },
      document: store.doc,
      etag: '"1"',
    });

    expect(await opening).toBeNull();
    expect([store.owner, store.draftId, store.autosave]).toEqual([
      '',
      '',
      null,
    ]);
    expect(store.error).toBe('');
    // The theme is a preference, and outlasts the late answer too.
    expect(store.theme).toBe('light');
  });

  test('a listing that answers after logout is not shown to the next user', async () => {
    let answer;
    api.listDrafts.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );

    const listing = store.fetchDrafts();
    await end();
    answer({ mine: [{ id: 'd1', owner: 'alice' }], shared: [], published: [] });
    await listing;

    expect(store.drafts.mine).toEqual([]);
    expect(store.loading).toBe(false);

    // A listing the role may not read shows nothing rather than what an
    // earlier one showed.
    store.drafts = {
      mine: [{ id: 'd1', owner: 'alice' }],
      shared: [],
      published: [],
    };
    store.documents = [{ id: 'p1' }];
    const forbidden = Object.assign(new Error('forbidden'), {
      response: { status: 403, data: { message: 'forbidden' } },
    });
    api.listDrafts.mockRejectedValueOnce(forbidden);
    api.listDocuments.mockRejectedValueOnce(forbidden);

    await store.fetchDrafts();
    await store.fetchDocuments();

    expect(store.drafts.mine).toEqual([]);
    expect(store.documents).toEqual([]);
    expect(store.error).toMatch(/^Could not list published diagrams\./);
  });
});

describe('sign-in', () => {
  // What a user leaves in this browser: preferences, recent commands and
  // an open draft listed in memory.
  function leftBehind(user) {
    store.drafts = {
      mine: [{ id: 'd1', owner: user, title: 'Secret lab' }],
      shared: [],
      published: [],
    };

    return memoryStorage({
      ...(user ? { [BUILDER_USER_KEY]: user } : {}),
      'phenix.builder.theme': 'dark',
      [RECENT_STORAGE_KEY]: '[{"id":"drafts.open","choices":["x"]}]',
    });
  }

  test("clears another user's Builder data before the next user's session starts, but not the preferences", async () => {
    for (const previous of ['alice', null]) {
      const local = leftBehind(previous);
      const clearDatabase = vi.fn(async () => true);

      await expect(
        startBuilderSession('bob', {
          localStorage: local,
          sessionStorage: memoryStorage(),
          clearDatabase,
        }),
      ).resolves.toBe(true);

      expect(clearDatabase, `left by ${previous}`).toHaveBeenCalledTimes(1);
      expect(Object.fromEntries(local.map)).toEqual({
        'phenix.builder.theme': 'dark',
        [BUILDER_USER_KEY]: 'bob',
      });
      expect(store.drafts.mine).toEqual([]);
    }
  });

  test("keeps the user's own Builder data when the same user signs in again", () => {
    const local = leftBehind('alice');
    const clearDatabase = vi.fn(async () => true);

    expect(
      startBuilderSession('alice', { localStorage: local, clearDatabase }),
    ).toBeNull();
    expect(clearDatabase).not.toHaveBeenCalled();
    expect(local.map.get(RECENT_STORAGE_KEY)).toContain('drafts.open');
    expect(store.drafts.mine).toHaveLength(1);
  });

  test('the local drafts database is not opened until a clearing under way has ended', async () => {
    // An IndexedDB whose requests answer when the test says.
    const opened = [];
    const factory = {
      open() {
        const request = {};
        opened.push(request);

        return request;
      },
    };
    const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

    const clearing = clearBuilderDatabase({ factory });
    const reading = createDraftStore({ factory }).all();
    await settle();
    expect(opened, 'opened by the clearing alone').toHaveLength(1);

    const transaction = { objectStore: () => ({ clear() {} }) };
    opened[0].result = { transaction: () => transaction, close() {} };
    opened[0].onsuccess();
    await settle();
    transaction.oncomplete();
    await expect(clearing).resolves.toBe(true);
    await settle();
    expect(opened, 'opened by the store once cleared').toHaveLength(2);

    opened[1].onerror();
    await expect(reading).resolves.toEqual([]);
  });

  test("the app's sign-in starts the Builder session of the user signing in", async () => {
    const local = leftBehind('alice');

    vi.stubGlobal('localStorage', local);
    vi.stubGlobal('sessionStorage', memoryStorage());

    try {
      const { usePhenixStore: useAppStore } =
        await vi.importActual('@/store.js');

      useAppStore().login(
        {
          token: 'token',
          user: { username: 'bob', role: { name: 'Global Admin' } },
        },
        false,
        false,
      );

      expect(local.map.get(BUILDER_USER_KEY)).toBe('bob');
      expect(local.map.has(RECENT_STORAGE_KEY)).toBe(false);
      expect(local.map.get('phenix.builder.theme')).toBe('dark');
      expect(store.drafts.mine).toEqual([]);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('permissions', () => {
  test('follow the signed-in role', () => {
    const phenix = usePhenixStore();

    phenix.role = {
      name: 'Global Viewer',
      policies: [
        { resources: ['*'], resourceNames: ['*'], verbs: ['list', 'get'] },
      ],
    };
    expect(store.canCreateDrafts).toBe(false);
    expect(store.canPublish).toBe(false);
    expect(store.canDeleteDrafts).toBe(false);

    phenix.role = {
      name: 'Global Admin',
      policies: [
        { resources: ['*', '*/*'], resourceNames: ['*', '*/*'], verbs: ['*'] },
      ],
    };
    expect(store.canCreateDrafts).toBe(true);
    expect(store.canPublish).toBe(true);
    expect(store.canDeleteDrafts).toBe(true);

    phenix.role = null;
    expect(store.canCreateDrafts).toBe(false);
  });
});
