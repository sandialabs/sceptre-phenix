// Pinia store orchestrating the beta builder editor.
//
// The store is a thin shell: all document logic lives in the pure modules under
// src/builder, which keeps this file about wiring (selection, history, the
// autosave queue, API calls, announcements) rather than behaviour.

import { defineStore } from 'pinia';
import { markRaw } from 'vue';

import { usePhenixStore } from '@/store.js';
import { roleAllowed } from '@/utils/rbac.js';

import { count, describeRemoval, listOf } from './announce.js';
import {
  builderApi,
  classifyError,
  errorMessage,
  serverReason,
} from './api.js';
import {
  appliedOperations,
  createAutosave,
  describeState,
  initialState,
  replayHistory,
  saveAnnouncement,
  snapshotIdOf,
} from './autosave.js';
import { copySelection, pasteClipboard } from './clipboard.js';
import { DocumentError, parseDocument } from './decode.js';
import { History, DEFAULT_HISTORY_LIMIT } from './history.js';
import { createDraftStore } from './idb.js';
import { changedGeometry, restoreGeometry, withGeometry } from './layout.js';
import { LayoutError, runLayout } from './layouts/index.js';
import { publishRefusal } from './publish.js';
import {
  builderSchemaV1,
  isSchemaBundle,
  normalizeSchemaBundle,
} from './schema.js';
import { onBuilderSessionEnd } from './session.js';
import { builderSettings } from './settings.js';
import { MAX_NAME_BYTES, validateDocument } from './validate.js';
import {
  addInterface,
  addNode,
  connect,
  connectNodes,
  createDocument,
  documentSummary,
  findNetwork,
  findNode,
  groupNodes,
  includedReason,
  moveNodes,
  namedSwitches,
  networkRefusal,
  nodeLabel,
  removalKept,
  removalRefusal,
  removeElements,
  removeInterface,
  removeNetworks,
  renameInterface,
  resizeNode,
  setGrid,
  setParent,
  setDocumentInfo,
  setScenario,
  setViewport,
  ungroup,
  updateEdge,
  updateNetwork,
  updateNode,
} from './model.js';
import {
  DEFAULT_THEME,
  applyTheme,
  nextTheme,
  readStoredTheme,
  resolveTheme,
  storeTheme,
} from './theme.js';

function emptySelection() {
  return { nodes: [], edges: [] };
}

// What the Inspector says when it falls back to the bundled schema.
const BUNDLED_FIELDS =
  'The Inspector shows the fields built into the Builder instead, which may differ from what this server accepts.';

// The disk listing under way (see fetchDisks).
let disksRequest = null;

// Removes what this device keeps for a deleted draft, for every user who
// edited it here. A record that cannot be removed stays behind unread: it is
// read back only for the same draft.
async function forgetLocalDraft(owner, id) {
  try {
    const local = createDraftStore();
    const records = await local.all();

    await Promise.all(
      records
        .filter((record) => record.owner === owner && record.draftId === id)
        .map((record) => local.remove(record.key)),
    );
  } catch {}
}

// Bumped when the session ends (see endSession), so a listing that answers
// after the user logged out is dropped rather than shown to the next user.
let sessionEpoch = 0;

// Bumped on every read of a draft's snapshots, so only the last one asked
// for fills the History dialog (see fetchHistory).
let historyRequest = 0;

// Empties the store of what the user had here, after stopping the queue.
// The theme stays: it is a preference of this browser, which logout keeps
// (see session.js).
function forgetUser(store) {
  const { theme, resolvedTheme } = store;

  store.autosave?.dispose();
  store.$reset();
  store.theme = theme;
  store.resolvedTheme = resolvedTheme;
}

// Whether the signed-in role may `verb` configs, as the server's Builder
// routes require (builderBetaBaseAllowed in web/builder_beta.go). The server
// decides; this only keeps controls it would refuse out of the way.
function configsAllowed(verb) {
  return Boolean(usePhenixStore().role) && roleAllowed('configs', verb);
}

// The drafts of mine made from a published diagram, the one changed last
// first. Times are compared as times: the server's RFC 3339 text drops
// trailing zeros of a fraction, so "…:43Z" is earlier than "…:43.1Z".
function draftsOfDocument(drafts, id) {
  const time = (draft) => Date.parse(draft.updated) || 0;

  return (drafts || [])
    .filter((draft) => draft.sourceToken === `builder-doc/${id}`)
    .sort((a, b) => time(b) - time(a));
}

// The title of a draft saved from local history: the diagram's name with
// " (local copy)". A name near the server's limit on names is cut first,
// without splitting a character, so the title still fits it.
const FORK_SUFFIX = ' (local copy)';

function forkTitle(name) {
  const encoder = new TextEncoder();
  const room = MAX_NAME_BYTES - encoder.encode(FORK_SUFFIX).length;
  let base = '';
  let bytes = 0;

  for (const character of name || 'Diagram') {
    bytes += encoder.encode(character).length;

    if (bytes > room) {
      break;
    }

    base += character;
  }

  return `${base}${FORK_SUFFIX}`;
}

export const useBuilderStore = defineStore('builder', {
  state: () => ({
    doc: createDocument(),
    history: markRaw(new History(createDocument(), DEFAULT_HISTORY_LIMIT)),
    // The History object is markRaw (its snapshots are not made reactive), so
    // nothing observes its index. Every action that moves it bumps this, and
    // the canUndo/canRedo getters read it.
    historyVersion: 0,
    autosave: null,
    saveState: initialState(),
    // The last save state that was announced, so a retry that ends in the
    // same problem stays silent.
    saveAnnounced: null,
    selection: emptySelection(),
    clipboard: null,
    // Where the last automatic layout moved nodes from (changedGeometry), and
    // the history entry that layout made. It can be put back only while that
    // entry is current: any other edit, an undo or redo, or another document
    // drops it, so a restore never moves nodes to stale positions.
    layoutRestore: null,
    // Whether an automatic layout is under way (see layout).
    layoutRunning: false,
    schema: builderSchemaV1,
    schemaSource: 'bundled',
    schemaError: '',
    owner: '',
    draftId: '',
    // Goes up each time a diagram is opened (not when a fork renames the
    // draft), so the canvas starts fresh for each diagram.
    openedSeq: 0,
    etag: null,
    // What Publish needs to know about the draft record: its id, the config
    // it was loaded from, the saved snapshot's digest and its last
    // publication (see draftCanUpdate in publish.js).
    draftRecord: { id: '', sourceToken: '', digest: '', publication: null },
    readOnly: false,
    // The published diagram shown read only, with no draft of its own yet
    // ({id, name, target}; see viewPublishedDocument), or null.
    published: null,
    announcement: '',
    // Messages in the same slot supersede each other while they wait to be
    // spoken ('save' for the save state, '' for everything else).
    announcementSlot: '',
    // Bumped on every announcement so a repeated message is re-announced.
    announcementSeq: 0,
    // Bumped on every save-state announcement, so Save now and Retry saving
    // know whether their outcome was spoken already.
    saveAnnouncementSeq: 0,
    // A refused canvas gesture, shown on the canvas until it is dismissed.
    notice: null,
    theme: DEFAULT_THEME,
    resolvedTheme: 'light',
    drafts: { mine: [], shared: [], published: [] },
    publishing: false,
    publishResult: null,
    // Whether resolveConflict is under way (see refuseWhileResolving).
    resolvingConflict: false,
    // The configs ("<kind>/<name>") the server refused to let this draft
    // update because someone else changed them since it published them (see
    // updateBlocker in publish.js).
    publishChanged: { draftId: '', targets: [] },
    queueWork: null,
    documents: [],
    sources: { images: [], topologies: [], scenarios: [], experiments: [] },
    // The file names of the disk images the server has (see fetchDisks), or
    // null while they are unknown: not read yet, or not readable. Drive
    // images are checked against them only while they are known.
    disks: null,
    serverHistory: [],
    // Whether the History dialog is reading serverHistory from the server,
    // and why that failed, if it did (see fetchHistory).
    historyLoading: false,
    historyError: '',
    loading: false,
    error: '',
    // The form field the error is about, when the request that failed came
    // from a dialog form: a publish target's field (see publishRefusal), or
    // 'content' or 'name' for an import source the server refused. The
    // dialog marks that control. '' when the error is about no field.
    errorField: '',
    // Bumped whenever an error is set, so the same failure twice is shown
    // (and announced) twice.
    errorSeq: 0,
  }),

  getters: {
    canUndo: (state) => state.historyVersion >= 0 && state.history.canUndo(),
    canRedo: (state) => state.historyVersion >= 0 && state.history.canRedo(),
    // Whether Auto layout offers to put the previous layout back instead.
    canRestoreLayout: (state) =>
      state.historyVersion >= 0 &&
      !state.readOnly &&
      Boolean(state.layoutRestore) &&
      state.layoutRestore.entryId === state.history.currentEntry().id,
    summary: (state) => documentSummary(state.doc),
    issues: (state) => validateDocument(state.doc, { disks: state.disks }),
    errors: (state) =>
      validateDocument(state.doc).filter((issue) => issue.level === 'error'),
    hasConflict: (state) => state.saveState.status === 'conflict',
    saveStateText: (state) => describeState(state.saveState),
    // What the signed-in role may do (see configsAllowed). Blank, Import and
    // Upload, and editing a published diagram, all create a draft.
    canCreateDrafts: () => configsAllowed('create'),
    canPublish: () => configsAllowed('update'),
    canDeleteDrafts: () => configsAllowed('delete'),
    // The draft, as draftCanUpdate() in publish.js takes it.
    publishDraft: (state) => ({
      ...state.draftRecord,
      source: state.doc.source || null,
      documents: state.documents,
      changedTargets:
        state.publishChanged.draftId === state.draftId
          ? state.publishChanged.targets
          : [],
    }),
    selectedNode(state) {
      return state.selection.nodes.length === 1
        ? findNode(state.doc, state.selection.nodes[0])
        : null;
    },
    selectedEdge(state) {
      return state.selection.edges.length === 1
        ? (state.doc.edges || []).find(
            (edge) => edge.id === state.selection.edges[0],
          )
        : null;
    },
    inspectorSelection(state) {
      if (state.selection.nodes.length === 1) {
        return { type: 'node', id: state.selection.nodes[0] };
      }

      if (state.selection.edges.length === 1) {
        return { type: 'edge', id: state.selection.edges[0] };
      }

      return { type: 'document' };
    },
  },

  actions: {
    /**
     * @param {string} message
     * @param {object} [options] slot: see announcementSlot
     */
    announce(message, { slot = '' } = {}) {
      this.announcement = message;
      this.announcementSlot = slot;
      this.announcementSeq += 1;
    },

    // Sets the page-level error. Every call counts, so a repeat is shown.
    setError(message, field = '') {
      this.error = message;
      this.errorField = field;
      this.errorSeq += 1;
    },

    clearError() {
      this.error = '';
      this.errorField = '';
    },

    historyChanged() {
      this.historyVersion += 1;
    },

    // Keeps what Publish needs from a draft record the server sent.
    rememberDraft(draft) {
      if (!draft || typeof draft !== 'object') {
        return;
      }

      this.draftRecord = {
        id: draft.id || '',
        sourceToken: draft.sourceToken || '',
        digest: draft.digest || '',
        publication: draft.publication || null,
      };
    },

    // Shows why a canvas gesture did nothing, and announces it.
    refuse(message) {
      this.announce(message);
      this.notice = { text: message, seq: this.announcementSeq };
    },

    dismissNotice() {
      this.notice = null;
    },

    /**
     * Records one semantic edit: local history, then the autosave queue. Every
     * call becomes exactly one server snapshot.
     *
     * @param {object} doc next document
     * @param {string} label short description
     */
    commit(doc, label) {
      if (this.readOnly) {
        // The page alert reads it; announcing it too would say it twice.
        this.setError('This draft is read only.');

        return null;
      }

      if (this.refuseWhileResolving()) {
        return null;
      }

      this.doc = doc;
      this.layoutRestore = null;

      const entry = this.history.push(doc, label);
      this.historyChanged();

      if (label) {
        this.announce(`${label}${this.unsavedNote()}`);
      }

      if (this.autosave && !this.readOnly) {
        this.trackQueue(this.autosave.commit(entry));
      }

      return entry;
    },

    // While a conflict is being resolved, an edit or an undo would be lost:
    // saving the history as a new draft saves it as it was when that started,
    // and discarding loads the server's version. It is refused and announced.
    refuseWhileResolving() {
      if (!this.resolvingConflict) {
        return false;
      }

      this.announce(
        'Not changed: the conflict is being resolved. Edit again once it is.',
      );

      return true;
    },

    // While the queue is blocked, an edit is kept only here; its
    // announcement says so instead of sounding saved.
    unsavedNote() {
      switch (this.saveState.status) {
        case 'conflict':
          return '. Not saved: this draft changed on the server.';
        case 'forbidden':
          return '. Not saved: you cannot change this draft.';
        default:
          return '';
      }
    },

    /**
     * Keeps a queue call from becoming fire and forget. The queue reports
     * failures through its own state, so nothing is thrown here, but the
     * promise is always handled and the last one is awaited by saveNow.
     *
     * @param {Promise<object>} promise
     */
    trackQueue(promise) {
      this.queueWork = markRaw(
        Promise.resolve(promise).catch((error) => {
          this.setError(this.describeError(error, 'save your changes'));

          return this.saveState;
        }),
      );

      return this.queueWork;
    },

    initAutosave({ owner, draftId, etag, entries, cursor, queue }) {
      const phenix = usePhenixStore();

      this.owner = owner;
      this.draftId = draftId;

      if (etag) {
        this.etag = etag;
      }

      this.autosave?.dispose();
      this.autosave = markRaw(
        createAutosave({
          api: builderApi,
          store: createDraftStore(),
          actor: phenix.username || 'anonymous',
          onState: (state) => {
            this.saveState = state;

            if (state.etag) {
              this.etag = state.etag;
            }

            this.announceSaveState(state);
          },
          onDraft: (envelope, operation) => {
            this.rememberDraft(envelope.draft);

            if (envelope.etag) {
              this.etag = envelope.etag;
            }

            // A save answers with the draft alone: the history it leaves
            // unknown is kept until History reads it again.
            if (Array.isArray(envelope.history)) {
              this.serverHistory = envelope.history;
            }

            if (operation?.kind === 'snapshot') {
              const position = this.history.entries.findIndex(
                (item) => item.id === operation.commitId,
              );
              const entry = this.history.entries[position];
              const snapshotId = snapshotIdOf(envelope);

              if (entry && snapshotId) {
                entry.serverSnapshotId = snapshotId;
              }

              // The server keeps fewer snapshots than this history when they
              // are large (MaxDraftHistoryBytes), the one just saved being
              // its newest. Undo stops at the oldest it keeps: a move to one
              // it dropped would be refused.
              const kept = envelope.draft?.snapshots;

              if (
                entry &&
                Number.isInteger(kept) &&
                kept > 0 &&
                this.history.trimOldest(position - (kept - 1)) > 0
              ) {
                this.historyChanged();
              }
            }
          },
        }),
      );

      this.autosave.listen();

      // Entries and the queue are deliberately left out unless the caller has
      // them: attach then keeps whatever ordered log this device already holds
      // for the draft, which is what recoverLocalHistory reads back.
      return this.autosave.attach({
        owner,
        draftId,
        etag,
        entries,
        cursor,
        queue,
      });
    },

    /**
     * Announces a save-state change a user must know about: a new problem,
     * or the recovery from one (see saveAnnouncement).
     *
     * @param {object} state queue state
     * @returns {boolean} whether anything was announced
     */
    announceSaveState(state) {
      const message = saveAnnouncement(this.saveAnnounced, state);

      // A conflict is announced by its panel, and resolving it by the
      // action chosen: the save that follows is no recovery to announce.
      if (['saved', 'conflict'].includes(state.status) || message) {
        this.saveAnnounced = {
          status: state.status,
          message: state.message,
          storageFailed: state.storageFailed,
        };
      }

      if (message) {
        this.announceSaveResult(message);
      }

      return Boolean(message);
    },

    // A save-state message: only the latest one waiting is spoken.
    announceSaveResult(message) {
      this.saveAnnouncementSeq += 1;
      this.announce(message, { slot: 'save' });
    },

    /**
     * Says how an explicit save ended, unless that was announced already (a
     * new problem, or the recovery from one). An outcome the same as before
     * is still spoken, so the user always hears a result.
     *
     * @param {object} state queue state after the save
     * @param {number} before saveAnnouncementSeq when the save started
     * @param {string} [note] what the save left out, said with the outcome
     *   (edits the Inspector could not apply); a save then says "Saved."
     *   rather than "All changes saved."
     */
    announceSaveOutcome(state, before, note = '') {
      if (this.saveAnnouncementSeq !== before) {
        if (note) {
          this.announce(note);
        }

        return;
      }

      if (state.status === 'saved') {
        this.announceSaveResult(note ? `Saved. ${note}` : 'All changes saved.');

        return;
      }

      // The state's own words end without a stop when said alone.
      const outcome = describeState(state);

      this.announceSaveResult(
        note ? `${outcome.replace(/[^.]$/, '$&.')} ${note}` : outcome,
      );
    },

    newDocument(init = {}) {
      this.autosave?.dispose();
      this.autosave = null;
      this.queueWork = null;

      const doc = createDocument(init);

      this.doc = doc;
      this.history = markRaw(new History(doc, DEFAULT_HISTORY_LIMIT));
      this.historyChanged();
      this.selection = emptySelection();
      this.openedSeq += 1;
      this.owner = '';
      this.draftId = doc.id;
      this.etag = null;
      this.readOnly = false;
      this.published = null;
      this.rememberDraft({});
      this.saveState = initialState();
      this.saveAnnounced = null;

      return doc;
    },

    /**
     * Replaces the document. The payload is decoded strictly: an unsupported or
     * invalid document is rejected with a message instead of being silently
     * repaired.
     *
     * @param {object} payload
     * @param {object} [options] label, resetHistory, and announce: false to
     *   load it with a reset history unannounced (see generate)
     * @returns {object|null} document, or null when rejected
     */
    setDocument(
      payload,
      { label = 'Document loaded', resetHistory = true, announce = true } = {},
    ) {
      let document;

      try {
        // A switch is named after its network, whatever label a document
        // saved or imported with another name still carries (R37).
        document = namedSwitches(parseDocument(payload));
      } catch (error) {
        // The page alert (or the dialog that called this) reads it.
        this.setError(
          error instanceof DocumentError
            ? error.message
            : 'The document could not be read.',
        );

        return null;
      }

      this.doc = document;
      // A recovered local history can make the layout's entry current again.
      this.layoutRestore = null;

      if (resetHistory) {
        this.history.reset(document, label);
        this.historyChanged();
        this.openedSeq += 1;

        if (announce) {
          this.announce(label);
        }
      } else {
        // commit() announces the label.
        this.commit(document, label);
      }

      this.selection = emptySelection();

      return document;
    },

    /**
     * @param {object} [options] document, title, sourceToken, and
     *   announcement: what to say once the draft exists ('' for nothing)
     */
    async createDraft({
      document,
      title,
      sourceToken,
      announcement = 'Draft created.',
    } = {}) {
      const phenix = usePhenixStore();
      const doc = document || this.doc;
      const epoch = sessionEpoch;

      this.clearError();

      try {
        const envelope = await builderApi.createDraft({
          title: title || doc.name || 'Untitled diagram',
          sourceToken,
          document: doc,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        this.owner = envelope.draft?.owner || phenix.username || '';
        this.draftId = envelope.draft?.id || doc.id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = false;
        this.published = null;
        this.rememberDraft(envelope.draft);
        this.doc = envelope.document ? parseDocument(envelope.document) : doc;
        this.history = markRaw(new History(this.doc, DEFAULT_HISTORY_LIMIT));
        this.historyChanged();
        this.linkCurrentHistorySnapshot(envelope);

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (announcement) {
          this.announce(announcement);
        }

        return envelope;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(this.describeError(error, 'create the draft'));
        }

        return null;
      }
    },

    async loadDraft(owner, id) {
      const epoch = sessionEpoch;

      this.loading = true;
      this.clearError();

      try {
        const envelope = await builderApi.getDraft(owner, id);
        const phenix = usePhenixStore();

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        this.owner = envelope.draft?.owner || owner;
        this.draftId = envelope.draft?.id || id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = Boolean(envelope.draft?.readOnly);
        this.published = null;
        this.rememberDraft(envelope.draft);

        const loaded = this.setDocument(envelope.document, {
          label: 'Draft loaded',
        });

        if (!loaded) {
          return null;
        }

        this.linkCurrentHistorySnapshot(envelope);
        const serverETag = envelope.etag;

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        await this.recoverLocalHistory(serverETag);

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (phenix.username && this.owner !== phenix.username) {
          this.announce('Opened a shared draft.');
        }

        return this.doc;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(this.describeError(error, 'load the draft'));
        }

        return null;
      } finally {
        this.loading = false;
      }
    },

    /**
     * Restores the pending queue a previous session on this device recorded,
     * and the undo history it leaves (see replayHistory). Operations the
     * server already holds are not sent again (see appliedOperations).
     *
     * @param {string} [serverETag] the ETag the draft was read with, whose
     *   history is serverHistory
     * @returns {Promise<boolean>} whether unsaved work was recovered
     */
    async recoverLocalHistory(serverETag = this.etag) {
      if (!this.autosave) {
        return false;
      }

      const local = await this.autosave.recover(this.owner, this.draftId);
      const queue = (local?.queue || []).filter(
        (operation) =>
          operation.kind === 'snapshot' ||
          (operation.kind === 'cursor' &&
            (operation.snapshotId || operation.commitId)),
      );
      const applied = appliedOperations(queue, this.serverHistory);
      const pending = queue.slice(applied.count);

      if (pending.length === 0) {
        await this.autosave.attach({
          owner: this.owner,
          draftId: this.draftId,
          etag: serverETag,
          entries: [],
          cursor: 0,
          queue: [],
        });

        return false;
      }

      const entries = (local.entries || []).map((entry) =>
        applied.snapshotIds.has(entry.id)
          ? { ...entry, serverSnapshotId: applied.snapshotIds.get(entry.id) }
          : entry,
      );
      const replayed = replayHistory(
        [this.history.currentEntry()],
        0,
        pending,
        entries,
      );

      this.history.restore(replayed.entries, replayed.index);
      this.historyChanged();
      this.doc = this.history.current();

      // The queue is based on the ETag it was stored with. When the server
      // holds its first operations, the rest goes on from the snapshot the
      // last of them stored: still the draft's newest and current one, the
      // server's version is exactly what the queue left.
      const lastApplied = applied.snapshotIds.get(
        queue[applied.count - 1]?.commitId,
      );
      const newest = this.serverHistory.at(-1);
      const etag =
        lastApplied && newest?.id === lastApplied && newest.current
          ? serverETag
          : local.etag;

      await this.autosave.attach({
        owner: this.owner,
        draftId: this.draftId,
        etag,
        entries,
        cursor: local.cursor,
        queue: pending,
      });

      this.announce(
        `Recovered ${pending.length} unsaved change${pending.length === 1 ? '' : 's'} from this device.`,
      );

      if (etag !== serverETag) {
        this.autosave.conflict();

        return true;
      }

      await this.autosave.flush();

      return true;
    },

    linkCurrentHistorySnapshot(envelope) {
      const snapshotId =
        envelope.history?.[envelope.cursor]?.id || envelope.draft?.snapshotId;

      if (snapshotId) {
        this.history.currentEntry().serverSnapshotId = snapshotId;
      }
    },

    /**
     * Tells the server which snapshot in the draft's own undo history is
     * current. This is the draft history cursor; the editor has no presence or
     * live collaboration signal.
     */
    moveHistoryCursor() {
      if (!this.autosave || this.readOnly) {
        return;
      }

      const entry = this.history.currentEntry();
      this.trackQueue(
        this.autosave.moveCursor({
          snapshotId: entry.serverSnapshotId,
          commitId: entry.id,
          entry,
        }),
      );
    },

    /**
     * @param {object} [options] announce: say how the save ended (Save
     *   now's key), even when nothing was waiting to be saved;
     *   note: what the save left out, said with the outcome (see
     *   announceSaveOutcome)
     */
    async saveNow({ announce = false, note = '' } = {}) {
      if (!this.autosave) {
        if (announce) {
          this.announce('Nothing to save: this diagram is not a draft yet.');
        }

        return this.saveState;
      }

      const before = this.saveAnnouncementSeq;

      // Anything already queued is awaited first so callers (publish) see the
      // real end state rather than racing an in-flight commit.
      await this.queueWork;

      const state = await this.autosave.flush();

      if (announce) {
        this.announceSaveOutcome(state, before, note);
      }

      return state;
    },

    /**
     * @param {object} [options] announce: say how the retry ended (the
     *   toolbar's Retry saving), even when it failed the same way again
     */
    async retrySave({ announce = false } = {}) {
      if (!this.autosave) {
        return this.saveState;
      }

      const before = this.saveAnnouncementSeq;
      const state = await this.autosave.retry();

      if (announce) {
        this.announceSaveOutcome(state, before);
      }

      return state;
    },

    /**
     * Resolves an ETag conflict. Local work is never written over the server
     * copy: either the server copy is reloaded, or the local history is saved
     * as a new draft.
     *
     * @param {'reload'|'fork'} choice
     * @param {object} [options] title
     */
    async resolveConflict(choice, options = {}) {
      if (!this.autosave || this.resolvingConflict) {
        return null;
      }

      this.resolvingConflict = true;

      try {
        return await this.settleConflict(choice, options);
      } finally {
        this.resolvingConflict = false;
      }
    },

    async settleConflict(choice, options) {
      if (choice === 'fork') {
        const title = options.title || forkTitle(this.doc.name);
        // The server titles a draft after its document's name, so every
        // saved snapshot carries the new name, or the fork would look like
        // the draft it leaves.
        const entries = this.history.entries.map((entry) => ({
          ...entry,
          snapshot: setDocumentInfo(entry.snapshot, { name: title }),
        }));

        try {
          const envelope = await this.autosave.forkLocalHistory({
            title,
            entries,
            index: this.history.index,
          });

          this.owner = envelope.draft?.owner || this.owner;
          this.draftId = envelope.draft?.id || this.draftId;
          this.rememberDraft(envelope.draft);
          this.etag = envelope.etag;
          this.history.entries = entries;
          this.doc = setDocumentInfo(this.doc, { name: title });
          this.historyChanged();

          // Each entry now names its snapshot in the new draft, so undo and
          // redo move that draft's cursor, never the old draft's.
          this.history.entries.forEach((entry) => {
            entry.serverSnapshotId = envelope.snapshotIds.get(entry.id);
          });
          this.serverHistory = await builderApi
            .listSnapshots(this.owner, this.draftId)
            .catch(() => []);
          this.announce(`Saved your local history as a new draft, ${title}.`);

          return envelope;
        } catch (error) {
          this.setError(this.describeError(error, 'save a new draft'));

          return null;
        }
      }

      const discarded = this.saveState.pending;

      await this.autosave.discardLocal();

      const loaded = await this.loadDraft(this.owner, this.draftId);

      if (loaded) {
        this.announce(
          `Discarded ${count(discarded, 'unsaved change')} and loaded the server version.`,
        );
      }

      return loaded;
    },

    /**
     * @param {string} owner
     * @param {string} id
     * @param {string} [etag]
     * @param {string} [title] named in the announcement
     */
    async deleteDraft(owner, id, etag, title = '') {
      this.clearError();

      try {
        await builderApi.deleteDraft(owner, id, etag);
        await forgetLocalDraft(owner, id);
        this.announce(title ? `Deleted draft ${title}.` : 'Draft deleted.');
        await this.fetchDrafts();

        return true;
      } catch (error) {
        this.setError(this.describeError(error, 'delete the draft'));

        return false;
      }
    },

    /**
     * Loads the server schema. A failure is reported: the editor keeps working
     * with the bundled copy, but the user is told the server copy is not in
     * use, because form fields may differ from what the server will accept.
     */
    async fetchSchema() {
      try {
        const payload = await builderApi.getSchema();

        const usable = isSchemaBundle(payload);

        this.schema = normalizeSchemaBundle(payload);
        this.schemaSource = usable ? 'server' : 'bundled';
        this.schemaError = usable
          ? ''
          : `The server sent form fields this editor does not recognize. ${BUNDLED_FIELDS}`;
      } catch (error) {
        this.schema = builderSchemaV1;
        this.schemaSource = 'bundled';
        this.schemaError = `${this.describeError(error, "load this server's form fields")} ${BUNDLED_FIELDS}`;
      }

      return this.schema;
    },

    // A listing that answers after the session ended is dropped: it is the
    // previous user's (see endSession). One the role may not read empties
    // the lists; any other failure keeps what they showed.
    async fetchDrafts() {
      const epoch = sessionEpoch;

      this.loading = true;
      this.clearError();

      try {
        const drafts = await builderApi.listDrafts();

        if (epoch === sessionEpoch) {
          this.drafts = drafts;
        }
      } catch (error) {
        if (epoch === sessionEpoch) {
          if (classifyError(error) === 'forbidden') {
            this.drafts = { mine: [], shared: [], published: [] };
          }
          this.setError(this.describeError(error, 'list drafts'));
        }
      } finally {
        if (epoch === sessionEpoch) {
          this.loading = false;
        }
      }

      return this.drafts;
    },

    async fetchDocuments() {
      const epoch = sessionEpoch;

      try {
        const documents = await builderApi.listDocuments();

        if (epoch === sessionEpoch) {
          this.documents = documents;
        }
      } catch (error) {
        if (epoch === sessionEpoch) {
          if (classifyError(error) === 'forbidden') {
            this.documents = [];
          }
          this.setError(this.describeError(error, 'list published diagrams'));
        }
      }

      return this.documents;
    },

    /**
     * Shows a published diagram read only. No draft is made: the diagram is
     * only looked at until the user chooses to edit it (editPublished), which
     * a role without the configs create permission cannot.
     *
     * @param {string} id published document id
     * @returns {Promise<object|null>} the diagram, or null when it could not
     *   be read
     */
    async viewPublishedDocument(id) {
      const epoch = sessionEpoch;

      this.loading = true;
      this.clearError();

      try {
        const document = parseDocument(await builderApi.getDocument(id));

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        const listed = this.documents.find((item) => item.id === id);
        const name = document.name || listed?.name || listed?.target || id;

        this.newDocument();
        this.setDocument(document, { announce: false });
        this.draftId = '';
        this.serverHistory = [];
        this.readOnly = true;
        this.published = { id, name, target: listed?.target || '' };
        this.announce(`Opened published diagram ${name}, read only.`);

        return this.doc;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(
            this.describeError(error, 'open the published diagram'),
          );
        }

        return null;
      } finally {
        this.loading = false;
      }
    },

    /**
     * Edits the published diagram shown read only (see openPublishedDocument).
     *
     * @returns {Promise<object|null>} the draft's document, or null
     */
    editPublished() {
      if (!this.published) {
        return Promise.resolve(null);
      }

      return this.openPublishedDocument(this.published.id, {
        document: this.doc,
      });
    },

    /**
     * Opens a published diagram for editing in a draft of mine: the one made
     * from it before, when there is one (see draftsOfDocument), so opening
     * the same diagram again never piles up copies; otherwise a new
     * draft made from it.
     *
     * @param {string} id published document id
     * @param {object} [options] announcement: what to say when a new draft
     *   is made, and resumed: when an existing one is opened (defaults name
     *   the diagram); document: the diagram, when it was read already
     * @returns {Promise<object|null>} the draft's document, or null
     */
    async openPublishedDocument(
      id,
      { announcement, resumed, document: known } = {},
    ) {
      const epoch = sessionEpoch;

      this.loading = true;
      this.clearError();

      try {
        const [existing] = draftsOfDocument(await this.listMine(), id);

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (existing) {
          const opened = await this.loadDraft(existing.owner, existing.id);

          if (opened) {
            this.announce(
              resumed ||
                `Opened your draft of published diagram ${opened.name || existing.title || id}.`,
            );
          }

          return opened;
        }

        const document =
          known || parseDocument(await builderApi.getDocument(id));

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        // One message for the whole operation, so neither half is lost.
        const created = await this.createDraft({
          document,
          title: document.name,
          sourceToken: `builder-doc/${id}`,
          announcement:
            announcement ||
            `Opened published diagram ${document.name || id} as a new draft.`,
        });

        return created ? this.doc : null;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(
            this.describeError(error, 'open the published diagram'),
          );
        }

        return null;
      } finally {
        this.loading = false;
      }
    },

    // My drafts as the server lists them now (they may have been made in
    // another tab), or as last listed when it cannot say.
    async listMine() {
      const epoch = sessionEpoch;

      try {
        const drafts = await builderApi.listDrafts();

        if (epoch === sessionEpoch) {
          this.drafts = drafts;
        }

        return drafts.mine;
      } catch {
        return this.drafts.mine;
      }
    },

    async fetchSources() {
      const epoch = sessionEpoch;

      try {
        const sources = await builderApi.getSources();

        if (epoch === sessionEpoch) {
          this.sources = sources;
        }
      } catch (error) {
        if (epoch === sessionEpoch) {
          this.setError(this.describeError(error, 'load source catalogs'));
        }
      }

      return this.sources;
    },

    /**
     * Reads the disk images the server has, for the drive image checks and
     * suggestions. Both are advisory, so a listing the server refuses (a
     * role without it), fails (minimega not answering) or never answers
     * leaves the images unknown, with no page alert. So does an empty one:
     * the server lists no images when minimega is not running either, and
     * every drive would then be reported missing. A read asked for while
     * another is under way waits for that one.
     *
     * @returns {Promise<string[]|null>} the images, or null when unknown
     */
    fetchDisks() {
      if (!disksRequest) {
        const epoch = sessionEpoch;
        const request = builderApi
          .listDisks()
          .then(
            (disks) => (disks.length ? disks : null),
            () => null,
          )
          .then((disks) => {
            // A reading for the previous user is not kept (see endSession).
            if (epoch === sessionEpoch) {
              this.disks = disks;
            }
            if (disksRequest === request) {
              disksRequest = null;
            }

            return disks;
          });

        disksRequest = request;
      }

      return disksRequest;
    },

    // Reads the open draft's snapshots for the History dialog, which opens
    // at once and shows historyLoading until they arrive, or historyError if
    // they do not: the page alert behind the dialog is left alone. A list
    // that answers after another read started, another draft opened or the
    // session ended is dropped.
    async fetchHistory() {
      const { owner, draftId } = this;
      if (!owner || !draftId) {
        return [];
      }

      const epoch = sessionEpoch;
      const request = (historyRequest += 1);
      const latest = () => epoch === sessionEpoch && request === historyRequest;
      const current = () =>
        latest() && this.owner === owner && this.draftId === draftId;

      this.clearError();
      this.historyLoading = true;
      this.historyError = '';

      try {
        const history = await builderApi.listSnapshots(owner, draftId);

        if (current()) {
          this.serverHistory = history;
        }
      } catch (error) {
        if (current()) {
          this.historyError = this.describeError(
            error,
            'read the draft history',
          );
        }
      } finally {
        if (latest()) {
          this.historyLoading = false;
        }
      }

      return this.serverHistory;
    },

    async restoreSnapshot(snapshotId) {
      this.clearError();

      try {
        if (!this.autosave) {
          this.setError('Open a saved draft before restoring history.');

          return null;
        }

        const saved = await this.saveNow();
        if (saved.status !== 'saved' || saved.pending > 0) {
          this.setError(
            'Your latest changes are not saved yet, so history cannot be restored. Wait for the save to finish, or use Retry saving.',
          );

          return null;
        }

        if (!this.serverHistory.some((entry) => entry.id === snapshotId)) {
          this.setError(
            'That snapshot is no longer in the draft history. Open History again to see the current list.',
          );

          return null;
        }

        const envelope = await builderApi.getSnapshot(
          this.owner,
          this.draftId,
          snapshotId,
        );

        const cursorState = await this.trackQueue(
          this.autosave.moveCursor({ snapshotId }),
        );
        if (cursorState.status !== 'saved') {
          return null;
        }

        // setDocument announces the label.
        const document = this.setDocument(envelope.document, {
          label: 'Snapshot restored',
          resetHistory: true,
        });
        this.history.currentEntry().serverSnapshotId = snapshotId;

        return document;
      } catch (error) {
        this.setError(this.describeError(error, 'restore the snapshot'));

        return null;
      }
    },

    /**
     * Publishes the draft. Publish never carries document bytes: the server
     * reads the snapshot the cursor points at, so the ordered queue has to be
     * confirmed by the server first. A draft with unsent work, a blocked queue
     * or local validation errors is not published.
     *
     * @param {object} intent mode, topology, scenario, experiment
     * @returns {Promise<object|null>} publish result
     */
    async publish(intent) {
      this.publishResult = null;
      this.clearError();

      if (this.readOnly) {
        this.setError('This draft is read only.');

        return null;
      }

      if (this.errors.length > 0) {
        this.setError('Fix the errors in the diagram before publishing.');

        return null;
      }

      if (!this.autosave || !this.owner || !this.draftId) {
        this.setError('Save this diagram as a draft before publishing it.');

        return null;
      }

      // The queue of the draft being published, whatever opens meanwhile.
      const autosave = this.autosave;
      const state = await this.saveNow();

      if (state.status !== 'saved' || state.pending > 0) {
        this.setError(
          state.status === 'conflict'
            ? 'This draft changed elsewhere. Resolve the conflict before publishing.'
            : 'Your latest changes are not saved on the server yet, so there is nothing new to publish. Wait for the save to finish or retry it.',
        );
        this.announce(this.error);

        return null;
      }

      this.publishing = true;

      // Edits made while publishing stay queued until it ends, so no save
      // lands between the publish and the ETag it answers with, which would
      // then be stale and raise a conflict with the user's own publish.
      const release = autosave.hold();

      try {
        await autosave.idle();

        const sent = autosave.sent;
        const { result, etag } = await builderApi.publish(
          this.owner,
          this.draftId,
          intent,
          this.etag,
        );

        if (etag && autosave.sent === sent) {
          this.etag = etag;
          await autosave.setETag(etag);
        }

        this.publishResult = result;
        this.rememberDraft(result.draft);

        // The configs just written now exist, so the next publish of the same
        // names has to update them, and the topology now points at the
        // diagram just published.
        if (result.ok || result.partial) {
          await Promise.all([this.fetchSources(), this.fetchDocuments()]);
        }

        if (result.ok) {
          this.clearError();
          this.announce('Diagram published.');
        } else {
          this.setError(
            result.partial
              ? 'Publish finished with failures. Some configs were written.'
              : 'Publish failed. No configs were written.',
          );
          this.announce(this.error);
        }

        return result;
      } catch (error) {
        if (error?.response?.status === 409) {
          // A 409 is about a config the publish would write, not the draft,
          // and the server says which and why. A create or update refused
          // because the config does or does not exist means the list of
          // existing configs is out of date, so it is read again first,
          // with the published diagrams that say which this draft may update.
          await Promise.all([this.fetchSources(), this.fetchDocuments()]);

          const refusal = publishRefusal(serverReason(error), intent, {
            topologies: this.sources.topologies,
            experiments: this.sources.experiments,
            draft: this.publishDraft,
          });

          // A config someone else changed since this draft published it
          // stays refused, so the dialog offers only another name for it.
          if (refusal.changed) {
            this.publishChanged = {
              draftId: this.draftId,
              targets: [...this.publishDraft.changedTargets, refusal.changed],
            };
          }

          this.setError(
            `Could not publish the diagram. ${refusal.message}`,
            refusal.field,
          );
        } else if (error?.response?.status === 422) {
          // A target the server refuses, such as an experiment name it
          // reserves, is named in the reason: the dialog marks its field.
          this.setError(
            this.describeError(error, 'publish the diagram'),
            publishRefusal(serverReason(error), intent).field,
          );
        } else if (classifyError(error) === 'conflict') {
          // 412 or 428: the draft's ETag.
          this.setError(
            'This draft changed on the server while publishing. Reload it and try again.',
          );
        } else {
          this.setError(this.describeError(error, 'publish the diagram'));
        }

        return null;
      } finally {
        this.publishing = false;
        this.trackQueue(release());
      }
    },

    async generate(request) {
      this.clearError();

      try {
        const result = await builderApi.generate(request);
        // Validate before detaching the current draft. A successful generation
        // always starts separately; it must never reuse the prior draft's queue.
        const generated = parseDocument(result.document);

        this.newDocument({ name: generated.name });

        // Nothing is imported yet: the user may still cancel on the
        // warnings, and the draft may fail to be created. The import is
        // announced once its draft exists (see GenerateDialog).
        const document = this.setDocument(generated, {
          label: 'Imported diagram',
          announce: false,
        });

        return document
          ? { document, warnings: result.warnings, source: result.source }
          : null;
      } catch (error) {
        const kind = classifyError(error);
        const uploaded = typeof request?.content === 'string';

        // A stored config removed since the list was read. The list is read
        // again, so it is no longer offered, and the user picks another.
        if (kind === 'missing' && !uploaded) {
          await this.fetchSources();

          const listed = (
            this.sources[
              request.kind === 'experiment' ? 'experiments' : 'topologies'
            ] || []
          ).some(
            (entry) =>
              (typeof entry === 'string' ? entry : entry?.name) ===
              request.name,
          );

          if (!listed) {
            this.setError(
              `Could not import the diagram. The ${request.kind} "${request.name}" no longer exists. Choose another one.`,
              'name',
            );

            return null;
          }
        }

        // A refused source is about the control that chose it: the uploaded
        // config, or the stored config picked by name.
        const refused =
          kind === 'invalid' ||
          kind === 'too-large' ||
          (kind === 'missing' && !uploaded);
        const field = uploaded ? 'content' : 'name';

        this.setError(
          this.describeError(error, 'import the diagram'),
          refused ? field : '',
        );

        return null;
      }
    },

    describeError(error, action) {
      // The server answered, but parseDocument rejected the diagram it sent.
      // That is not a connection problem, and only the decoder's reason says
      // what is wrong.
      if (error instanceof DocumentError) {
        const reason = error.message.trim().replace(/\.$/, '');

        return `Could not ${action}. The server sent a diagram the editor cannot open: ${reason}.`;
      }

      const kind = classifyError(error);
      const detail = errorMessage(kind, error);

      return `Could not ${action}. ${detail}`;
    },

    // --- document editing -------------------------------------------------

    addNode(options) {
      const result = addNode(this.doc, options);

      this.commit(result.doc, `Added ${options.kind || 'device'}`);
      this.selection = { nodes: [result.node.id], edges: [] };

      return result.node;
    },

    updateNode(id, patch, label = 'Updated node') {
      const reason = includedReason(findNode(this.doc, id));

      // Where an included device sits is the diagram's own; the rest is not.
      if (reason && (patch.device || patch.label !== undefined)) {
        this.refuse(reason);

        return null;
      }

      return this.commit(updateNode(this.doc, id, patch), label);
    },

    /**
     * Moves one or more nodes as a single history commit, so a multi-node drag
     * or keyboard nudge is one undo step and one server snapshot. A move that
     * leaves a node where it is, such as a click that wobbled less than a grid
     * step, is not an edit: it is dropped rather than announced as a move and
     * kept as an empty undo step.
     *
     * @param {{id: string, position: {x: number, y: number}}[]} moves
     * @returns {boolean} whether any node moved
     */
    moveNodes(moves, label) {
      const changed = (moves || []).filter((move) => {
        const node = findNode(this.doc, move.id);

        return (
          node &&
          (node.position?.x !== move.position.x ||
            node.position?.y !== move.position.y)
        );
      });

      if (changed.length === 0) {
        return false;
      }

      const entry = this.commit(
        moveNodes(this.doc, changed),
        label ||
          (changed.length === 1
            ? `Moved ${nodeLabel(findNode(this.doc, changed[0].id)) || 'node'}`
            : `Moved ${changed.length} nodes`),
      );

      return entry !== null;
    },

    moveNode(id, position) {
      this.moveNodes([{ id, position }]);
    },

    /**
     * Nudges the current selection by a delta, as one commit.
     *
     * @param {number} dx
     * @param {number} dy
     */
    nudgeSelection(dx, dy) {
      const moves = this.selection.nodes
        .map((id) => findNode(this.doc, id))
        .filter(Boolean)
        .map((node) => ({
          id: node.id,
          position: { x: node.position.x + dx, y: node.position.y + dy },
        }));

      // Keyboard users cannot see where a node went, so say it.
      const [only] = moves;
      const direction =
        (dx > 0 && 'right') || (dx < 0 && 'left') || (dy > 0 ? 'down' : 'up');
      const label =
        moves.length === 1
          ? `Moved ${nodeLabel(findNode(this.doc, only.id)) || 'node'} to ` +
            `x ${only.position.x}, y ${only.position.y}`
          : `Moved ${moves.length} nodes ${direction} by ` +
            `${Math.abs(dx || dy)}`;

      this.moveNodes(moves, label);
    },

    // A resize or a move between groups that changes nothing is not an
    // edit, as in moveNodes: no undo step, no snapshot, no announcement.
    resizeNode(id, size) {
      const node = findNode(this.doc, id);

      if (
        !node ||
        (node.size?.width === size.width && node.size?.height === size.height)
      ) {
        return null;
      }

      // Keyboard users cannot see the new size, so say it.
      return this.commit(
        resizeNode(this.doc, id, size),
        `Resized ${nodeLabel(node) || 'node'} to ${size.width} by ${size.height}`,
      );
    },

    setParent(id, parentId) {
      const next = setParent(this.doc, id, parentId);

      if (next === this.doc) {
        return null;
      }

      const name = nodeLabel(findNode(this.doc, id)) || 'node';
      const group = nodeLabel(findNode(this.doc, parentId));

      return this.commit(
        next,
        parentId
          ? `Added ${name} to group ${group}`
          : `Removed ${name} from its group`,
      );
    },

    updateNetwork(id, patch) {
      const current = findNetwork(this.doc, id);
      const renamed =
        patch.name !== undefined &&
        String(patch.name).trim().replace(/\s+/g, '-') !== current?.name;
      const reason = renamed ? networkRefusal(this.doc, id) : '';

      if (reason) {
        this.refuse(reason);

        return null;
      }

      const next = updateNetwork(this.doc, id, patch);
      const name = findNetwork(next, id)?.name;

      return this.commit(
        next,
        name ? `Updated network ${name}` : 'Updated network',
      );
    },

    removeNetwork(id) {
      const name = findNetwork(this.doc, id)?.name;
      const reason = removalRefusal(this.doc, 'networks', id);

      if (reason) {
        this.refuse(reason);

        return null;
      }

      return this.commit(
        removeNetworks(this.doc, [id]),
        name ? `Removed network ${name}` : 'Removed network',
      );
    },

    // Refuses a change to the interfaces of an included device; returns
    // whether it did.
    refuseIncluded(nodeId) {
      const reason = includedReason(findNode(this.doc, nodeId));

      if (reason) {
        this.refuse(reason);
      }

      return Boolean(reason);
    },

    addInterface(nodeId, init) {
      if (this.refuseIncluded(nodeId)) {
        return null;
      }

      const result = addInterface(this.doc, nodeId, init);

      if (result.handle) {
        this.commit(result.doc, `Added interface ${result.handle.name}`);
      }

      return result.handle;
    },

    removeInterface(nodeId, handleId) {
      if (this.refuseIncluded(nodeId)) {
        return;
      }

      const node = findNode(this.doc, nodeId);
      const handle = (node?.device?.interfaces || []).find(
        (entry) => entry.id === handleId,
      );

      this.commit(
        removeInterface(this.doc, nodeId, handleId),
        handle
          ? `Removed interface ${handle.name} from ${nodeLabel(node)}`
          : 'Removed interface',
      );
    },

    renameInterface(nodeId, handleId, name) {
      if (this.refuseIncluded(nodeId)) {
        return;
      }

      this.commit(
        renameInterface(this.doc, nodeId, handleId, name),
        `Renamed interface to ${name}`,
      );
    },

    /**
     * @param {object} connection
     * @param {object} [options] announce: false when the caller shows the
     *   refusal itself (in its own alert), so it is not read twice
     */
    connect(connection, { announce = true } = {}) {
      const result = connect(this.doc, connection);

      if (result.error) {
        if (announce) {
          this.announce(result.error);
        }

        return { error: result.error };
      }

      this.commit(result.doc, 'Connected nodes');

      return { edge: result.edge };
    },

    // Canvas drag-to-connect: any device or switch to any device or switch.
    connectNodes(connection) {
      const result = connectNodes(this.doc, connection);

      if (result.error) {
        this.refuse(result.error);

        return { error: result.error };
      }

      this.commit(
        result.doc,
        result.node
          ? 'Connected devices through a new switch'
          : 'Connected nodes',
      );

      return { edges: result.edges, node: result.node };
    },

    updateEdge(id, patch) {
      this.commit(updateEdge(this.doc, id, patch), 'Updated connection');
    },

    removeSelection() {
      if (!this.selection.nodes.length && !this.selection.edges.length) {
        return;
      }

      this.remove(this.selection);
    },

    // Items outside `selection` that were selected stay selected, so
    // deleting one row or connection does not silently drop the rest. So do
    // items that would change an included device (see removalRefusal): a
    // removal of only those is refused, and one that also removes other
    // items says how many it kept, counting those in a deleted group.
    // Returns whether anything was removed.
    remove(selection) {
      const next = removeElements(this.doc, selection);
      const nodes = new Set(next.nodes.map((node) => node.id));
      const edges = new Set(next.edges.map((edge) => edge.id));
      const kept = removalKept(this.doc, selection);
      const removed =
        next.nodes.length < this.doc.nodes.length ||
        next.edges.length < this.doc.edges.length;

      if (!removed && kept.length) {
        this.refuse(
          kept.length === 1
            ? kept[0]
            : `Nothing was deleted: the ${count(kept.length, 'selected item')} ` +
                `are read only here. ${kept[0]}`,
        );

        return false;
      }

      this.commit(
        next,
        `${describeRemoval(this.doc, next)}` +
          (kept.length
            ? `. Kept ${count(kept.length, 'read-only item')} of included topologies`
            : ''),
      );
      this.selection = {
        nodes: this.selection.nodes.filter((id) => nodes.has(id)),
        edges: this.selection.edges.filter((id) => edges.has(id)),
      };

      return removed;
    },

    group() {
      const result = groupNodes(this.doc, this.selection.nodes);

      if (!result.group) {
        this.announce('Select at least one node to group.');

        return null;
      }

      this.commit(result.doc, 'Grouped selection');
      this.selection = { nodes: [result.group.id], edges: [] };

      return result.group;
    },

    ungroup(groupId) {
      const id = groupId || this.selection.nodes[0];
      const next = id ? ungroup(this.doc, id) : this.doc;

      // Only a group ungroups; anything else is no edit, and says so.
      if (next === this.doc) {
        this.announce('Select a group to ungroup.');

        return;
      }

      this.commit(next, 'Ungrouped nodes');
      this.selection = emptySelection();
    },

    /**
     * Lays the diagram out with the algorithm the settings choose, as one
     * commit, which the same toolbar button can then put back (see
     * restoreLayout). A layout that moves nothing is not an edit, as in
     * moveNodes: no undo step, no snapshot, and no restore.
     *
     * A layout can finish later (ELK runs in a Web Worker). Until it does,
     * layoutRunning is set and another request is turned away. A diagram
     * that changes meanwhile keeps the change, and the layout, made for
     * what the diagram was, is dropped.
     *
     * @param {object} [options] algorithm: a LAYOUT_ALGORITHMS id to use in
     *   place of the setting; the rest go to the algorithm
     * @returns {Promise<object|null>} the history entry, or null when
     *   nothing moved or the layout was not applied
     */
    async layout({ algorithm, ...options } = {}) {
      if (this.readOnly) {
        // The page alert reads it, as for any edit (see commit).
        this.setError('This draft is read only.');

        return null;
      }

      if (this.layoutRunning) {
        this.announce('Auto layout is still running.');

        return null;
      }

      const { history } = this;
      const entryId = history.currentEntry().id;
      let laid;

      this.layoutRunning = true;

      try {
        laid = await runLayout(
          algorithm || builderSettings.layoutAlgorithm,
          this.doc,
          options,
        );
      } catch (error) {
        // Stopped as the Builder closed or the session ended (see
        // stopLayoutEngine): there is no one to tell.
        if (error?.name === 'AbortError') {
          return null;
        }

        // A layout library's own words mean nothing to the viewer: they go
        // to the console.
        const known = error instanceof LayoutError;

        if (!known) {
          console.error('Auto layout failed.', error);
        }

        this.setError(
          `Auto layout failed. ${known ? error.message : 'The layout could not be computed.'}`,
        );

        return null;
      } finally {
        this.layoutRunning = false;
      }

      // Another edit, an undo or another document since: panning and
      // zooming change no entry.
      if (this.history !== history || history.currentEntry().id !== entryId) {
        this.announce(
          'The diagram changed during Auto layout, so the layout was not applied.',
        );

        return null;
      }

      const next = withGeometry(this.doc, laid);
      const geometry = changedGeometry(this.doc, next);

      if (Object.keys(geometry).length === 0) {
        this.announce('The diagram is already laid out.');

        return null;
      }

      const entry = this.commit(next, 'Applied automatic layout');

      if (entry) {
        this.layoutRestore = { entryId: entry.id, geometry };
      }

      return entry;
    },

    /**
     * Puts back what the last automatic layout moved, as one commit that
     * undo reverses like any other. Only offered while the diagram is still
     * exactly what that layout left (see canRestoreLayout).
     *
     * @returns {object|null} the history entry, or null when there is none
     */
    restoreLayout() {
      if (!this.canRestoreLayout) {
        this.announce('There is no earlier layout to restore.');

        return null;
      }

      // commit() drops the saved layout, so it is put back only once.
      return this.commit(
        restoreGeometry(this.doc, this.layoutRestore.geometry),
        'Restored previous layout',
      );
    },

    setInfo(patch) {
      this.commit(setDocumentInfo(this.doc, patch), 'Renamed diagram');
    },

    setViewport(viewport) {
      this.doc = setViewport(this.doc, viewport);
    },

    setGrid(patch) {
      this.commit(setGrid(this.doc, patch), 'Changed grid');
    },

    setScenario(scenario) {
      // Nothing to remove: no snapshot, and no claim that one was removed.
      if (!scenario && !this.doc.scenario) {
        this.announce('No scenario is attached.');

        return;
      }

      this.commit(
        setScenario(this.doc, scenario),
        scenario ? 'Attached scenario' : 'Removed scenario',
      );
    },

    copy() {
      const clipboard = copySelection(this.doc, this.selection);

      if (!clipboard.nodes.length && !clipboard.edges.length) {
        this.announce('Nothing is selected to copy.');

        return this.clipboard;
      }

      this.clipboard = clipboard;

      const parts =
        clipboard.nodes.length <= 3
          ? clipboard.nodes.map((node) => nodeLabel(node))
          : [count(clipboard.nodes.length, 'node')];

      if (clipboard.edges.length) {
        parts.push(count(clipboard.edges.length, 'connection'));
      }

      this.announce(`Copied ${listOf(parts)}.`);

      return this.clipboard;
    },

    paste() {
      if (!this.clipboard) {
        this.announce('Clipboard is empty.');

        return [];
      }

      const result = pasteClipboard(this.doc, this.clipboard);

      this.commit(result.doc, `Pasted ${count(result.nodeIds.length, 'node')}`);
      this.selection = { nodes: result.nodeIds, edges: [] };

      return result.nodeIds;
    },

    duplicate() {
      this.copy();

      return this.paste();
    },

    undo() {
      if (this.refuseWhileResolving()) {
        return;
      }

      if (!this.history.canUndo()) {
        this.announce('Nothing to undo.');

        return;
      }

      const label = this.history.undoLabel();

      this.doc = this.history.undo();
      // Dropped here, a redo back to the layout cannot offer it again either.
      this.layoutRestore = null;
      this.historyChanged();
      this.selection = emptySelection();
      // A blocked queue keeps the move on this device only; say so.
      this.announce(`Undid ${label}${this.unsavedNote() || '.'}`);
      this.moveHistoryCursor();
    },

    redo() {
      if (this.refuseWhileResolving()) {
        return;
      }

      if (!this.history.canRedo()) {
        this.announce('Nothing to redo.');

        return;
      }

      const label = this.history.redoLabel();

      this.doc = this.history.redo();
      this.historyChanged();
      this.selection = emptySelection();
      this.announce(`Redid ${label}${this.unsavedNote() || '.'}`);
      this.moveHistoryCursor();
    },

    select(selection) {
      this.selection = {
        nodes: selection?.nodes || [],
        edges: selection?.edges || [],
      };
    },

    selectAll() {
      this.selection = {
        nodes: (this.doc.nodes || []).map((node) => node.id),
        edges: (this.doc.edges || []).map((edge) => edge.id),
      };
      this.announce('Selected everything.');
    },

    clearSelection() {
      this.selection = emptySelection();
    },

    // --- theme ------------------------------------------------------------

    initTheme(element) {
      const storage = typeof localStorage !== 'undefined' ? localStorage : null;
      const mm =
        typeof window !== 'undefined' && window.matchMedia
          ? window.matchMedia.bind(window)
          : undefined;

      this.theme = readStoredTheme(storage);
      this.resolvedTheme = applyTheme(element, this.theme, mm);

      return this.resolvedTheme;
    },

    // The Settings dialog says nothing: its radio buttons speak for
    // themselves, and the live region would say it once the dialog closes.
    setTheme(theme, element, { announce = true } = {}) {
      const storage = typeof localStorage !== 'undefined' ? localStorage : null;
      const mm =
        typeof window !== 'undefined' && window.matchMedia
          ? window.matchMedia.bind(window)
          : undefined;

      this.theme = storeTheme(theme, storage);
      this.resolvedTheme = element
        ? applyTheme(element, this.theme, mm)
        : resolveTheme(this.theme, mm);
      if (announce) {
        this.announce(`Theme set to ${this.theme}.`);
      }

      return this.resolvedTheme;
    },

    // The toolbar's toggle, which goes by what System shows now (see
    // nextTheme).
    cycleTheme(element) {
      const mm =
        typeof window !== 'undefined' && window.matchMedia
          ? window.matchMedia.bind(window)
          : undefined;

      return this.setTheme(
        nextTheme(this.theme, resolveTheme('system', mm)),
        element,
      );
    },

    // --- session ----------------------------------------------------------

    // Forgets everything the signed-in user had here, on logout (see
    // session.js): the open draft, its queue and history, and the lists. A
    // request still under way for them is dropped when it answers.
    endSession() {
      sessionEpoch += 1;
      disksRequest = null;
      forgetUser(this);
    },

    // Whether the session ended since `epoch` was read (see endSession). A
    // draft opened or made for the previous user that answers afterwards is
    // dropped, with any queue it started, and the store left empty.
    sessionEndedSince(epoch) {
      if (epoch === sessionEpoch) {
        return false;
      }

      forgetUser(this);

      return true;
    },
  },
});

onBuilderSessionEnd(() => useBuilderStore().endSession());
