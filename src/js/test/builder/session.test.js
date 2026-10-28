// What session.js clears on logout and sign-in.

import { readFileSync, statSync } from 'node:fs';

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { reactive } from 'vue';

// The app store: the signed-in user, which each test sets, reactive as the
// real store is, so the permission getters follow a role change.
const phenix = vi.hoisted(() => ({ state: null }));
const router = vi.hoisted(() => ({ replace: vi.fn() }));

vi.mock('@/router', () => ({ default: router }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix.state }));

const api = vi.hoisted(() => ({
  appendSnapshot: vi.fn(),
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

import { createPinia, setActivePinia } from 'pinia';

import {
  GROUP_PATTERN_STORAGE_KEY,
  readGroupPattern,
  rememberGroupPattern,
} from '@/builder/grouping.js';
import {
  clearBuilderDatabase,
  createDraftStore,
  draftKey,
  unloadCopyKey,
} from '@/builder/idb.js';
import {
  keymapState,
  setShortcut,
  SHORTCUTS_STORAGE_KEY,
} from '@/builder/keymap.js';
import { MINIMAP_STORAGE_KEY, PANES_STORAGE_KEY } from '@/builder/panes.js';
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
import { SETTINGS_STORAGE_KEY } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';
import { THEME_STORAGE_KEY } from '@/builder/theme.js';
import { usePhenixStore } from '@/store.js';

import { memoryStorage } from './fixtures.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('the Builder session', () => {
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
    phenix.state = reactive({ username: 'alice', role: null });
    setActivePinia(createPinia());
    vi.clearAllMocks();
    store = useBuilderStore();
  });

  describe('logout', () => {
    test("clears what Builder kept here of the user's: keys, lists, recent commands, the open diagram and local drafts, but not the preferences", async () => {
      // The preferences are the modules' own keys, and the recent commands,
      // which name drafts and nodes, are not among them.
      expect([...BUILDER_PREFERENCE_KEYS].sort()).toEqual(
        [
          MINIMAP_STORAGE_KEY,
          PANES_STORAGE_KEY,
          SETTINGS_STORAGE_KEY,
          SHORTCUTS_STORAGE_KEY,
          THEME_STORAGE_KEY,
        ].sort(),
      );
      expect(BUILDER_PREFERENCE_KEYS).not.toContain(RECENT_STORAGE_KEY);
      // The Auto-group pattern used last is the user's own text.
      expect(BUILDER_PREFERENCE_KEYS).not.toContain(GROUP_PATTERN_STORAGE_KEY);

      const local = memoryStorage({
        'phenix.builder.theme': 'dark',
        'phenix.builder.panes': '{"start":300}',
        'phenix.builder.minimap': '{"width":280}',
        'phenix.builder.settings': '{"layoutAlgorithm":"dagre"}',
        'phenix.builder.unlisted': 'x',
        [unloadCopyKey(draftKey('alice', 'd1'))]: '{"queue":[]}',
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
        all: [
          { id: 'd1', createdBy: 'alice', title: 'Secret lab' },
          { id: 'd2', createdBy: 'bob', title: 'Their lab' },
        ],
        published: [],
        damaged: [],
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
      rememberGroupPattern('^secret-lab-(\\d+)', local);
      // The layer set library and the server's default set, as read.
      store.layerSets = {
        ...store.layerSets,
        loaded: true,
        items: [{ id: 'lab', name: 'Secret lab', layers: [], version: 1 }],
        defaultId: 'lab',
      };
      expect(readGroupPattern(local)).toBe('^secret-lab-(\\d+)');
      expect(keymapState.overrides).toHaveProperty('palette.open');

      await expect(
        endBuilderSession({
          localStorage: local,
          sessionStorage: session,
          clearDatabase,
        }),
      ).resolves.toBe(true);

      // Only the Builder's keys go, and of those not the preferences. A key
      // not listed as one goes too.
      expect([...local.entries.keys()].sort()).toEqual([
        'alice.vimMode',
        'phenix.builder.minimap',
        'phenix.builder.panes',
        'phenix.builder.settings',
        'phenix.builder.shortcuts',
        'phenix.builder.theme',
        'phenix.user',
      ]);
      expect([...session.entries.keys()]).toEqual(['phenix.token']);
      // The queue stops before the database is emptied, so it cannot write
      // the draft back.
      expect(order).toEqual(['autosave disposed', 'database cleared']);
      expect(store.drafts).toEqual({ all: [], published: [], damaged: [] });
      expect(store.documents).toEqual([]);
      expect(store.sources.topologies).toEqual([]);
      expect(store.published).toBeNull();
      expect(store.readOnly).toBe(false);
      expect(store.autosave).toBeNull();
      expect(readRecent()).toEqual([]);
      expect(readGroupPattern(local)).toBe('');
      expect(store.layerSets.items).toEqual([]);
      expect(store.layerSets.defaultId).toBeNull();
      expect(store.layerSets.loaded).toBe(false);
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
      const opening = store.loadDraft('d1');
      await end();
      answer({
        draft: { id: 'd1', createdBy: 'alice' },
        document: store.doc,
        etag: '"1"',
      });

      expect(await opening).toBeNull();
      expect([store.createdBy, store.draftId, store.autosave]).toEqual([
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
      answer({
        all: [{ id: 'd1', createdBy: 'alice' }],
        published: [],
        damaged: [],
      });
      await listing;

      expect(store.drafts.all).toEqual([]);
      expect(store.loading).toBe(false);

      // A listing the role may not read shows nothing rather than what an
      // earlier one showed.
      store.drafts = {
        all: [{ id: 'd1', createdBy: 'alice' }],
        published: [],
        damaged: [],
      };
      store.documents = [{ id: 'p1' }];
      const forbidden = Object.assign(new Error('forbidden'), {
        response: { status: 403, data: { message: 'forbidden' } },
      });
      api.listDrafts.mockRejectedValueOnce(forbidden);
      api.listDocuments.mockRejectedValueOnce(forbidden);

      await store.fetchDrafts();
      await store.fetchDocuments();

      expect(store.drafts.all).toEqual([]);
      expect(store.documents).toEqual([]);
      expect(store.error).toMatch(/^Could not list published diagrams\./);
    });
    test("the app's logout ends the Builder session at once", async () => {
      const local = memoryStorage({
        [BUILDER_USER_KEY]: 'alice',
        'phenix.builder.theme': 'dark',
        [RECENT_STORAGE_KEY]: '[{"id":"drafts.open","choices":["x"]}]',
        [unloadCopyKey(draftKey('alice', 'd1'))]: '{"queue":[]}',
      });

      vi.stubGlobal('localStorage', local);
      vi.stubGlobal('sessionStorage', memoryStorage());
      store.drafts = {
        all: [{ id: 'd1', createdBy: 'alice', title: 'Secret lab' }],
        published: [],
        damaged: [],
      };

      const { usePhenixStore: useAppStore } =
        await vi.importActual('@/store.js');

      useAppStore().logout();

      expect(Object.fromEntries(local.entries)).toEqual({
        'phenix.builder.theme': 'dark',
      });
      expect(store.drafts.all).toEqual([]);
      expect(router.replace).toHaveBeenCalledWith('/signin');
    });
  });

  describe('sign-in', () => {
    // What a user leaves in this browser: preferences, recent commands and
    // an open draft listed in memory.
    function leftBehind(user) {
      store.drafts = {
        all: [{ id: 'd1', createdBy: user, title: 'Secret lab' }],
        published: [],
        damaged: [],
      };

      return memoryStorage({
        ...(user ? { [BUILDER_USER_KEY]: user } : {}),
        'phenix.builder.theme': 'dark',
        [RECENT_STORAGE_KEY]: '[{"id":"drafts.open","choices":["x"]}]',
        [unloadCopyKey(draftKey(user, 'd1'))]: '{"queue":[]}',
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
        expect(Object.fromEntries(local.entries)).toEqual({
          'phenix.builder.theme': 'dark',
          [BUILDER_USER_KEY]: 'bob',
        });
        expect(store.drafts.all).toEqual([]);
      }
    });

    test("keeps the user's own Builder data when the same user signs in again", () => {
      const local = leftBehind('alice');
      const clearDatabase = vi.fn(async () => true);

      expect(
        startBuilderSession('alice', { localStorage: local, clearDatabase }),
      ).toBeNull();
      expect(clearDatabase).not.toHaveBeenCalled();
      expect(local.entries.get(RECENT_STORAGE_KEY)).toContain('drafts.open');
      expect(local.entries.has(unloadCopyKey(draftKey('alice', 'd1')))).toBe(
        true,
      );
      expect(store.drafts.all).toHaveLength(1);
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

        expect(local.entries.get(BUILDER_USER_KEY)).toBe('bob');
        expect(local.entries.has(RECENT_STORAGE_KEY)).toBe(false);
        expect(local.entries.has(unloadCopyKey(draftKey('alice', 'd1')))).toBe(
          false,
        );
        expect(local.entries.get('phenix.builder.theme')).toBe('dark');
        expect(store.drafts.all).toEqual([]);
      } finally {
        vi.unstubAllGlobals();
      }
    });
  });

  describe('permissions', () => {
    test('follow the signed-in role', () => {
      const user = usePhenixStore();

      // Reading every config does not give the Builder.
      user.role = {
        name: 'Global Viewer',
        policies: [
          { resources: ['*'], resourceNames: ['*'], verbs: ['list', 'get'] },
        ],
      };
      expect(store.canUseBuilder).toBe(false);
      expect(store.canPublish).toBe(false);

      // The Builder, without the right to change configs.
      user.role = {
        name: 'Builder only',
        policies: [
          { resources: ['builder'], resourceNames: [], verbs: ['use'] },
        ],
      };
      expect(store.canUseBuilder).toBe(true);
      expect(store.canPublish).toBe(false);

      user.role = {
        name: 'Global Admin',
        policies: [
          {
            resources: ['*', '*/*'],
            resourceNames: ['*', '*/*'],
            verbs: ['*'],
          },
        ],
      };
      expect(store.canUseBuilder).toBe(true);
      expect(store.canPublish).toBe(true);

      user.role = null;
      expect(store.canUseBuilder).toBe(false);
      expect(store.canPublish).toBe(false);
    });
  });
});

// The rest of the UI loads session.js and focusMode.js, and what they import
// lands in a chunk the server sends uncompressed.
describe('imports', () => {
  const src = new URL('../../src/', import.meta.url);
  const imports = /\b(?:from|import)\s*\(?\s*['"]((?:\.|@\/)[^'"]+)['"]/g;

  // The src/ files the module at `path` reaches.
  function reached(path) {
    const seen = new Set();
    const visit = (url) => {
      if (seen.has(url.href)) {
        return;
      }
      seen.add(url.href);
      for (const [, from] of readFileSync(url, 'utf8').matchAll(imports)) {
        const base = from.startsWith('@/')
          ? new URL(from.slice(2), src)
          : new URL(from, url);
        visit(
          [base, new URL(`${base.href}.js`)].find((file) =>
            statSync(file, { throwIfNoEntry: false })?.isFile(),
          ),
        );
      }
    };
    visit(new URL(path, src));

    return [...seen].map((href) => href.slice(src.href.length));
  }

  test('stay small: no API client, decoder, validator or Builder store', () => {
    expect(reached('builder/session.js').sort()).toEqual([
      'builder/idb.js',
      'builder/session.js',
      'builder/storage.js',
      'builder/text.js',
      'builder/values.js',
    ]);
    expect(reached('builder/focusMode.js')).toEqual(['builder/focusMode.js']);
  });
});
