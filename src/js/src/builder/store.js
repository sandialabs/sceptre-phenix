// Pinia store orchestrating the Builder editor.
//
// The store is a thin shell: all document logic lives in the pure modules under
// src/builder, which keeps this file about wiring (selection, history, the
// autosave queue, API calls, announcements) rather than behaviour.

import { defineStore } from 'pinia';
import { markRaw, toRaw } from 'vue';

import { usePhenixStore } from '@/store.js';
import { roleAllowed } from '@/utils/rbac.js';

import {
  count,
  describeCombined,
  describeRemoval,
  listOf,
} from './announce.js';
import {
  builderApi,
  classifyError,
  endsBulk,
  errorMessage,
  fileTopology,
  libraryErrorMessage,
  preconditionETag,
  serverReason,
  serverSentence,
  shareErrors,
  sourceFileName,
} from './api.js';
import {
  appliedOperations,
  createAutosave,
  describeState,
  headOf,
  initialState,
  replayHistory,
  saveAnnouncement,
  serverCopyOf,
  snapshotIdOf,
  stampOf,
} from './autosave.js';
import { BulkError, bulkOutcome, mergeShareList, runBulk } from './bulk.js';
import {
  copySelection,
  DUPLICATE_NEEDS_NODES,
  pasteClipboard,
} from './clipboard.js';
import { DocumentError, parseDocument } from './decode.js';
import {
  PatternError,
  applyGroups,
  groupingPhrase,
  nothingToGroup,
  nothingToGroupByPattern,
  planGroups,
  planPatternGroups,
  rememberGroupPattern,
} from './grouping.js';
import { patternProblem } from './groupingPattern.js';
import { History, DEFAULT_HISTORY_LIMIT } from './history.js';
import {
  iconLibrary,
  ingestIcons,
  ingestSavedTemplateIcons,
  ingestTemplateIcons,
  savedTemplateIcons,
} from './iconLibrary.js';
import { settleIcons } from './icons.js';
import { createDraftStore } from './idb.js';
import { newId, uniqueName } from './ids.js';
import { issueTarget, responseIssues, toIssue } from './issues.js';
import {
  layoutChanges,
  restoreLayoutChanges,
  withGeometry,
  withLayoutChoice,
} from './layout.js';
import {
  LayoutError,
  documentLayout,
  layoutAlgorithm,
  ownLayout,
  runLayout,
} from './layouts/index.js';
import { changesFrom, mergeDocuments, possessive } from './merge.js';
import {
  FILE_TOKEN,
  LEGACY_TOKEN,
  PUBLISHED_TOKEN,
  configName,
  publishRefusal,
  replacedScenarioConfig,
} from './publish.js';
import {
  builderSchemaV1,
  ICON_SIZE_TITLES,
  isSchemaBundle,
  normalizeSchemaBundle,
} from './schema.js';
import {
  diagramFile,
  downloadedDiagram,
  onBuilderSessionEnd,
} from './session.js';
import { builderSettings } from './settings.js';
import { newestFirst, reasonMessage } from './share.js';
import { requestSignIn, signIn, signInAvailable } from './signin.js';
import { pageStorage } from './storage.js';
import { builderTabs } from './tabs.js';
import { importProblem, uniqueCollectionName } from './templateFile.js';
import {
  paletteTemplateGroups,
  templateByKey,
  templateContent,
  templatesFull,
} from './templates.js';
import { utf8Length } from './text.js';
import { MAX_NAME_BYTES, validateDocument } from './validate.js';
import {
  addInterface,
  addNode,
  addTemplate,
  combineIncluded,
  connect,
  connectNodes,
  createDocument,
  documentIconSize,
  documentScenarios,
  documentSummary,
  findNetwork,
  findNode,
  groupNodes,
  includedReason,
  insertLinePoint,
  kindLabel,
  linePointName,
  metadataOf,
  moveLinePoint,
  moveNodes,
  namedSwitches,
  networkRefusal,
  nodeLabel,
  removalKept,
  removalRefusal,
  removeElements,
  removeInterface,
  removeLinePoint,
  removeNetworks,
  removeTemplate,
  renameInterface,
  resizedBox,
  resizeNode,
  savedStamp,
  setDiagramNotes,
  setGrid,
  setIconSize,
  setLinePoints,
  setParent,
  setDocumentInfo,
  setScenarios,
  setViewport,
  sizeOf,
  ungroup,
  updateEdge,
  updateNetwork,
  updateNode,
  updateTemplate,
  withStamp,
} from './model.js';
import {
  DEFAULT_THEME,
  applyTheme,
  nextTheme,
  pageMatchMedia,
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

// Why a draft that changed again while its delete was sent a second time
// was not deleted.
const CHANGED_WHILE_DELETING =
  'It changed on the server while it was being deleted. Try again.';

// Why a bulk action left out a listed draft the server no longer finds.
// One of the user's own is gone. For another user's the server answers the
// same when the user may no longer see it, so the reason names both.
const DRAFT_DELETED = 'It was deleted since the list was read.';
const DRAFT_GONE =
  'It was deleted since the list was read, or you can no longer see it.';

function draftMissingReason(item) {
  return item.owner === usePhenixStore().username ? DRAFT_DELETED : DRAFT_GONE;
}

// Deletes a draft, and what this device kept for it. A draft that changed
// since `etag` was read (412) is deleted with its current ETag, once: the
// one the refusal carries, which a damaged draft has no other way to give,
// or else the draft's, read again. Resolves with null once it is deleted,
// or with why it was not: the failure, and whether it is that the draft
// changed again meanwhile.
async function removeDraft(owner, id, etag) {
  let retried = false;

  try {
    try {
      await builderApi.deleteDraft(owner, id, etag);
    } catch (error) {
      if (error?.response?.status !== 412) {
        throw error;
      }

      const current =
        preconditionETag(error) || (await builderApi.getDraft(owner, id)).etag;

      retried = true;
      await builderApi.deleteDraft(owner, id, current);
    }

    await forgetLocalDraft(owner, id);

    return null;
  } catch (error) {
    return {
      error,
      changed: retried && classifyError(error) === 'conflict',
    };
  }
}

// Why one item of a bulk action failed, in words.
function bulkReason(error) {
  return errorMessage(classifyError(error), error);
}

// Why a listed diagram could not be saved as a file: the server sent a
// document this version of the Builder cannot read.
const UNREADABLE_DOWNLOAD =
  'Its diagram cannot be read, so it was not downloaded. A newer version of phenix may have saved it.';

// Whether a failed download ends the run: as for the other batches, but a
// diagram this version cannot read (a DocumentError, which carries no
// response) is about that item alone, as the server did answer.
function endsDownloads(error) {
  return !(error instanceof DocumentError) && endsBulk(error);
}

/**
 * Saves listed items as Builder JSON files, one after another, each as
 * Download saves the open diagram: with a copy of every custom icon it
 * names (see downloadedDiagram and diagramFile in session.js). Every item
 * is tried, unless the session ends or the server cannot be reached.
 *
 * @param {object[]} items
 * @param {(item: object) => Promise<object>} read the item's document
 * @param {object} options save(file): saves {name, text}; onProgress(done,
 *   total); missing: why an item the server no longer finds was not saved
 * @returns {Promise<{done: object[], failures: Array<{item: object,
 *   reason: string}>}>}
 */
async function downloadEach(items, read, { save, onProgress, missing }) {
  const results = await runBulk(
    items,
    async (item) => {
      const doc = await read(item);

      save(diagramFile(await downloadedDiagram(doc)));
    },
    { limit: 1, onProgress, stop: endsDownloads },
  );

  return bulkOutcome(results, (error, item) => {
    if (error instanceof DocumentError) {
      return UNREADABLE_DOWNLOAD;
    }

    return classifyError(error) === 'missing'
      ? missing(item)
      : bulkReason(error);
  });
}

// What a layout run is for, as the viewer knows it, and what is dropped
// when the diagram changes before it ends.
const LAYOUT_TASKS = {
  layout: { name: 'Auto layout', dropped: 'the layout was not applied' },
  group: { name: 'Auto-group', dropped: 'no groups were made' },
};

// Why commit refuses an edit: the draft is read only, or a conflict is
// being resolved (see refuseWhileResolving).
const READ_ONLY_REFUSAL = 'This draft is read only.';
const RESOLVING_REFUSAL =
  'Not changed: the conflict is being resolved. Edit again once it is.';

// Lays `doc` out for layout or autoGroup (`task`, a LAYOUT_TASKS key). A
// layout can finish later (ELK runs in a Web Worker): until it does,
// layoutRunning is set and another request is turned away. Resolves to the
// geometry, or to null, with the reason told, when the draft is read only,
// another layout runs, the layout fails, or the diagram changes meanwhile:
// that diagram keeps the change, and the layout, made for what it was, is
// dropped.
async function layOut(store, doc, id, options, task) {
  const { name, dropped } = LAYOUT_TASKS[task];

  if (store.readOnly) {
    // The page alert reads it, as for any edit (see commit).
    store.setError('This draft is read only.');

    return null;
  }

  if (store.layoutRunning) {
    store.announce(
      `${store.autoGrouping ? 'Auto-group' : 'Auto layout'} is still running.`,
    );

    return null;
  }

  const { history } = store;
  const entryId = history.currentEntry().id;
  let laid;

  store.layoutRunning = true;
  store.autoGrouping = task === 'group';

  try {
    // Nodes leave room for their notes while the canvas shows them.
    laid = await runLayout(id, doc, { ...notesShown(), ...options });
  } catch (error) {
    // Stopped as the Builder closed or the session ended (see
    // stopLayoutEngine): there is no one to tell.
    if (error?.name === 'AbortError') {
      return null;
    }

    // A layout library's own words mean nothing to the viewer: they go to
    // the console.
    const known = error instanceof LayoutError;

    if (!known) {
      console.error(`${name} failed.`, error);
    }

    store.setError(
      `${name} failed. ${known ? error.message : 'The layout could not be computed.'}`,
    );

    return null;
  } finally {
    store.layoutRunning = false;
    store.autoGrouping = false;
  }

  // Another edit, an undo or another document since: panning and zooming
  // change no entry.
  if (store.history !== history || history.currentEntry().id !== entryId) {
    store.announce(`The diagram changed during ${name}, so ${dropped}.`);

    return null;
  }

  return laid;
}

// Whether the canvas shows node notes, as the layouts and the model's group
// sizes take it (see nodeFootprint): nodes leave room for their notes, and
// a group holds its members' notes, while they are shown.
function notesShown() {
  return { showNotes: builderSettings.showNodeNotes };
}

// Makes the planned groups (see applyGroups) for autoGroup and
// autoGroupByPattern, lays the diagram out with its layout, or the Settings
// default, so the groups do not overlap, and commits both as one edit that
// keeps that layout as the draft's, as layout does. `phrase` says how they
// were grouped ("by network"). Resolves to the groups made, or to null when
// the layout was not applied (see layOut).
async function makeGroups(store, planned, phrase, options) {
  const grouped = applyGroups(store.doc, planned, notesShown());
  const id = store.layoutToRun;
  const laid = await layOut(store, grouped.doc, id, options, 'group');

  if (!laid) {
    return null;
  }

  store.commit(
    withLayoutChoice(withGeometry(grouped.doc, laid), id),
    `Created ${count(grouped.groups.length, 'group')} ${phrase}`,
  );

  return grouped.groups;
}

// Bumped when the session ends (see endSession), so a listing that answers
// after the user logged out is dropped rather than shown to the next user.
let sessionEpoch = 0;

// Bumped on every read of a draft's snapshots, so only the last one asked
// for fills the History dialog (see fetchHistory).
let historyRequest = 0;

// Bumped by every merge of a conflict (see mergeConflict), so only the last
// one lets edits through again: a save of a merged diagram can meet another
// conflict, whose merge starts before the first one has ended.
let mergeRuns = 0;

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
// routes require (builderBaseAllowed in web/builder.go). The server
// decides; this only keeps controls it would refuse out of the way.
function configsAllowed(verb) {
  return Boolean(usePhenixStore().role) && roleAllowed('configs', verb);
}

// Why a published topology the server no longer lists was not deleted.
const PUBLISHED_GONE =
  'It was deleted or published again since the list was read.';

// What opening another user's draft says when the server cannot find it:
// the same whether it is missing or not shared with the user, as the server
// answers the same.
const NOT_SHARED = 'This draft does not exist, or it is not shared with you.';

// The lists as they are before they are read.
function emptyLists() {
  return { mine: [], shared: [], others: [], published: [], damaged: [] };
}

// What a template library holds at most (the limits of api/builder), used
// until the server says.
const LIBRARY_LIMITS = Object.freeze({
  templates: 200,
  collections: 50,
  shares: 25,
  nameBytes: 128,
  descriptionBytes: 1024,
  deviceBytes: 16384,
});

// The user's template library as it is before it is read.
function emptyLibrary() {
  return {
    status: 'idle',
    error: '',
    loaded: false,
    owner: '',
    items: [],
    collections: [],
    canShare: false,
    canPublish: false,
    damaged: false,
    limits: { ...LIBRARY_LIMITS },
  };
}

// The reads of the template library asked for, and the last one whose
// answer was kept: an answer older than the one kept is dropped (see
// fetchTemplates).
let libraryReads = 0;
let libraryReadKept = 0;

// The server changes a library with one compare-and-swap of its record,
// which it tries a few times. When the record keeps changing under it (the
// user's other tabs, say) it answers 503: the change is sent again, a
// moment later, this many times.
const LIBRARY_RETRIES = 2;
export const LIBRARY_RETRY_MS = 1000;

async function libraryWrite(send) {
  for (let attempt = 0; ; attempt += 1) {
    try {
      return await send();
    } catch (error) {
      if (error?.response?.status !== 503 || attempt >= LIBRARY_RETRIES) {
        throw error;
      }

      await new Promise((resolve) => {
        setTimeout(resolve, LIBRARY_RETRY_MS);
      });
    }
  }
}

/**
 * A change of the template library that was not sent, with why, in words.
 */
export class LibraryError extends Error {
  /**
   * @param {string} reason a sentence
   */
  constructor(reason) {
    super(reason);
    this.name = 'LibraryError';
    this.reason = reason;
  }
}

// Checks a diagram the server sent (see parseDocument). Whatever the check
// throws leaves as a DocumentError: the server answered, so the failure is
// about the diagram, and is never taken for a request that did not arrive
// (see describeError).
function serverDocument(value) {
  try {
    return parseDocument(value);
  } catch (error) {
    throw error instanceof DocumentError
      ? error
      : Object.assign(new DocumentError('', { code: 'unreadable' }), {
          cause: error,
        });
  }
}

// The document setDocument opens from a payload: decoded strictly, an
// unsupported or invalid one refused rather than silently repaired. Gives
// {document}, or {error}, the reason it cannot be opened.
function openableDocument(payload) {
  try {
    // A switch is named after its network, whatever label a document
    // saved or imported with another name still carries.
    return { document: namedSwitches(parseDocument(payload)) };
  } catch (error) {
    return {
      error:
        error instanceof DocumentError
          ? error.message
          : 'The document could not be read.',
    };
  }
}

// A merged document as setDocument takes it (see parseDocument), with its
// switches named after their networks, or why the strict check refuses it:
// each issue as "path: message".
function checkedMerge(doc) {
  try {
    return { doc: namedSwitches(parseDocument(doc)), issues: [] };
  } catch (error) {
    const listed =
      error instanceof DocumentError && Array.isArray(error.issues)
        ? error.issues.map((issue) => `${issue.path}: ${issue.message}`)
        : [];

    return {
      doc: null,
      issues: listed.length
        ? listed
        : [
            error instanceof DocumentError && error.message
              ? error.message
              : 'The merged diagram could not be read.',
          ],
    };
  }
}

// The selection with only what `doc` still holds.
function keptSelection(selection, doc) {
  const nodes = new Set((doc.nodes || []).map((node) => node.id));
  const edges = new Set((doc.edges || []).map((edge) => edge.id));

  return {
    nodes: selection.nodes.filter((id) => nodes.has(id)),
    edges: selection.edges.filter((id) => edges.has(id)),
  };
}

// Whether the UI was built with sign-in. Without it everyone is the same
// user, so a merge names no one, as the conflict panel does.
function signedIn() {
  const mode = import.meta.env.VITE_AUTH;

  return Boolean(mode) && mode !== 'disabled';
}

// Why merging a conflict is not available (see mergeConflict).
const MERGE_NO_BASE =
  'Merging is not available: the version your changes started from is no longer kept on the server, or could not be read.';
const MERGE_SLOW_BASE =
  'Merging is not available: the server took too long to send the version your changes started from.';
const MERGE_NO_THEIRS =
  'Merging is not available: the version on the server could not be read.';

// How long a merge waits for the server to send the version the unsaved
// changes started from (see mergeBase). The conflict panel stays hidden
// while it waits, so the wait is bounded: after it, merging is not
// available and the panel offers the other ways out.
export const MERGE_BASE_TIMEOUT_MS = 15000;

// What withTimeout resolves to when the time ran out first.
const TIMED_OUT = Symbol('timed out');

// Settles as `promise` does, or resolves to TIMED_OUT once `ms` have passed
// without it settling.
function withTimeout(promise, ms) {
  let timer;
  const expired = new Promise((resolve) => {
    timer = setTimeout(() => resolve(TIMED_OUT), ms);
  });

  return Promise.race([promise, expired]).finally(() => clearTimeout(timer));
}

// Why a merge that threw is not available, in words. The message is the
// error's own: nothing of the diagram or the request is added to it, and
// nothing is logged.
function mergeFailure(error) {
  const message =
    error instanceof Error && error.message ? error.message : 'unknown error';

  return `Merging failed: ${message}`;
}

// The source token of a draft made from a published diagram as it was read
// (see readPublished): "builder-doc/<document id>", or, for the diagram of a
// topology's Builder file, "builder-file/<topology>/<digest>", with the
// digest of what the file held. '' for a file whose digest the server did
// not say.
function publishedToken(read) {
  if (read.source !== 'file') {
    return `${PUBLISHED_TOKEN}${read.id}`;
  }

  return read.digest ? `${FILE_TOKEN}${read.target}/${read.digest}` : '';
}

// The drafts of mine made from a published diagram, by their source token,
// the one changed last first. A Builder file's token names what the file
// held, so a draft made before the file changed is not one of them.
function draftsFrom(drafts, token) {
  return newestFirst(
    (drafts || []).filter((draft) => token && draft.sourceToken === token),
  );
}

/**
 * The draft a published diagram is edited in, or null. In this order: the
 * draft of mine that published it ('publisher'), a draft someone shared
 * with me for editing that published it ('shared'), then the draft of mine
 * made from it before ('opened'). A draft that published the diagram is
 * found by the document its last publication stored, so it is still found
 * after it publishes again, which gives the diagram a new id. Among
 * several, the draft the diagram itself names comes first, then the one
 * changed last.
 *
 * A draft shared for viewing only, or seen through a role, is not one:
 * the diagram is opened to be edited.
 *
 * @param {{mine: object[], shared: object[]}} drafts as listed
 * @param {{id: string, draftId?: string}} published the diagram's id, and
 *   the id of the draft that published it when the listing says; the handle
 *   of a topology's Builder file, or '', has no publishing draft
 * @param {string} token the source token of a draft made from the diagram
 *   (see publishedToken), or '' to leave such drafts out
 * @returns {{draft: object, how: 'publisher'|'shared'|'opened'}|null}
 */
export function draftForPublished(drafts, published, token) {
  const id = published?.id && !fileTopology(published.id) ? published.id : '';
  const publishers = (list) => {
    const found = newestFirst(
      (list || []).filter(
        (draft) => id && draft?.publication?.documentId === id,
      ),
    );

    return (
      found.find((draft) => draft.id === published.draftId) || found[0] || null
    );
  };

  const mine = publishers(drafts?.mine);

  if (mine) {
    return { draft: mine, how: 'publisher' };
  }

  const shared = publishers(
    (drafts?.shared || []).filter(
      (draft) => draft?.via === 'share' && draft.access === 'edit',
    ),
  );

  if (shared) {
    return { draft: shared, how: 'shared' };
  }

  const [opened] = draftsFrom(drafts?.mine, token);

  return opened ? { draft: opened, how: 'opened' } : null;
}

// The title of a draft saved from local history: the diagram's name with
// " (local copy)". A name near the server's limit on names is cut first,
// without splitting a character, so the title still fits it.
const FORK_SUFFIX = ' (local copy)';

export function forkTitle(name) {
  const room = MAX_NAME_BYTES - utf8Length(FORK_SUFFIX);
  let base = '';
  let bytes = 0;

  for (const character of name || 'Diagram') {
    bytes += utf8Length(character);

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
    // What the last automatic layout changed (layoutChanges: where it moved
    // nodes from, the routes and layout choice it replaced), and the history
    // entry that layout made. It can be put back only while that entry is
    // current: any other edit, an undo or redo, or another document drops
    // it, so a restore never moves nodes to stale positions.
    layoutRestore: null,
    // Whether an automatic layout is under way (see layout), and whether it
    // is Auto-group's (see autoGroup).
    layoutRunning: false,
    autoGrouping: false,
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
    // publication (see draftCanUpdate in publish.js); when the server last
    // changed it, for Download; and the name of the uploaded file it was made
    // from, if it was, for the Inspector.
    draftRecord: {
      id: '',
      sourceToken: '',
      sourceFile: '',
      digest: '',
      publication: null,
      forked: null,
      updated: '',
    },
    // The experiment a publication of the open draft made, by name, while
    // it still exists and the user may get it, as the server last said:
    // when the draft was read or made, and when it published. '' for none.
    experiment: '',
    readOnly: false,
    // How the user reaches the open draft (see rememberAccess): 'owner',
    // 'edit' or 'view', and for another user's draft, 'share' or 'role'.
    access: '',
    via: '',
    // Whether the user may change who the open draft is shared with, and who
    // it is shared with ({user, access, stale}).
    canShare: false,
    shares: [],
    // Whether the user may share their drafts at all, as the server last
    // said of one of them (it depends on the user alone), or null while no
    // response has said.
    shareCapable: null,
    // Why a draft someone shared can no longer be saved (see accessLost in
    // autosave.js): 'view-only', 'role' or 'gone'; '' otherwise.
    accessLost: '',
    // The published diagram shown read only, with no draft of its own yet
    // (see viewPublishedDocument), or null: its id, name and target, and
    // source, 'store' for a published document or 'file' for the diagram of
    // the Builder file a topology names. A file's also has its path, the
    // digest of what it held, and topologyDiffers: whether the stored
    // topology is not what the file publishes. `read` is the diagram as the
    // server sent it, which editing it sends back unchanged.
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
    drafts: emptyLists(),
    // The user's template library (see fetchTemplates): status is 'idle'
    // before it is asked for, then 'loading', and 'ready' or 'failed' as
    // the last read went, with why it failed (error). loaded says whether
    // the rest is what the server answered: the library's owner, its
    // templates (items) and collections, and those of other users shared
    // with the user or published server-wide (each with its source and
    // owner), what the user may do (canShare, canPublish), whether the
    // stored library cannot be read (damaged), and its limits. A read that
    // fails keeps what an earlier one answered.
    templates: emptyLibrary(),
    publishing: false,
    publishResult: null,
    // The issues the server listed when it refused the last publish (see
    // responseIssues in issues.js), which the Publish dialog lists.
    publishIssues: [],
    // Go to, from a list of checks (see goToIssue): the node or connection
    // selected for it, the Inspector field to focus ('' for none), a
    // number that changes with every request, and `taken` once the
    // Inspector has acted on it (see takeFocusRequest).
    focusRequest: null,
    // Whether resolveConflict or a merge is under way (see
    // refuseWhileResolving).
    resolvingConflict: false,
    // The merge of a conflict's server version with the unsaved changes
    // (see mergeConflict), or null when none is under way: status is
    // 'merging' while it runs, 'review' when the user chooses (some fields
    // clash, or the merged diagram is refused), and 'unavailable' with why
    // (reason) when the version the changes started from cannot be read in
    // time, or the merge failed.
    // from names who saved the server's version (see changesFrom), clashes
    // says how many fields clash; base and server (the server copy, its
    // document checked) are what a review merges.
    merge: null,
    // The configs ("<kind>/<name>") the server refused to let this draft
    // update because someone else changed them since it published them (see
    // updateBlocker in publish.js).
    publishChanged: { draftId: '', targets: [] },
    queueWork: null,
    documents: [],
    // The ids of the published diagrams whose topologies are being deleted
    // (see deletePublished).
    deletingDocuments: [],
    sources: { images: [], topologies: [], scenarios: [], experiments: [] },
    // The file names of the disk images the server has (see fetchDisks), or
    // null while they are unknown: not read yet, or not readable. Drive
    // images are checked against them only while they are known.
    disks: null,
    // The stored scenarios the Inspector lists the apps of, by name (see
    // fetchScenario): the content last read, or why it could not be read.
    storedScenarios: {},
    serverHistory: [],
    // Whether the History dialog is reading serverHistory from the server,
    // and why that failed, if it did (see fetchHistory).
    historyLoading: false,
    historyError: '',
    // The ETag of the history the History dialog shows, when it is newer
    // than the queue's: read again after a delete met a change made
    // elsewhere (see deleteSnapshot). null otherwise.
    historyEtag: null,
    loading: false,
    error: '',
    // The form field the error is about, when the request that failed came
    // from a dialog form: a publish target's field (see publishRefusal), or
    // 'content' or 'name' for an import source the server refused, or
    // 'newName' for the name of the new topology an import makes. The
    // dialog marks that control. '' when the error is about no field.
    errorField: '',
    // Bumped whenever an error is set, so the same failure twice is shown
    // (and announced) twice.
    errorSeq: 0,
  }),

  getters: {
    canUndo: (state) => state.historyVersion >= 0 && state.history.canUndo(),
    canRedo: (state) => state.historyVersion >= 0 && state.history.canRedo(),
    // The layout that made the diagram's positions, which the draft keeps
    // (see ownLayout), or '' for none: the toolbar's Default.
    currentLayout: (state) => ownLayout(state.doc),
    // The layout a layout run uses: the draft's own, or the viewer's
    // default (see documentLayout).
    layoutToRun: (state) =>
      documentLayout(state.doc, builderSettings.layoutAlgorithm),
    // Whether the layout menu offers to put the previous layout back.
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
    // Whether the conflict panel shows: a conflict, unless a merge that may
    // end it without asking is under way (see mergeConflict).
    conflictShown: (state) =>
      state.saveState.status === 'conflict' &&
      state.merge?.status !== 'merging',
    // Why commit would refuse an edit now, or '' when it would not. A form
    // that shows a refusal itself reads it before it changes the diagram.
    editRefusal: (state) => {
      if (state.readOnly) {
        return READ_ONLY_REFUSAL;
      }

      return state.resolvingConflict ? RESOLVING_REFUSAL : '';
    },
    saveStateText: (state) => describeState(state.saveState),
    // What the signed-in role may do (see configsAllowed). Blank, Import and
    // Upload, and editing a published diagram, all create a draft.
    canCreateDrafts: () => configsAllowed('create'),
    canPublish: () => configsAllowed('update'),
    canDeleteDrafts: () => configsAllowed('delete'),
    // Whether the open draft is the user's own. A diagram not saved yet is.
    isOwner: (state) =>
      state.access
        ? state.access === 'owner'
        : !state.owner || state.owner === usePhenixStore().username,
    // The owner of the open draft, when someone shared it with the user and
    // still does: once the share, or the draft, is gone, no one is named.
    sharedBy: (state) =>
      state.via === 'share' && state.accessLost !== 'gone' ? state.owner : '',
    // The experiment the open diagram's publication made: a draft's own, or
    // that of the published diagram shown read only, as its listed row says.
    experimentName: (state) =>
      state.published
        ? state.documents.find((row) => row.id === state.published.id)
            ?.experiment || ''
        : state.experiment,
    // The drafts the server can no longer read: the user's own, and other
    // users' (on the Other users' drafts tab, never Shared Drafts).
    damagedDrafts: (state) => {
      const user = usePhenixStore().username;
      const all = state.drafts.damaged || [];

      return {
        mine: all.filter((item) => item.owner === user),
        others: all.filter((item) => item.owner !== user),
      };
    },
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
    // The device templates the palette offers, in groups: those saved in
    // the open diagram, then those of the user's library, or the built-in
    // ones while there is no library to show (see templates.js).
    paletteTemplateGroups: (state) =>
      paletteTemplateGroups(state.doc, state.templates),
    // The template a palette entry's key names ("diagram:<id>", "own:<id>",
    // "shared:<owner>/<id>", "server:<owner>/<id>", "builtin:<id>"), or
    // undefined.
    templateByKey: (state) => (key) =>
      templateByKey(state.doc, key, state.templates),
    // The user's own templates and collections, in the order the library
    // keeps them: the order they were added in.
    ownTemplates: (state) =>
      state.templates.items.filter((template) => template.source === 'own'),
    ownCollections: (state) =>
      state.templates.collections.filter(
        (collection) => collection.source === 'own',
      ),
    // What the signed-in role may do to its library (see configsAllowed):
    // add templates and collections, change them, delete them.
    templateRights: () => ({
      create: configsAllowed('create'),
      update: configsAllowed('update'),
      delete: configsAllowed('delete'),
    }),
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
        sourceFile: draft.sourceFile || '',
        digest: draft.digest || '',
        publication: draft.publication || null,
        forked: draft.forked || null,
        updated: draft.updated || '',
      };
    },

    /**
     * Keeps how the user reaches a draft the server sent (access, via,
     * canShare, shares). A response that does not say (a save, a create)
     * changes nothing.
     *
     * @param {object} draft
     */
    rememberAccess(draft) {
      if (!draft?.access || typeof draft.access !== 'string') {
        return;
      }

      this.access = draft.access;
      this.via = draft.via || '';
      this.canShare = draft.canShare === true;
      this.shares = Array.isArray(draft.shares) ? draft.shares : [];

      if (draft.access === 'owner') {
        this.shareCapable = this.canShare;
      }
    },

    // The access of a draft the user just made: their own, which they may
    // share when the server has said they may share any.
    ownDraft() {
      this.access = 'owner';
      this.via = '';
      this.canShare = this.shareCapable === true;
      this.shares = [];
      this.accessLost = '';
    },

    // A draft someone shared can no longer be saved (see accessLost in
    // autosave.js): it is read only from now on, and the view says why.
    loseAccess(why) {
      this.accessLost = why;
      this.readOnly = true;

      if (why === 'view-only') {
        this.access = 'view';
      }
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
     * The document recorded carries copies only of custom icons its nodes
     * name that the icon library lacks or holds otherwise (see settleIcons):
     * this is the one place that drops the others for every edit.
     *
     * @param {object} doc next document
     * @param {string} label short description
     */
    commit(doc, label) {
      if (this.readOnly) {
        // The page alert reads it; announcing it too would say it twice.
        this.setError(READ_ONLY_REFUSAL);

        return null;
      }

      if (this.refuseWhileResolving()) {
        return null;
      }

      const settled = settleIcons(doc, iconLibrary);

      this.doc = settled;
      this.layoutRestore = null;

      const entry = this.history.push(settled, label);
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

      this.announce(RESOLVING_REFUSAL);

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

    initAutosave({ owner, draftId, etag, entries, cursor, queue, serverHead }) {
      const phenix = usePhenixStore();

      this.owner = owner;
      this.draftId = draftId;

      if (etag) {
        this.etag = etag;
      }

      this.autosave?.dispose();
      // What the queue before this one was still sending, and the problem
      // last announced for it, are that draft's. A draft closed while it
      // was being saved goes on in the background (see
      // createBackgroundSaves in leave.js): this draft's saves do not wait
      // for it (see saveNow), and its first save is not the end of that
      // draft's problem (see announceSaveState).
      this.queueWork = null;
      this.saveAnnounced = null;
      this.merge = null;
      this.autosave = markRaw(
        createAutosave({
          api: builderApi,
          store: createDraftStore(),
          actor: phenix.username || 'anonymous',
          // Other tabs with the draft open (see tabs.js).
          tabs: builderTabs,
          signInHere: signInAvailable,
          // Someone else saved the draft first: their version is merged
          // with the changes queued here.
          onConflict: (server) => this.mergeConflict(server),
          onState: (state) => {
            this.saveState = state;

            if (state.etag) {
              this.etag = state.etag;
            }

            if (state.accessLost && state.accessLost !== this.accessLost) {
              this.loseAccess(state.accessLost);
            }

            this.announceSaveState(state);
          },
          onDraft: (envelope, operation) => {
            this.rememberDraft(envelope.draft);
            // A read of the draft (after a conflict) says who it is shared
            // with now.
            this.rememberAccess(envelope.draft);

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

              this.stampEntry(entry, stampOf(envelope));

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
        serverHead,
      });
    },

    /**
     * Copies the stamp a save answered with (who made the document and who
     * saved it last, and when; see withStamp in model.js) into the history
     * entry the save stored, so undo back to it shows what the server holds
     * of it. While that entry is the one shown, the diagram takes the stamp
     * too: it is the entry's snapshot, or a copy of it with another viewport.
     *
     * @param {object} [entry] history entry
     * @param {object|null} stamp stampOf() the save's answer
     */
    stampEntry(entry, stamp) {
      if (!entry || !stamp) {
        return;
      }

      const shown = entry === this.history.currentEntry();
      const same = shown && toRaw(this.doc) === toRaw(entry.snapshot);

      entry.snapshot = withStamp(entry.snapshot, stamp);

      if (shown) {
        this.doc = same ? entry.snapshot : withStamp(this.doc, stamp);
      }
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
      this.draftId = doc.metadata.id;
      this.etag = null;
      this.readOnly = false;
      this.access = '';
      this.via = '';
      this.canShare = false;
      this.shares = [];
      this.accessLost = '';
      this.published = null;
      this.rememberDraft({});
      this.experiment = '';
      this.saveState = initialState();
      this.saveAnnounced = null;
      this.merge = null;

      return doc;
    },

    /**
     * Replaces the document. The payload is decoded strictly: an unsupported or
     * invalid document is rejected with a message instead of being silently
     * repaired.
     *
     * @param {object} payload
     * @param {object} [options] label, resetHistory, and announce: false to
     *   load it with a reset history unannounced (see openImported)
     * @returns {object|null} document, or null when rejected
     */
    setDocument(
      payload,
      { label = 'Document loaded', resetHistory = true, announce = true } = {},
    ) {
      const { document, error } = openableDocument(payload);

      if (!document) {
        // The page alert (or the dialog that called this) reads it.
        this.setError(error);

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
     * @param {object} [options] document, title, sourceToken; sourceFile:
     *   the name of the uploaded file the document was read from;
     *   announcement: what to say once the draft exists ('' for nothing)
     */
    async createDraft({
      document,
      title,
      sourceToken,
      sourceFile,
      announcement = 'Draft created.',
    } = {}) {
      const phenix = usePhenixStore();
      const doc = document || this.doc;
      const epoch = sessionEpoch;

      this.clearError();

      try {
        const envelope = await builderApi.createDraft({
          title: title || metadataOf(doc).name || 'Untitled diagram',
          sourceToken,
          // Left out of the request when there is none, or none the server
          // would record.
          sourceFile: sourceFileName(sourceFile) || undefined,
          document: doc,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        this.owner = envelope.draft?.owner || phenix.username || '';
        this.draftId = envelope.draft?.id || metadataOf(doc).id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = false;
        this.published = null;
        this.rememberDraft(envelope.draft);
        this.experiment = envelope.draft?.experiment || '';
        this.ownDraft();
        // The server stored the document with the stamp it answers with,
        // which the editor's copy takes. Its switches are named after their
        // networks, as those of a draft read from the server are (see
        // setDocument): the document sent may be a published one, sent as
        // it was read.
        const stored = envelope.document
          ? serverDocument(envelope.document)
          : doc;
        const stamp = stampOf(envelope);

        this.doc = namedSwitches(stamp ? withStamp(stored, stamp) : stored);
        this.history = markRaw(new History(this.doc, DEFAULT_HISTORY_LIMIT));
        this.historyChanged();
        this.linkCurrentHistorySnapshot(envelope);

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
          serverHead: headOf(envelope),
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (announcement) {
          this.announce(announcement);
        }

        if (this.shareCapable === null) {
          this.learnShareCapable();
        }

        return envelope;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(
            String(sourceToken || '').startsWith(FILE_TOKEN)
              ? this.describeFileError(error, 'create the draft')
              : this.describeError(error, 'create the draft'),
          );
        }

        return null;
      }
    },

    /**
     * Reads a draft from the server and makes it the open one.
     *
     * @param {string} owner
     * @param {string} id
     * @param {object} [options] quiet: says nothing of the draft having
     *   opened, for a draft loaded while the drafts stay on screen (a card's
     *   Publish); changes recovered from this device are still announced
     * @returns {Promise<object|null>} the draft's document, or null
     */
    async loadDraft(owner, id, { quiet = false } = {}) {
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
        this.experiment = envelope.draft?.experiment || '';
        this.access = '';
        this.via = '';
        this.canShare = false;
        this.shares = [];
        this.accessLost = '';
        this.rememberAccess(envelope.draft);

        const loaded = this.setDocument(envelope.document, {
          label: 'Draft loaded',
          announce: !quiet,
        });

        if (!loaded) {
          return null;
        }

        this.linkCurrentHistorySnapshot(envelope);
        const serverETag = envelope.etag;
        const serverHead = headOf(envelope);

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
          serverHead,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        await this.recoverLocalHistory(
          serverETag,
          serverHead,
          serverCopyOf(envelope),
        );

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        // Another user's draft says whose it is, and whether it can be
        // edited: others may be editing it at the same time.
        if (!quiet && phenix.username && this.owner !== phenix.username) {
          const name =
            metadataOf(this.doc).name || envelope.draft?.title || 'Untitled';

          this.announce(
            this.readOnly
              ? `Opened ${this.owner}'s draft ${name}, view only.`
              : `Opened ${this.owner}'s draft ${name}. You can edit it; others may be editing too.`,
          );
        }

        return this.doc;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          const theirs = owner !== usePhenixStore().username;
          const kind = classifyError(error);

          this.setError(
            theirs && kind === 'missing'
              ? NOT_SHARED
              : this.describeError(error, 'load the draft'),
          );

          // Someone else's draft that is gone, or no longer shared, leaves
          // the lists, so its card does not offer the same failure again.
          if (theirs && (kind === 'missing' || kind === 'forbidden')) {
            await this.fetchDrafts({ keepError: true });
          }
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
     * @param {object} [serverHead] headOf() the draft as read
     * @param {object} [server] the draft as read (see serverCopyOf): a
     *   queue based on an older ETag is merged with it (see mergeConflict)
     * @returns {Promise<boolean>} whether unsaved work was recovered
     */
    async recoverLocalHistory(
      serverETag = this.etag,
      serverHead = null,
      server = null,
    ) {
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
          serverHead,
          entries: [],
          cursor: 0,
          queue: [],
        });

        return false;
      }

      // An entry the server holds is the snapshot it lists: it takes that
      // snapshot's id, and the stamp the save, whose answer never arrived,
      // wrote into it (see savedStamp).
      const listed = new Map(
        this.serverHistory.map((snapshot) => [snapshot.id, snapshot]),
      );
      const opened = this.history.current();
      const entries = (local.entries || []).map((entry) => {
        const snapshotId = applied.snapshotIds.get(entry.id);

        return snapshotId
          ? {
              ...entry,
              serverSnapshotId: snapshotId,
              snapshot: withStamp(
                entry.snapshot,
                savedStamp(opened, listed.get(snapshotId)),
              ),
            }
          : entry;
      });
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
        // The head of the ETag the queue goes on from.
        serverHead: etag === serverETag ? serverHead : local.serverHead,
        entries,
        cursor: local.cursor,
        queue: pending,
      });

      this.announce(
        `Recovered ${count(pending.length, 'unsaved change')} from this device.`,
      );

      if (etag !== serverETag) {
        this.autosave.conflict(undefined, { server });

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

      // Save now while the session is over opens the sign-in, as Retry
      // saving does.
      if (
        announce &&
        this.saveState.signInNeeded &&
        signIn.needed &&
        requestSignIn()
      ) {
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
     * Sends the queue again. While the session is over, the same token
     * would be refused again: the sign-in opens instead, where it can (see
     * signin.js), and signing in sends the queue.
     *
     * @param {object} [options] announce: say how the retry ended (the
     *   toolbar's Retry saving), even when it failed the same way again
     */
    async retrySave({ announce = false } = {}) {
      if (!this.autosave) {
        return this.saveState;
      }

      if (this.saveState.signInNeeded && signIn.needed && requestSignIn()) {
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
        const title = options.title || forkTitle(metadataOf(this.doc).name);
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
          this.merge = null;
          this.rememberDraft(envelope.draft);
          this.experiment = envelope.draft?.experiment || '';
          // The new draft is the user's own, even when the one it leaves
          // was shared with them and they lost access to it.
          this.ownDraft();
          this.readOnly = false;
          this.etag = envelope.etag;
          this.history.entries = entries;

          // Each entry now names its snapshot in the new draft, so undo and
          // redo move that draft's cursor, never the old draft's, and holds
          // the stamp the new draft stored it with.
          this.history.entries.forEach((entry) => {
            const stamp = envelope.stamps?.get(entry.id);

            entry.serverSnapshotId = envelope.snapshotIds.get(entry.id);

            if (stamp) {
              entry.snapshot = withStamp(entry.snapshot, stamp);
            }
          });

          const renamed = setDocumentInfo(this.doc, { name: title });
          const stamp = envelope.stamps?.get(this.history.currentEntry().id);

          this.doc = stamp ? withStamp(renamed, stamp) : renamed;
          this.historyChanged();
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
     * Merges the server's version of the open draft, which a save found
     * someone else saved first (see onConflict in autosave.js), with the
     * changes not saved yet (see merge.js). The base is the server snapshot
     * those changes started from (the head the queue last confirmed): an
     * entry of the undo history holds it, or the server is asked for it.
     * When no field clashes and the strict check takes the merged diagram,
     * it is saved on top of the server's version (see saveMerged).
     * Otherwise the conflict panel offers Review and merge (status
     * 'review'), or says why merging is not available (status
     * 'unavailable'); saving the history as a new draft and discarding it
     * stay offered. Edits are refused while the merge runs, as they are
     * while a conflict is resolved, since the queue is replaced.
     *
     * @param {object} server the draft as the server holds it now (see
     *   serverCopyOf in autosave.js)
     * @returns {Promise<object|null>} the merged document once saved, or
     *   null
     */
    async mergeConflict(server) {
      const { autosave } = this;

      if (!autosave || !server || this.readOnly) {
        return null;
      }

      const run = (mergeRuns += 1);
      const epoch = sessionEpoch;
      const current = () =>
        !this.sessionEndedSince(epoch) && this.autosave === autosave;
      const from = changesFrom(
        signedIn() ? server.lastModifiedBy : '',
        usePhenixStore().username || '',
      );
      const blocked = (status, reason = '', more = {}) => {
        this.merge = markRaw({
          status,
          from,
          reason,
          clashes: 0,
          base: null,
          server,
          ...more,
        });
      };

      blocked('merging');
      this.resolvingConflict = true;

      try {
        const { doc: base, reason } = await this.mergeBase(autosave);

        if (!current()) {
          return null;
        }

        if (!base) {
          blocked('unavailable', reason);

          return null;
        }

        let theirs = null;

        try {
          theirs = namedSwitches(serverDocument(server.document));
        } catch {}

        if (!theirs) {
          blocked('unavailable', MERGE_NO_THEIRS);

          return null;
        }

        const checked = { ...server, document: theirs };
        const result = mergeDocuments(base, toRaw(this.doc), theirs);
        const merged =
          result.clashes.length === 0 ? checkedMerge(result.doc).doc : null;

        if (merged) {
          return await this.saveMerged(merged, checked, from);
        }

        blocked('review', '', {
          clashes: result.clashes.length,
          base,
          server: checked,
        });

        return null;
      } catch (error) {
        // Whatever failed, the merge ends: the conflict panel shows again,
        // with why, and saving the history as a new draft and discarding it
        // stay offered. A later merge has the panel to itself.
        if (run === mergeRuns && current()) {
          blocked('unavailable', mergeFailure(error));
        }

        return null;
      } finally {
        if (run === mergeRuns) {
          this.resolvingConflict = false;
        }
      }
    },

    /**
     * The document the unsaved changes started from: the server snapshot
     * the queue last confirmed, from the undo history or the queue's
     * entries when one holds it, else read from the server, which has
     * MERGE_BASE_TIMEOUT_MS to send it.
     *
     * @param {object} autosave the queue
     * @returns {Promise<{doc: object|null, reason: string}>} doc is null,
     *   with why merging is not available, when it cannot be read: the
     *   server no longer keeps it, cannot be reached, or took too long
     */
    async mergeBase(autosave) {
      const record = autosave.record;
      const id = record?.serverHead?.snapshotId;
      const missing = { doc: null, reason: MERGE_NO_BASE };

      if (!id) {
        return missing;
      }

      const held = [...this.history.entries, ...(record.entries || [])].find(
        (entry) => entry.serverSnapshotId === id && entry.snapshot,
      );

      if (held) {
        return { doc: toRaw(held.snapshot), reason: '' };
      }

      try {
        const read = await withTimeout(
          builderApi.getSnapshot(record.owner, record.draftId, id),
          MERGE_BASE_TIMEOUT_MS,
        );

        if (read === TIMED_OUT) {
          return { doc: null, reason: MERGE_SLOW_BASE };
        }

        return read?.document
          ? { doc: namedSwitches(serverDocument(read.document)), reason: '' }
          : missing;
      } catch {
        return missing;
      }
    },

    /**
     * Saves a merged document on top of the server's version: the queue
     * goes on from that version's ETag and holds one snapshot of the merged
     * document, named "Merged changes from <user>" (see rebase in
     * autosave.js), and the merge is announced.
     *
     * The undo history becomes the server's version, then the merged
     * document, one step apart. The entries of the edits that were not
     * saved are dropped rather than rebased: each holds the diagram as it
     * was before the other editor's changes, so undoing to one would take
     * those changes out again, and the server holds none of them, so a
     * move of the draft's cursor to one could not be saved. The merged
     * document holds every one of those edits.
     *
     * @param {object} merged the merged document, checked
     * @param {object} server the server copy, its document checked
     * @param {string} from who saved the server's version (see changesFrom)
     * @returns {Promise<object>} the document
     */
    async saveMerged(merged, server, from) {
      const { autosave } = this;
      const doc = settleIcons(merged, iconLibrary);
      const entries = [
        {
          id: newId(),
          label: 'initial',
          snapshot: server.document,
          serverSnapshotId: server.head?.snapshotId,
        },
        { id: newId(), label: `Merged changes from ${from}`, snapshot: doc },
      ];

      this.history.restore(entries, 1);
      this.historyChanged();
      this.doc = doc;
      this.layoutRestore = null;
      this.selection = keptSelection(this.selection, doc);
      this.etag = server.etag;
      this.rememberDraft(server.draft);
      this.rememberAccess(server.draft);

      if (Array.isArray(server.history)) {
        this.serverHistory = server.history;
      }

      await autosave.rebase({
        etag: server.etag,
        head: server.head,
        entries,
      });
      // The queue holds the merge now and the conflict has ended, so the
      // merge dialog closes with the conflict panel already gone (see
      // mergeReview in Builder.vue), and an edit goes after the merge.
      this.merge = null;
      this.resolvingConflict = false;
      this.announce(`Merged ${possessive(from)} changes with yours.`);
      this.trackQueue(autosave.flush());

      return doc;
    },

    /**
     * The merge the merge dialog reviews: the conflict's base and server
     * version with the diagram as it is now.
     *
     * @returns {object|null} mergeDocuments() result, or null when no
     *   review is offered
     */
    mergeReview() {
      const { merge } = this;

      if (merge?.status !== 'review') {
        return null;
      }

      return markRaw(
        mergeDocuments(merge.base, toRaw(this.doc), merge.server.document),
      );
    },

    /**
     * Why the merge with `choices` would be refused, as the strict check
     * says it (see parseDocument): each issue as "path: message".
     *
     * @param {Object<string, 'mine'|'theirs'>} choices by clash key
     * @returns {string[]} none when it would be saved
     */
    mergeProblems(choices = {}) {
      const { merge } = this;

      if (merge?.status !== 'review') {
        return [];
      }

      return checkedMerge(
        mergeDocuments(
          merge.base,
          toRaw(this.doc),
          merge.server.document,
          choices,
        ).doc,
      ).issues;
    },

    /**
     * Saves the merge the user reviewed, with the version they chose for
     * each clashing field (see saveMerged). A merged diagram the strict
     * check refuses is not saved: the issues say why. Nothing is saved
     * either while another merge runs (another change arrived, and the
     * merge is made again with it): busy says so.
     *
     * @param {Object<string, 'mine'|'theirs'>} choices by clash key
     * @returns {Promise<{saved: boolean, issues: string[], busy?: boolean}>}
     */
    async saveMergeChoices(choices = {}) {
      const { merge, autosave } = this;

      if (merge?.status === 'merging' || this.resolvingConflict) {
        return { saved: false, busy: true, issues: [] };
      }

      if (merge?.status !== 'review' || !autosave) {
        return { saved: false, issues: [] };
      }

      const checked = checkedMerge(
        mergeDocuments(
          merge.base,
          toRaw(this.doc),
          merge.server.document,
          choices,
        ).doc,
      );

      if (!checked.doc) {
        return { saved: false, issues: checked.issues };
      }

      const run = (mergeRuns += 1);

      this.resolvingConflict = true;

      try {
        await this.saveMerged(checked.doc, merge.server, merge.from);
      } finally {
        if (run === mergeRuns) {
          this.resolvingConflict = false;
        }
      }

      return { saved: true, issues: [] };
    },

    /**
     * Deletes a draft. A card's ETag can be older than the server's, as it
     * is right after Back to drafts until the list is read again: the
     * draft is then deleted with its current ETag, once (see removeDraft).
     *
     * @param {string} owner
     * @param {string} id
     * @param {string} [etag]
     * @param {string} [title] named in the announcement
     */
    async deleteDraft(owner, id, etag, title = '') {
      this.clearError();

      const failure = await removeDraft(owner, id, etag);

      if (failure) {
        this.setError(
          failure.changed
            ? `Could not delete the draft. ${CHANGED_WHILE_DELETING}`
            : this.describeError(failure.error, 'delete the draft'),
        );

        return false;
      }

      this.announce(title ? `Deleted draft ${title}.` : 'Draft deleted.');
      await this.fetchDrafts();

      return true;
    },

    /**
     * Deletes several listed drafts, a few at a time, each as deleteDraft
     * deletes one. Every draft is tried, unless the session ends or the
     * server cannot be reached; what failed is returned with why, not put
     * in the page alert, and nothing is announced: the caller says what the
     * run came to. A draft of the user's own that the server no longer
     * finds counts as deleted. The lists are read again once, at the end.
     *
     * @param {{owner: string, id: string, etag?: string}[]} items
     * @param {object} [options] before(item): awaited before a draft is
     *   deleted (the view ends its saves in the background); onProgress(done,
     *   total): called as each draft ends
     * @returns {Promise<{done: object[], failures: Array<{item: object,
     *   reason: string}>}>}
     */
    async deleteDrafts(items, { before, onProgress } = {}) {
      const results = await runBulk(
        items,
        async (item) => {
          await before?.(item);

          const failure = await removeDraft(item.owner, item.id, item.etag);

          if (!failure) {
            return;
          }

          if (failure.changed) {
            throw new BulkError(CHANGED_WHILE_DELETING);
          }

          if (classifyError(failure.error) !== 'missing') {
            throw failure.error;
          }

          // The server no longer finds it. One of the user's own was
          // deleted elsewhere since the list was read, which is what was
          // asked for: it counts as deleted, and what this device kept for
          // it goes too.
          if (item.owner !== usePhenixStore().username) {
            throw new BulkError(DRAFT_GONE);
          }

          await forgetLocalDraft(item.owner, item.id);
        },
        { onProgress, stop: endsBulk },
      );

      await this.fetchDrafts({ keepError: true });

      return bulkOutcome(results, bulkReason);
    },

    /**
     * Deletes the topology a published diagram is current for, and its
     * published diagrams with it. Drafts and experiments made from it stay:
     * publishing a draft again creates the topology again. A diagram the
     * server no longer lists as current (published again or deleted
     * elsewhere) is refused, and the list is read again.
     *
     * @param {{id: string, target?: string}} item a listed published diagram
     * @returns {Promise<boolean>} whether it was deleted
     */
    async deletePublished(item) {
      const name = item.target || item.id;

      if (this.deletingDocuments.includes(item.id)) {
        return false;
      }

      this.clearError();

      try {
        await this.removePublished(item);
        this.announce(`Deleted topology ${name}.`);

        return true;
      } catch (error) {
        const missing = classifyError(error) === 'missing';

        this.setError(
          missing
            ? `Could not delete topology ${name}. ${PUBLISHED_GONE}`
            : this.describeError(error, `delete topology ${name}`),
        );

        if (missing) {
          await this.fetchDocuments();
        }

        return false;
      }
    },

    // Deletes a published topology, its card saying so meanwhile, and takes
    // it off the lists. It rejects with the server's failure.
    async removePublished(item) {
      const name = item.target || item.id;

      this.deletingDocuments = [...this.deletingDocuments, item.id];

      try {
        await builderApi.deleteDocument(item.id);

        this.documents = this.documents.filter((entry) => entry.id !== item.id);
        // Until the list is read again, Publish must not offer to update it.
        this.sources = {
          ...this.sources,
          topologies: (this.sources.topologies || []).filter(
            (entry) =>
              (typeof entry === 'string' ? entry : entry?.name) !== name,
          ),
        };
      } finally {
        this.deletingDocuments = this.deletingDocuments.filter(
          (id) => id !== item.id,
        );
      }
    },

    /**
     * Deletes the topologies of several listed published diagrams, each as
     * deletePublished deletes one, one at a time: the server takes them one
     * at a time anyway. Every topology is tried, unless the session ends or
     * the server cannot be reached; what failed is returned with why, not
     * put in the page alert, and nothing is announced. The list is read
     * again once, at the end.
     *
     * @param {{id: string, target?: string}[]} items listed published
     *   diagrams
     * @param {object} [options] onProgress(done, total): called as each
     *   topology ends
     * @returns {Promise<{done: object[], failures: Array<{item: object,
     *   reason: string}>}>}
     */
    async deletePublishedMany(items, { onProgress } = {}) {
      const results = await runBulk(
        items,
        (item) => this.removePublished(item),
        { limit: 1, onProgress, stop: endsBulk },
      );

      await this.fetchDocuments();

      return bulkOutcome(results, (error) =>
        classifyError(error) === 'missing' ? PUBLISHED_GONE : bulkReason(error),
      );
    },

    /**
     * Saves several listed drafts as Builder JSON files, one after another:
     * each draft's current version as the server has it, read first, with
     * a copy of every custom icon it names. What failed is returned with
     * why, not put in the page alert, and nothing is announced.
     *
     * @param {{owner: string, id: string}[]} items listed drafts
     * @param {object} options save(file): saves {name, text} (file-saver in
     *   the app); onProgress(done, total)
     * @returns {Promise<{done: object[], failures: Array<{item: object,
     *   reason: string}>}>}
     */
    async downloadDrafts(items, { save, onProgress } = {}) {
      return downloadEach(
        items,
        async (item) =>
          serverDocument(
            (await builderApi.getDraft(item.owner, item.id)).document,
          ),
        { save, onProgress, missing: draftMissingReason },
      );
    },

    /**
     * Saves several listed published diagrams as Builder JSON files, one
     * after another, each read as readPublished reads it, likewise.
     *
     * @param {{id: string}[]} items listed published diagrams
     * @param {object} options save(file); onProgress(done, total)
     * @returns {Promise<{done: object[], failures: Array<{item: object,
     *   reason: string}>}>}
     */
    async downloadPublishedMany(items, { save, onProgress } = {}) {
      return downloadEach(
        items,
        async (item) => (await this.readPublished(item.id)).document,
        { save, onProgress, missing: () => PUBLISHED_GONE },
      );
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
    // the lists; any other failure keeps what they showed. With keepError,
    // the page alert keeps saying what failed before the lists were read.
    async fetchDrafts({ keepError = false } = {}) {
      const epoch = sessionEpoch;

      this.loading = true;

      if (!keepError) {
        this.clearError();
      }

      try {
        const drafts = await builderApi.listDrafts();

        if (epoch === sessionEpoch) {
          this.drafts = drafts;
          this.learnFromList(drafts);
        }
      } catch (error) {
        if (epoch === sessionEpoch) {
          if (classifyError(error) === 'forbidden') {
            this.drafts = emptyLists();
          }
          if (!keepError) {
            this.setError(this.describeError(error, 'list drafts'));
          }
        }
      } finally {
        if (epoch === sessionEpoch) {
          this.loading = false;
        }
      }

      return this.drafts;
    },

    // Whether the user may share: any draft of theirs listed says.
    learnFromList(drafts) {
      const [own] = drafts?.mine || [];

      if (own) {
        this.shareCapable = own.canShare === true;
      }
    },

    // A draft just made is shareable when the user may share any draft,
    // which the lists say once they hold one of the user's own. While the
    // user has none, the lists are read again, now that they have one, and
    // the open draft takes what they say.
    async learnShareCapable() {
      const { owner, draftId } = this;
      const mine = await this.listMine();

      if (this.owner === owner && this.draftId === draftId && this.isOwner) {
        this.canShare =
          mine.find((draft) => draft.id === draftId)?.canShare === true;
      }
    },

    /**
     * Reads who a draft is shared with, for the Share dialog. Only its
     * owner may.
     *
     * @param {{owner: string, id: string}} target
     * @returns {Promise<object>} see getShares in api.js
     */
    loadShares(target) {
      return builderApi.getShares(target.owner, target.id);
    },

    /**
     * Saves who a draft is shared with, for the Share dialog, which reports
     * a failure itself. The change gives the draft a new ETag. For the open
     * draft, nothing is sent meanwhile, and the queue takes the new ETag
     * only while the draft's content is as it last confirmed (see
     * adoptIfSameHead in autosave.js). A listed draft takes the draft the
     * server answered with, so its Delete carries the new ETag.
     *
     * @param {{owner: string, id: string}} target
     * @param {{user: string, access: string}[]} shares the whole new list
     * @param {string} sharesEtag the list's tag, as read
     * @returns {Promise<object>} see updateShares in api.js
     */
    async saveShares(target, shares, sharesEtag) {
      const autosave =
        target.owner === this.owner && target.id === this.draftId
          ? this.autosave
          : null;
      const release = autosave?.hold();

      try {
        await autosave?.idle();

        const saved = await builderApi.updateShares(
          target.owner,
          target.id,
          shares,
          sharesEtag,
        );

        if (saved.draft) {
          if (autosave && autosave === this.autosave) {
            this.rememberAccess(saved.draft);

            if (await autosave.adoptIfSameHead(saved.draft, saved.etag)) {
              this.etag = saved.etag;
            }
          }

          this.patchListedDraft(saved.draft);
        }

        return saved;
      } finally {
        if (release) {
          this.trackQueue(release());
        }
      }
    },

    /**
     * Shares several of the user's listed drafts with `people`, at
     * `access`, a few drafts at a time. For each draft the share list is
     * read, the people are added to it (see mergeShareList: no one is
     * removed, and someone listed already gets the access chosen) and the
     * whole list is saved; a list that changed meanwhile (412) is read and
     * merged again, once. A draft that would be shared with more people
     * than the server allows is not sent. Every draft is tried, unless the
     * session ends or the server cannot be reached; what failed is returned
     * with why, and nothing is announced. Each draft shared takes its place
     * on the lists, as after saveShares.
     *
     * @param {{owner: string, id: string}[]} items listed drafts
     * @param {string[]} people the users to add
     * @param {string} access 'view' or 'edit'
     * @param {object} [options] onProgress(done, total): called as each
     *   draft ends
     * @returns {Promise<{done: object[], failures: Array<{item: object,
     *   reason: string}>}>}
     */
    async shareDrafts(items, people, access, { onProgress } = {}) {
      const share = async (item, retried = false) => {
        const current = await this.loadShares(item);
        const merged = mergeShareList(current.shares, people, access);

        if (merged.shares.length > current.maxShares) {
          throw new BulkError(
            `It would be shared with ${count(merged.shares.length, 'person', 'people')}. A draft can be shared with at most ${count(current.maxShares, 'person', 'people')}.`,
          );
        }

        if (!merged.changed) {
          return;
        }

        try {
          await this.saveShares(item, merged.shares, current.sharesEtag);
        } catch (error) {
          const refused = shareErrors(error);

          if (error?.response?.status === 412) {
            if (!retried) {
              await share(item, true);

              return;
            }

            throw new BulkError(
              'Who it is shared with changed while it was being shared. Try again.',
            );
          }

          if (refused.length) {
            throw new BulkError(
              refused
                .map((entry) => reasonMessage(entry.reason, entry.user))
                .join(' '),
            );
          }

          throw error;
        }
      };
      const results = await runBulk(items, (item) => share(item), {
        onProgress,
        stop: endsBulk,
      });

      return bulkOutcome(results, (error, item) =>
        classifyError(error) === 'missing'
          ? draftMissingReason(item)
          : bulkReason(error),
      );
    },

    // Replaces a draft of mine on the lists with the one the server sent.
    patchListedDraft(draft) {
      const index = this.drafts.mine.findIndex(
        (item) => item.id === draft?.id && item.owner === draft?.owner,
      );

      if (index < 0) {
        return;
      }

      const mine = [...this.drafts.mine];

      mine[index] = {
        ...draft,
        shares: Array.isArray(draft.shares) ? draft.shares : [],
      };
      this.drafts = { ...this.drafts, mine };
    },

    /**
     * Reads the users a draft's owner may share it with, for the Share
     * dialog, which reports a failure itself.
     *
     * @param {{owner: string, id: string}} target
     * @returns {Promise<{username: string, name: string}[]>}
     */
    loadShareCandidates(target) {
      return builderApi.listShareCandidates(target.owner, target.id);
    },

    /**
     * The phenix Topology config a document publishes as, for the Download
     * dialog, which reports a failure itself.
     *
     * @param {object} doc
     * @param {string} [name]
     * @returns {Promise<object>} see exportTopology in api.js
     */
    exportTopology(doc, name) {
      return builderApi.exportTopology(doc, name);
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
     * Reads a published diagram: the published document of that id, or,
     * for the handle of a topology whose diagram is read from the Builder
     * file it names (see fileHandle in api.js), the document that topology
     * references now. That is the file's, unless the topology was published
     * since the list was read: then it is a published document, with an id.
     *
     * @param {string} id a listed published diagram's id
     * @returns {Promise<object>} id, source ('store' or 'file') and
     *   document: the diagram as the server sent it, decoded; for a
     *   topology's, also target, path, digest and topologyDiffers
     */
    async readPublished(id) {
      const topology = fileTopology(id);

      if (!topology) {
        return {
          id,
          source: 'store',
          document: serverDocument(await builderApi.getDocument(id)),
        };
      }

      const read = await builderApi.getTopologyDocument(topology);
      const file = read.source === 'file';

      return {
        id: file ? id : read.id || id,
        source: file ? 'file' : 'store',
        target: topology,
        path: file ? read.path || '' : '',
        digest: file ? read.digest || '' : '',
        topologyDiffers: file && read.topologyDiffers === true,
        document: serverDocument(read.document),
      };
    },

    /**
     * Shows a published diagram read only. No draft is made: the diagram is
     * only looked at until the user chooses to edit it (editPublished), which
     * a role without the configs create permission cannot.
     *
     * @param {string} id a listed published diagram's id: a published
     *   document's, or the handle of a topology's Builder file
     * @returns {Promise<object|null>} the diagram, or null when it could not
     *   be read
     */
    async viewPublishedDocument(id) {
      const epoch = sessionEpoch;
      const topology = fileTopology(id);

      this.loading = true;
      this.clearError();

      try {
        const read = await this.readPublished(id);

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        const listed = this.documents.find((item) => item.id === id);
        const target = read.target || listed?.target || '';
        const name =
          metadataOf(read.document).name || listed?.name || target || id;
        const file = read.source === 'file';

        this.newDocument();
        this.setDocument(read.document, { announce: false });
        this.draftId = '';
        this.serverHistory = [];
        this.readOnly = true;
        this.published = {
          id: read.id,
          name,
          target,
          source: read.source,
          path: read.path || '',
          digest: read.digest || '',
          topologyDiffers: read.topologyDiffers === true,
          read: read.document,
        };
        this.announce(
          file
            ? `Opened the diagram of topology ${target}, read only.`
            : `Opened published diagram ${name}, read only.`,
        );

        return this.doc;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(this.describePublishedError(error, topology));
        }

        return null;
      } finally {
        this.loading = false;
      }
    },

    /**
     * Edits the published diagram shown read only (see openPublishedDocument).
     * The draft is made from the diagram as the server sent it, not as the
     * editor shows it (a switch renamed after its network, say): the server
     * stores a diagram sent back unchanged as it is, so opening one is not
     * an edit of it.
     *
     * @returns {Promise<object|null>} the draft's document, or null
     */
    editPublished() {
      if (!this.published) {
        return Promise.resolve(null);
      }

      return this.openPublishedDocument(this.published.id, {
        document: this.published.read || this.doc,
        digest: this.published.digest,
      });
    },

    /**
     * Opens a published diagram for editing in a draft: the draft that
     * published it, when it is mine or shared with me for editing, or else
     * the draft of mine made from it before (see draftForPublished), so
     * opening the same diagram again never piles up copies; otherwise a new
     * draft made from it. A draft made from a topology's Builder file is
     * one made from what the file holds now: once the file changes, a new
     * draft is made from it. The custom icons the file carries are added to
     * the server's icon library first, as an upload's are (see ingestIcons).
     *
     * @param {string} id a listed published diagram's id: a published
     *   document's, or the handle of a topology's Builder file
     * @param {object} [options] announcement: what to say when a new draft
     *   is made, publisher: when the draft of mine that published the
     *   diagram is opened, and resumed: when the draft of mine made from it
     *   is (defaults name the diagram); document: the diagram, when it was
     *   read already, and digest: the digest a Builder file's was read with
     * @returns {Promise<object|null>} the draft's document, or null
     */
    async openPublishedDocument(
      id,
      { announcement, resumed, publisher, document: known, digest = '' } = {},
    ) {
      const epoch = sessionEpoch;
      const topology = fileTopology(id);

      this.loading = true;
      this.clearError();

      try {
        let read = known
          ? {
              id,
              source: topology ? 'file' : 'store',
              target: topology,
              digest,
              document: known,
            }
          : null;

        // A file's draft is found by what the file holds, so it is read
        // first.
        if (topology && !read) {
          read = await this.readPublished(id);

          if (this.sessionEndedSince(epoch)) {
            return null;
          }
        }

        const token = publishedToken(read || { id, source: 'store' });
        // The lists as they are now: the drafts shared with me too.
        const mine = await this.listMine();

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        const listed = this.documents.find((item) => item.id === id);
        const found = draftForPublished(
          { mine, shared: this.drafts.shared },
          { id, draftId: listed?.draftId },
          token,
        );

        if (found) {
          const { draft: existing, how } = found;
          const opened = await this.loadDraft(existing.owner, existing.id);

          // Another user's draft has said whose it is (see loadDraft).
          if (opened && how !== 'shared') {
            const name = metadataOf(opened).name || existing.title || id;

            this.announce(
              how === 'publisher'
                ? publisher ||
                    `Opened the draft that published diagram ${name}.`
                : resumed ||
                    (topology
                      ? `Opened your draft of the diagram of topology ${topology}.`
                      : `Opened your draft of published diagram ${name}.`),
            );
          }

          return opened;
        }

        read ||= await this.readPublished(id);

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        // A Builder file carries copies of the custom icons it uses, as a
        // downloaded diagram does. They go to the server's icon library
        // first, as those of an uploaded file do (see ingestIcons), so the
        // draft keeps only the copies the library refused or holds with
        // other bytes, each with a warning.
        const ingested =
          read.source === 'file'
            ? await ingestIcons(read.document, iconLibrary)
            : { doc: read.document, warnings: [] };

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        // One message for the whole operation, so neither half is lost.
        const readName = metadataOf(read.document).name;
        const opened =
          announcement ||
          (topology
            ? `Opened the diagram of topology ${topology} as a new draft.`
            : `Opened published diagram ${readName || id} as a new draft.`);
        const created = await this.createDraft({
          document: ingested.doc,
          title: readName,
          sourceToken: token,
          announcement: [opened, ...ingested.warnings].join(' '),
        });

        // The warnings stay on the canvas until they are dismissed.
        if (created && ingested.warnings.length > 0) {
          this.notice = {
            text: ingested.warnings.join(' '),
            seq: this.announcementSeq,
          };
        }

        return created ? this.doc : null;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(this.describePublishedError(error, topology));
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
          this.learnFromList(drafts);
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

    /**
     * Reads a stored scenario's content, whose apps the Inspector lists: a
     * document names its scenarios and holds none. The content read before
     * stays until this read answers. A read that answers after a later one,
     * or after the session ended, is dropped.
     *
     * @param {string} name
     * @returns {Promise<void>}
     */
    async fetchScenario(name) {
      const epoch = sessionEpoch;
      const previous = this.storedScenarios[name];
      const read = (previous?.read ?? 0) + 1;

      this.storedScenarios[name] = {
        content: previous?.content ?? null,
        problem: '',
        loading: true,
        read,
      };

      let found;

      try {
        found = { content: await builderApi.getScenario(name), problem: '' };
      } catch (error) {
        found = { content: null, problem: classifyError(error) };
      }

      if (epoch === sessionEpoch && this.storedScenarios[name]?.read === read) {
        this.storedScenarios[name] = { ...found, loading: false, read };
      }
    },

    /**
     * Reads a stored scenario's content again, for Download (see
     * fetchScenario; the Inspector shows this reading too).
     *
     * @param {string} name
     * @returns {Promise<{content: object|null, problem: string}>} the
     *   content, or null and why it could not be read (see classifyError)
     */
    async readScenario(name) {
      await this.fetchScenario(name);

      const read = this.storedScenarios[name];

      return read?.content
        ? { content: read.content, problem: '' }
        : { content: null, problem: read?.problem || 'error' };
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
      this.historyEtag = null;

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
            'That snapshot is no longer in the draft history. Open Draft History again to see the current list.',
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
     * Deletes a snapshot from the open draft's history, for the History
     * dialog, which says how it went; the current snapshot (the cursor's)
     * cannot be deleted. As for a restore, the queue saves first, and sends
     * nothing meanwhile. The queue takes the new ETag when the delete was
     * sent with its own (see adoptAfter in autosave.js), and undo and redo
     * skip the snapshot from then on.
     *
     * A delete refused because the draft changed elsewhere (412), or
     * because the list was out of date (409, 404), reads the draft again,
     * so the dialog lists its history as it is now; the next try is sent
     * with that read's ETag.
     *
     * @param {string} snapshotId
     * @returns {Promise<{deleted: boolean, message: string}>} message says
     *   why it was not deleted
     */
    async deleteSnapshot(snapshotId) {
      const { autosave, owner, draftId } = this;

      if (!autosave || this.readOnly) {
        return { deleted: false, message: 'This draft is read only.' };
      }

      const saved = await this.saveNow();

      if (saved.status !== 'saved' || saved.pending > 0) {
        return {
          deleted: false,
          message:
            'Your latest changes are not saved yet, so history cannot be changed. Wait for the save to finish, or use Retry saving.',
        };
      }

      const epoch = sessionEpoch;
      const release = autosave.hold();
      // The draft is the same one, in the same session.
      const current = () =>
        !this.sessionEndedSince(epoch) &&
        this.autosave === autosave &&
        this.owner === owner &&
        this.draftId === draftId;

      try {
        await autosave.idle();

        const sent = this.historyEtag || this.etag;
        const envelope = await builderApi.deleteSnapshot(
          owner,
          draftId,
          snapshotId,
          sent,
        );

        if (!current()) {
          return { deleted: false, message: '' };
        }

        const adopted = await autosave.adoptAfter(
          sent,
          envelope.draft,
          envelope.etag,
        );

        this.historyEtag = adopted ? null : envelope.etag;
        this.rememberDraft(envelope.draft);
        this.serverHistory = this.serverHistory.filter(
          (entry) => entry.id !== snapshotId,
        );

        if (
          this.history.removeWhere(
            (entry) => entry.serverSnapshotId === snapshotId,
          ) > 0
        ) {
          this.historyChanged();
        }

        return { deleted: true, message: '' };
      } catch (error) {
        if (!current()) {
          return { deleted: false, message: '' };
        }

        const why = {
          409: 'That is the current snapshot, which cannot be deleted. The list now shows the latest history.',
          412: 'This draft changed on the server since its history was read. The list now shows the latest history. Try again.',
          404: 'That snapshot is no longer in the draft history. The list now shows the latest history.',
        }[error?.response?.status];

        if (!why) {
          return {
            deleted: false,
            message: this.describeError(error, 'delete the snapshot'),
          };
        }

        try {
          await this.rereadHistory(autosave);
        } catch (readError) {
          return {
            deleted: false,
            message: this.describeError(readError, 'read the draft history'),
          };
        }

        return { deleted: false, message: why };
      } finally {
        this.trackQueue(release());
      }
    },

    // Reads the open draft again for the History dialog, while `autosave`
    // is held: its history, and the ETag the next change to it is sent
    // with. The queue takes that ETag only when the draft's content is as
    // it last confirmed (see adoptIfSameHead in autosave.js).
    async rereadHistory(autosave) {
      const fresh = await builderApi.getDraft(this.owner, this.draftId);

      if (Array.isArray(fresh.history)) {
        this.serverHistory = fresh.history;
      }

      const adopted = await autosave.adoptIfSameHead(fresh.draft, fresh.etag);

      this.historyEtag = adopted ? null : fresh.etag;
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
      this.publishIssues = [];
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
      const { owner, draftId } = this;
      // Whether that draft is still the open one: its answer says nothing
      // of a draft opened since.
      const stillOpen = () => this.owner === owner && this.draftId === draftId;

      // Under way from here on, the save it starts with included, for
      // whoever waits for the draft to be free again.
      this.publishing = true;

      const state = await this.saveNow().catch((error) => {
        this.publishing = false;

        throw error;
      });

      if (state.status !== 'saved' || state.pending > 0) {
        this.publishing = false;
        this.setError(
          state.status === 'conflict'
            ? 'This draft changed elsewhere. Resolve the conflict before publishing.'
            : 'Your latest changes are not saved on the server yet, so there is nothing new to publish. Wait for the save to finish or retry it.',
        );
        this.announce(this.error);

        return null;
      }

      // Edits made while publishing stay queued until it ends, so no save
      // lands between the publish and the ETag it answers with, which would
      // then be stale and raise a conflict with the user's own publish.
      const release = autosave.hold();

      try {
        await autosave.idle();

        const sent = autosave.sent;
        const { result, etag } = await builderApi.publish(
          owner,
          draftId,
          intent,
          this.etag,
        );

        if (etag && autosave.sent === sent) {
          if (stillOpen()) {
            this.etag = etag;
          }

          await autosave.setETag(etag);
        }

        this.publishResult = result;

        if (stillOpen()) {
          this.rememberDraft(result.draft);

          // The draft the server answers with names the experiment this
          // publication, or an earlier one, made.
          if (result.draft) {
            this.experiment = result.draft.experiment || '';
          }
        }

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
        // A refusal may list what it refuses, each issue with the element
        // it is about, for the dialog's list of checks.
        if ([409, 422].includes(error?.response?.status)) {
          this.publishIssues = responseIssues(error.response.data);
        }

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

    /**
     * Asks the server to build a diagram from a topology or experiment
     * config, for Import. The diagram is checked here, and opened by
     * openImported once the user accepts it: until then the open draft,
     * its autosave, owner and ETag stay as they are, so an answer nobody
     * waits for any more changes nothing.
     *
     * @param {{kind?: string, name?: string, content?: string,
     *   includes?: 'keep'|'combine', copy?: boolean, newName?: string}} request
     *   kind and name of a stored config, or the text of a config file;
     *   includes 'combine' makes a topology's included nodes the diagram's
     *   own, copy makes the diagram a copy, and either gives it newName
     * @returns {Promise<{document: object, warnings: string[],
     *   source: object|null, detached?: true}|null>} source is the config
     *   that was read; detached: the diagram is a copy or combined, so it is
     *   linked to no config and its draft takes no source token of a stored
     *   one. null when the import failed, which `error` then says, with
     *   `errorField` 'name' or 'content' for a source the server refused
     *   and 'newName' for a new name it refused
     */
    async generate(request) {
      this.clearError();

      try {
        const result = await builderApi.generate(request);
        const document = serverDocument(result.document);

        // The server's answer does not tell a copy from a plain import.
        const detached =
          Boolean(request?.copy) || request?.includes === 'combine';

        return {
          document,
          warnings: result.warnings,
          source: result.source,
          ...(detached ? { detached: true } : {}),
        };
      } catch (error) {
        const kind = classifyError(error);
        const uploaded = typeof request?.content === 'string';

        // A new name the server refuses is about the field it was typed in.
        if (
          error?.response?.status === 422 &&
          /^new topology name\b/i.test(serverReason(error))
        ) {
          this.setError(
            this.describeError(error, 'import the diagram'),
            'newName',
          );

          return null;
        }

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

    /**
     * Opens a diagram generate() made, once the user has accepted it. An
     * import always starts a new draft: the open draft is detached first,
     * with its autosave queue, so a later edit never overwrites it. The
     * diagram is checked before that, as setDocument checks it, so one
     * that cannot be opened leaves the open draft, its autosave, owner and
     * ETag as they are. Nothing is announced: the import is, once its
     * draft exists (see ImportDialog).
     *
     * @param {{document: object}} result what generate() returned
     * @returns {object|null} the document as opened, or null when it could
     *   not be, which `error` then says
     */
    openImported(result) {
      const { document, error } = openableDocument(result.document);

      if (!document) {
        this.setError(error);

        return null;
      }

      this.newDocument({ name: metadataOf(document).name });

      return this.setDocument(document, {
        label: 'Imported diagram',
        announce: false,
      });
    },

    /**
     * Asks the server to convert a diagram of the legacy Builder, for
     * Upload: the diagram's XML, or a Topology config that holds one. The
     * document is checked here and loaded by the dialog, once the user has
     * seen the warnings: until then the open draft stays as it is.
     *
     * @param {{content: string, name?: string}} request the text of the
     *   chosen file, and the name of the document of a diagram that comes
     *   without a topology
     * @returns {Promise<{document: object, warnings: string[],
     *   source: object|null, sourceToken?: string}|null>} source as
     *   generate() gives it, null for a diagram without a topology, whose
     *   draft takes sourceToken; null when the conversion failed, which
     *   `error` then says, with `errorField` 'content' when the file was
     *   refused
     */
    async convertLegacy({ content, name = '' } = {}) {
      this.clearError();

      try {
        const result = await builderApi.convertLegacy({ content, name });
        const document = serverDocument(result.document);

        return {
          document,
          warnings: result.warnings,
          source: result.source,
          ...(result.source ? {} : { sourceToken: LEGACY_TOKEN }),
        };
      } catch (error) {
        const kind = classifyError(error);

        // The server says what is wrong with the file in a sentence written
        // to be shown, which names its root element or the topology in it.
        this.setError(
          this.describeFileError(error, 'convert the legacy diagram'),
          kind === 'invalid' || kind === 'too-large' ? 'content' : '',
        );

        return null;
      }
    },

    /**
     * Makes a new draft of the open diagram in which the nodes of its
     * included topologies are its own (see combineIncluded in model.js),
     * and opens it. The draft that was open is left as it is, and stays
     * listed. The new draft is linked to no config, so publishing it makes
     * a new topology; it is named after the diagram, with a name that no
     * stored topology has and no draft of the user's, read anew, is titled:
     * combining the same diagram again makes a draft of another name.
     *
     * @returns {Promise<object|null>} the new draft, or null when it could
     *   not be made, which `error` then says
     */
    async combineIncluded() {
      const mine = await this.listMine();
      const { name: current } = metadataOf(this.doc);
      const from = current || 'Untitled diagram';
      const included = this.summary.included;
      const name = uniqueName(`${configName(current) || 'diagram'}-combined`, [
        ...(this.sources.topologies || []).map((entry) =>
          typeof entry === 'string' ? entry : entry?.name,
        ),
        ...(mine || []).map((draft) => draft?.title),
      ]);
      const combined = combineIncluded(toRaw(this.doc), name);

      this.newDocument({ name });

      const document = this.setDocument(combined, {
        label: 'Combined diagram',
        announce: false,
      });

      if (!document) {
        return null;
      }

      // One message once the draft exists, so neither half is lost.
      return this.createDraft({
        document,
        title: name,
        sourceToken: '',
        announcement: describeCombined(
          included,
          name,
          from,
          combined.source.includeTopologies,
        ),
      });
    },

    describeError(error, action) {
      // The server answered, but the diagram it sent did not pass the
      // editor's check (see serverDocument). That is not a connection
      // problem, and only the decoder's reason, when it gave one, says what
      // is wrong.
      if (error instanceof DocumentError) {
        const reason = error.message.trim().replace(/\.$/, '');

        return `Could not ${action}. The server sent a diagram the editor cannot open${reason ? `: ${reason}` : ''}.`;
      }

      const kind = classifyError(error);
      const detail = errorMessage(kind, error);

      return `Could not ${action}. ${detail}`;
    },

    /**
     * Why a request about a topology's Builder file failed. The server says
     * what is wrong with the file in a sentence written to be shown (it is
     * outside the directory phenix reads, it does not exist, it changed
     * since it was opened), which is shown as it is. Any other failure is
     * described as usual.
     *
     * @param {object} error
     * @param {string} action what could not be done
     * @returns {string}
     */
    describeFileError(error, action) {
      const said = [404, 409, 413, 422].includes(error?.response?.status)
        ? serverSentence(error)
        : '';

      return said
        ? `Could not ${action}. ${said}`
        : this.describeError(error, action);
    },

    // Why a published diagram could not be opened: a topology's, read from
    // its Builder file, or a published document.
    describePublishedError(error, topology) {
      return topology
        ? this.describeFileError(
            error,
            `open the diagram of topology ${topology}`,
          )
        : this.describeError(error, 'open the published diagram');
    },

    // --- document editing -------------------------------------------------

    addNode(options) {
      const result = addNode(this.doc, options);
      // A shape by its figure: "Added circle".
      const kind =
        result.node.kind === 'shape'
          ? kindLabel(result.node).toLowerCase()
          : options.kind || 'device';

      this.commit(result.doc, `Added ${kind}`);
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

    /**
     * Resizes a node to the box a mouse resize left it in, which a drag of
     * its top or left side also moves (see resizedBox: never smaller than
     * the node can be, and a group around its members), as one undo step.
     *
     * @param {string} id
     * @param {{x: number, y: number, width: number, height: number}} box
     *   absolute
     * @returns {object|null} the history entry, or null when nothing changed
     */
    resizeNodeBox(id, box) {
      const node = findNode(this.doc, id);

      if (!node) {
        return null;
      }

      const { position, size } = resizedBox(this.doc, id, box, notesShown());
      const current = sizeOf(node);

      if (
        position.x === node.position.x &&
        position.y === node.position.y &&
        size.width === current.width &&
        size.height === current.height &&
        node.size
      ) {
        return null;
      }

      return this.commit(
        updateNode(this.doc, id, { position, size }),
        `Resized ${nodeLabel(node) || 'node'} to ${size.width} by ${size.height}`,
      );
    },

    /**
     * Sets the points of a line, relative to its position, as one undo step:
     * the end of a drag of one of its points.
     *
     * @param {string} id line node
     * @param {{x: number, y: number}[]} points
     * @param {string} [label] what the history and the announcement say
     * @returns {object|null} the history entry, or null when nothing changed
     */
    setLinePoints(id, points, label) {
      const node = findNode(this.doc, id);
      const next = setLinePoints(this.doc, id, points);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, label || `Moved a point of ${nodeLabel(node)}`);
    },

    /**
     * Moves one point of a line, as the arrow keys on its handle do.
     *
     * @param {string} id line node
     * @param {number} index
     * @param {{x: number, y: number}} point relative to the line's position
     * @returns {object|null} the history entry, or null
     */
    moveLinePoint(id, index, point) {
      const node = findNode(this.doc, id);
      const next = moveLinePoint(this.doc, id, index, point);

      if (next === this.doc) {
        return null;
      }

      return this.commit(
        next,
        `Moved ${linePointName(node, index)} to x ${Math.round(point.x)}, y ${Math.round(point.y)}`,
      );
    },

    /**
     * Adds a bend to a line in the segment that ends at point `index`.
     *
     * @param {string} id line node
     * @param {number} index
     * @param {{x: number, y: number}} [point] relative to the line's
     *   position; the segment's middle without one
     * @returns {object|null} the history entry, or null when the line takes
     *   no more points
     */
    insertLinePoint(id, index, point) {
      const node = findNode(this.doc, id);
      const next = insertLinePoint(this.doc, id, index, point);

      if (next === this.doc) {
        if (node?.kind === 'line') {
          this.announce(
            `${nodeLabel(node)} has the most points a line can have.`,
          );
        }

        return null;
      }

      return this.commit(next, `Added a bend to ${nodeLabel(node)}`);
    },

    /**
     * Removes a point of a line, never leaving it fewer than two.
     *
     * @param {string} id line node
     * @param {number} index
     * @returns {object|null} the history entry, or null when it has two
     */
    removeLinePoint(id, index) {
      const node = findNode(this.doc, id);
      const next = removeLinePoint(this.doc, id, index);

      if (next === this.doc) {
        if (node?.kind === 'line') {
          this.announce(`${nodeLabel(node)} keeps at least two points.`);
        }

        return null;
      }

      return this.commit(next, `Removed ${linePointName(node, index)}`);
    },

    /**
     * @param {string} id
     * @param {string|null} parentId
     * @param {object} [options] announce: false when the caller shows a
     *   refusal itself (editRefusal), so the store does not say it too
     * @returns {object|null} the history entry, or null when nothing moved:
     *   the node is there already or cannot go there, or the edit was
     *   refused
     */
    setParent(id, parentId, { announce = true } = {}) {
      const next = setParent(this.doc, id, parentId, notesShown());

      if (next === this.doc || (!announce && this.editRefusal)) {
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
     * @returns {object} {edge} once connected, else {error}: why the
     *   connection, or the edit (editRefusal), was refused
     */
    connect(connection, { announce = true } = {}) {
      const result = connect(this.doc, connection);
      // An edit commit would refuse is found first when the caller says
      // why itself, as commit would say it too.
      const error = result.error || (announce ? '' : this.editRefusal);

      if (error) {
        if (announce) {
          this.announce(error);
        }

        return { error };
      }

      // commit has said why it refused.
      if (!this.commit(result.doc, 'Connected nodes')) {
        return { error: this.editRefusal || 'Not connected.' };
      }

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
      const result = groupNodes(this.doc, this.selection.nodes, notesShown());

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
     * Lays the diagram out, as one commit, which the layout menu can then put
     * back (see restoreLayout). With an algorithm, the layout menu's choice,
     * that one runs; without one, the draft's layout, or the Settings
     * default for a draft with none (layoutToRun). The draft keeps the
     * layout that ran as its own in the same commit, so the layout menu
     * names what made the positions. A layout that changes nothing is not
     * an edit, as in moveNodes: no undo step, no snapshot, and no restore;
     * one not chosen then keeps no layout either, so an empty diagram stays
     * at Default. See layOut for a layout that finishes later.
     *
     * @param {object} [options] algorithm: a LAYOUT_ALGORITHMS id to run;
     *   the rest go to the algorithm
     * @returns {Promise<object|null>} the history entry, or null when
     *   nothing changed or the layout was not applied
     */
    async layout({ algorithm, ...options } = {}) {
      const chosen = layoutAlgorithm(algorithm) ? algorithm : '';
      const id = chosen || this.layoutToRun;
      const laid = await layOut(this, this.doc, id, options, 'layout');

      if (!laid) {
        return null;
      }

      const placed = withGeometry(this.doc, laid);
      const next =
        chosen || layoutChanges(this.doc, placed)
          ? withLayoutChoice(placed, id)
          : placed;
      const changes = layoutChanges(this.doc, next);
      const name = layoutAlgorithm(id).label;

      if (!changes) {
        this.announce('The diagram is already laid out.');

        return null;
      }

      const moved =
        Object.keys(changes.geometry).length > 0 ||
        Object.keys(changes.routes).length > 0;
      const entry = this.commit(
        next,
        moved ? `Applied ${name} layout` : `Chose ${name} layout`,
      );

      if (entry) {
        this.layoutRestore = { entryId: entry.id, ...changes };
      }

      return entry;
    },

    /**
     * Auto-group: puts the ungrouped devices and switches (the selected
     * ones, when nodes are selected) into new groups, by network or by name
     * (see grouping.js), then lays the diagram out with its layout, or the
     * Settings default (layoutToRun), so the groups do not overlap, all as
     * one commit that keeps that layout as the draft's, as layout does. The
     * selection stays as it was. Nothing to group is no edit, and says so.
     * The rule that needs a pattern is autoGroupByPattern's: asked for
     * here, it does nothing.
     *
     * @param {string} [strategy] a GROUPING_STRATEGIES id
     * @param {object} [options] for the layout
     * @returns {Promise<object[]|null>} the groups made, or null
     */
    async autoGroup(strategy = 'network', options = {}) {
      if (strategy === 'pattern') {
        return null;
      }

      const selected = this.selection.nodes.length > 0;
      const planned = this.readOnly
        ? []
        : planGroups(this.doc, strategy, { selection: this.selection.nodes });

      if (!this.readOnly && !this.layoutRunning && planned.length === 0) {
        this.announce(nothingToGroup(strategy, { selected }));

        return null;
      }

      return makeGroups(this, planned, groupingPhrase(strategy), options);
    },

    /**
     * Auto-group by name pattern: as autoGroup, with the nodes whose names
     * the pattern matches the same text of in each group (see
     * planPatternGroups). The pattern runs in a Web Worker, so this ends
     * later, and its dialog is open meanwhile: what keeps it from grouping
     * is returned as `problem` for the dialog to show, and nothing is
     * announced or changed then. A pattern that compiles is kept for the
     * next time the dialog opens, whatever it then matches.
     *
     * @param {string} pattern a regular expression, as typed
     * @param {object} [options] match and signal, for planPatternGroups; the
     *   rest go to the layout
     * @returns {Promise<{groups: object[]|null, problem: string,
     *   invalid: boolean}>} the groups made; or null with the problem, which
     *   is about the pattern when `invalid` is set; or null with no problem
     *   when the layout was not applied (see layOut) or `signal` aborted
     */
    async autoGroupByPattern(pattern, { match, signal, ...options } = {}) {
      const refused = (problem, invalid = false) => ({
        groups: null,
        problem,
        invalid,
      });
      const unusable = patternProblem(pattern);

      if (unusable) {
        return refused(unusable, true);
      }

      rememberGroupPattern(pattern);

      if (this.readOnly) {
        return refused('This draft is read-only.');
      }

      if (this.layoutRunning) {
        return refused(
          `${this.autoGrouping ? 'Auto-group' : 'Auto layout'} is still running.`,
        );
      }

      const { history } = this;
      const entryId = history.currentEntry().id;
      const selected = this.selection.nodes.length > 0;
      let plan;

      try {
        plan = await planPatternGroups(this.doc, pattern, {
          selection: this.selection.nodes,
          match,
          signal,
        });
      } catch (error) {
        // The dialog closed meanwhile: there is no one to tell.
        if (error?.name === 'AbortError') {
          return refused('');
        }

        if (error instanceof PatternError) {
          return refused(error.message, error.code !== 'worker');
        }

        console.error('Auto-group by name pattern failed.', error);

        return refused(new PatternError('worker').message);
      }

      if (signal?.aborted) {
        return refused('');
      }

      // An edit, an undo or another document while the pattern ran: the
      // plan is for what the diagram was.
      if (this.history !== history || history.currentEntry().id !== entryId) {
        this.announce(
          'The diagram changed during Auto-group, so no groups were made.',
        );

        return refused('');
      }

      if (plan.groups.length === 0) {
        return refused(
          nothingToGroupByPattern(plan.matched, { selected }),
          true,
        );
      }

      return {
        groups: await makeGroups(
          this,
          plan.groups,
          groupingPhrase('pattern'),
          options,
        ),
        problem: '',
        invalid: false,
      };
    },

    /**
     * Puts back what the last automatic layout changed, its layout choice
     * included, as one commit that undo reverses like any other. Only
     * offered while the diagram is still exactly what that layout left (see
     * canRestoreLayout).
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
        restoreLayoutChanges(this.doc, this.layoutRestore),
        'Restored previous layout',
      );
    },

    // --- device templates of the diagram -----------------------------------

    /**
     * Saves a device template in the open diagram, as one edit. The custom
     * icon it names is a name the icon library resolves.
     *
     * @param {{name: string, description?: string, device: object}} template
     * @returns {object|null} the template as the diagram holds it, with its
     *   id; null when the diagram did not take it
     */
    addTemplate(template) {
      const full = templatesFull(this.doc);

      if (full) {
        this.announce(`Not saved. ${full}`);

        return null;
      }

      const result = addTemplate(this.doc, template);

      return this.commit(result.doc, `Added template ${result.template.name}`)
        ? result.template
        : null;
    },

    /**
     * Changes a template of the open diagram, as one edit. Devices made from
     * it before stay as they are.
     *
     * @param {string} id
     * @param {{name?: string, description?: string, device?: object}} patch
     * @returns {object|null} the template as the diagram holds it now; null
     *   when the diagram has no such template, or did not take the change
     */
    updateTemplate(id, patch) {
      const next = updateTemplate(this.doc, id, patch);
      const updated = (next.templates || []).find(
        (template) => template.id === id,
      );

      if (next === this.doc || !updated) {
        return null;
      }

      return this.commit(next, `Updated template ${updated.name}`)
        ? updated
        : null;
    },

    /**
     * Deletes a template from the open diagram, as one edit, which Undo
     * brings back.
     *
     * @param {string} id
     * @returns {boolean} whether it was deleted
     */
    removeTemplate(id) {
      const template = (this.doc.templates || []).find(
        (entry) => entry.id === id,
      );

      if (!template) {
        return false;
      }

      return Boolean(
        this.commit(
          removeTemplate(this.doc, id),
          `Deleted template ${template.name}`,
        ),
      );
    },

    // --- the template library ----------------------------------------------

    /**
     * Reads the user's template library. It is read when the Builder
     * opens, with the drafts' lists, when a draft opens, and after every
     * change of it. A read that fails says why in `templates.error`, never
     * in the page alert, and keeps what an earlier read answered: the tab
     * and the palette say so where they show the library. A role that may
     * not read it is left with none. An answer that arrives after a later
     * read's, or after the session ended, is dropped.
     *
     * @returns {Promise<object>} the library, as the store keeps it
     */
    async fetchTemplates() {
      const epoch = sessionEpoch;
      const read = (libraryReads += 1);

      this.templates = { ...this.templates, status: 'loading' };

      try {
        const library = await builderApi.listTemplates();

        if (epoch === sessionEpoch && read > libraryReadKept) {
          libraryReadKept = read;
          this.templates = {
            // A later read is still under way.
            status: read === libraryReads ? 'ready' : 'loading',
            error: '',
            loaded: true,
            owner: library.owner,
            items: library.templates,
            collections: library.collections,
            canShare: library.canShare,
            canPublish: library.canPublish,
            damaged: library.damaged,
            limits: { ...LIBRARY_LIMITS, ...library.limits },
          };
        }
      } catch (error) {
        if (epoch === sessionEpoch && read === libraryReads) {
          this.templates = {
            ...(classifyError(error) === 'forbidden'
              ? emptyLibrary()
              : this.templates),
            status: 'failed',
            error: libraryErrorMessage(error),
          };
        }
      }

      return this.templates;
    },

    // The user the library belongs to, which every change of it names. The
    // library says who that is, so one that was not read yet is read first.
    async libraryOwner() {
      if (!this.templates.owner) {
        await this.fetchTemplates();
      }

      if (!this.templates.owner) {
        throw new LibraryError(
          this.templates.error || 'Your library could not be loaded.',
        );
      }

      return this.templates.owner;
    },

    /**
     * Why a change of the template library failed, for the dialog or the
     * page that asked for it.
     *
     * @param {object} error what the change rejected with
     * @param {string} action what could not be done: "save the template"
     * @returns {string}
     */
    describeLibraryError(error, action) {
      return `Could not ${action}. ${
        error instanceof LibraryError
          ? error.reason
          : libraryErrorMessage(error)
      }`;
    },

    /**
     * Adds templates to the user's library, each as a copy under an id the
     * server gives it, and reads the library again. It rejects with the
     * failure, which the caller reports (see describeLibraryError).
     *
     * @param {{name: string, description?: string, device: object}[]}
     *   templates
     * @param {object} [options] collection: {name, description?}, a new
     *   collection that holds exactly these templates
     * @returns {Promise<{created: object[], collection: object|null}>} the
     *   ids and tags the server gave
     */
    async createLibraryTemplates(templates, { collection = null } = {}) {
      const owner = await this.libraryOwner();
      const result = await libraryWrite(() =>
        builderApi.createTemplates(owner, {
          templates: templates.map(templateContent),
          collection,
        }),
      );

      await this.fetchTemplates();

      return result;
    },

    /**
     * Saves a copy of a template of the diagram to the user's library (see
     * createLibraryTemplates); the template stays in the diagram. A template
     * of a library carries no icon, so the copy of the custom icon it names
     * that the diagram carries goes to the icon library first, under its
     * name, by the rules of an upload (see ingestSavedTemplateIcons): the
     * saved template then shows it. It rejects with the failure to save,
     * which the caller reports (see describeLibraryError).
     *
     * @param {object} template a template of the diagram
     * @param {object} [options]
     * @param {object} [options.library] the icon library
     * @returns {Promise<string[]>} what became of an icon that could not be
     *   added to the icon library
     */
    async saveTemplateToLibrary(template, { library = iconLibrary } = {}) {
      const warnings = await ingestSavedTemplateIcons(
        savedTemplateIcons(template, this.doc?.icons),
        library,
      );

      await this.createLibraryTemplates([template]);

      return warnings;
    },

    /**
     * Imports a template file (see parseTemplateFile in templateFile.js)
     * into the user's library: its templates, each a copy under an id the
     * server gives it, as a new collection named as the file names it, or
     * with " (2)" and so on when one of the user's collections has that
     * name. The custom icons the file carries go to the icon library first,
     * by the rules of an upload (see ingestTemplateIcons). It rejects with
     * the failure, which the caller reports (see describeLibraryError).
     *
     * @param {object} file the file, as parseTemplateFile reads it
     * @param {object} [options]
     * @param {object} [options.library] the icon library
     * @returns {Promise<{collection: string, templates: number, warnings:
     *   string[]}>} the new collection's name, how many templates it holds,
     *   and what became of icons that could not be added
     */
    async importTemplateFile(file, { library = iconLibrary } = {}) {
      const full = importProblem(this.templates, file.templates.length);

      if (full) {
        throw new LibraryError(full);
      }

      const warnings = await ingestTemplateIcons(file.icons, library);
      const name = uniqueCollectionName(
        file.name,
        this.ownCollections.map((collection) => collection.name),
      );

      await this.createLibraryTemplates(file.templates, {
        collection: {
          name,
          ...(file.description ? { description: file.description } : {}),
        },
      });

      return { collection: name, templates: file.templates.length, warnings };
    },

    /**
     * Replaces a template of the user's library, and reads the library
     * again. It rejects with the failure: a template that changed since
     * `etag` was read is refused (412), and the caller decides what then.
     *
     * @param {string} id
     * @param {{name: string, description?: string, device: object}} content
     * @param {string} etag the template's tag, as listed
     * @returns {Promise<object>} the template as the server has it now
     */
    async updateLibraryTemplate(id, content, etag) {
      const owner = await this.libraryOwner();
      const saved = await libraryWrite(() =>
        builderApi.updateTemplate(
          owner,
          id,
          { description: '', ...templateContent(content) },
          etag,
        ),
      );

      await this.fetchTemplates();

      return saved;
    },

    /**
     * Adds a collection to the user's library, and reads the library
     * again. It rejects with the failure.
     *
     * @param {{name: string, description?: string, templateIds?: string[]}}
     *   content
     * @returns {Promise<object>} the collection, with its id
     */
    async createCollection({ name, description = '', templateIds = [] }) {
      const owner = await this.libraryOwner();
      const created = await libraryWrite(() =>
        builderApi.createTemplateCollection(owner, {
          name,
          description,
          templateIds,
        }),
      );

      await this.fetchTemplates();

      return created;
    },

    /**
     * Replaces a collection of the user's library: its name, its
     * description and the templates it holds. It rejects with the failure;
     * one that changed since `etag` was read is refused (412).
     *
     * @param {string} id
     * @param {{name: string, description?: string, templateIds: string[]}}
     *   content the whole collection
     * @param {string} etag the collection's tag, as listed
     * @returns {Promise<object>} the collection as the server has it now
     */
    async updateCollection(id, { name, description = '', templateIds }, etag) {
      const owner = await this.libraryOwner();
      const saved = await libraryWrite(() =>
        builderApi.updateTemplateCollection(
          owner,
          id,
          { name, description, templateIds },
          etag,
        ),
      );

      await this.fetchTemplates();

      return saved;
    },

    /**
     * Adds templates to a collection of the user's library, or takes some
     * out of it. The collection is sent whole, as it is listed, with the
     * change made. One that changed meanwhile (412: another tab, or a
     * template of it deleted) is read again, and the change made to what it
     * holds now, once. It rejects with the failure.
     *
     * @param {string} id the collection's id
     * @param {{add?: string[], remove?: string[]}} change template ids
     * @returns {Promise<boolean>} whether the collection changed; false
     *   when it already was as asked
     */
    async changeCollectionMembers(id, { add = [], remove = [] }) {
      const send = async (retried) => {
        const collection = this.templates.collections.find(
          (entry) => entry.id === id && entry.source === 'own',
        );

        if (!collection) {
          throw new LibraryError(
            'This collection is no longer in your library.',
          );
        }

        const before = collection.templateIds || [];
        const gone = new Set(remove);
        const templateIds = [
          ...before.filter((entry) => !gone.has(entry)),
          ...add.filter((entry) => !before.includes(entry) && !gone.has(entry)),
        ];

        if (
          templateIds.length === before.length &&
          templateIds.every((entry, index) => entry === before[index])
        ) {
          return false;
        }

        try {
          await this.updateCollection(
            id,
            { ...collection, templateIds },
            collection.etag,
          );
        } catch (error) {
          if (error?.response?.status !== 412 || retried) {
            throw error;
          }

          await this.fetchTemplates();

          return send(true);
        }

        return true;
      };

      return send(false);
    },

    /**
     * Deletes templates and collections of the user's library in one
     * request, and reads the library again. A deleted template leaves its
     * collections; a deleted collection leaves its templates. It rejects
     * with the failure.
     *
     * @param {{templates?: string[], collections?: string[]}} selection ids
     * @returns {Promise<{templates: number, collections: number}>} how many
     *   of each the server deleted
     */
    async deleteLibraryItems({ templates = [], collections = [] }) {
      const owner = await this.libraryOwner();
      const deleted = await libraryWrite(() =>
        builderApi.deleteTemplates(owner, { templates, collections }),
      );

      await this.fetchTemplates();

      return deleted;
    },

    /**
     * The users the templates and collections of the user's library may be
     * shared with. It rejects with the failure: a user without an account
     * of their own may share nothing (403).
     *
     * @returns {Promise<{username: string, name: string}[]>}
     */
    loadTemplateShareCandidates() {
      return builderApi.listTemplateShareCandidates();
    },

    /**
     * Adds people to templates and collections of the user's library, and
     * takes people off them, every item at once, then reads the library
     * again. Sharing a collection shares its templates. It rejects with the
     * failure: a person the server refuses (422, see shareErrors) changes
     * nothing.
     *
     * @param {{templates?: string[], collections?: string[]}} items ids
     * @param {{add?: string[], remove?: string[]}} people usernames
     * @returns {Promise<{failed: object[]}>} the items left as they were
     *   (see readLibraryResult in api.js)
     */
    async shareLibraryItems(
      { templates = [], collections = [] },
      { add = [], remove = [] },
    ) {
      const owner = await this.libraryOwner();
      const result = await libraryWrite(() =>
        builderApi.shareTemplates(owner, {
          templates,
          collections,
          add,
          remove,
        }),
      );

      await this.fetchTemplates();

      return result;
    },

    /**
     * Publishes templates and collections server-wide, for everyone who
     * can use the Builder, or takes them back, then reads the library
     * again. Publishing a collection publishes its templates. It rejects
     * with the failure.
     *
     * @param {{templates?: string[], collections?: string[]}} items ids
     * @param {boolean} serverWide
     * @param {string} [owner] whose library the items are: the user's own
     *   when not given. A role that may publish takes back another user's
     *   items too.
     * @returns {Promise<{failed: object[]}>} the items left as they were
     */
    async publishLibraryItems(
      { templates = [], collections = [] },
      serverWide,
      owner = '',
    ) {
      const library = owner || (await this.libraryOwner());
      const result = await libraryWrite(() =>
        builderApi.publishTemplates(library, {
          templates,
          collections,
          serverWide,
        }),
      );

      await this.fetchTemplates();

      return result;
    },

    setInfo(patch) {
      this.commit(setDocumentInfo(this.doc, patch), 'Renamed diagram');
    },

    /**
     * Replaces the notes of the diagram, in its metadata, in one undo step
     * (see setDiagramNotes in model.js): a blank note is dropped, so a note
     * cleared and left is removed. Notes that are those already take no
     * step, and neither do notes of which one cannot be written (too long,
     * or holding a control character; see diagramNoteProblem): the diagram
     * keeps the notes it had.
     *
     * @param {string[]} notes
     * @returns {object|null} the history entry, or null when nothing changed,
     *   a note cannot be written or the draft is read only
     */
    setDiagramNotes(notes) {
      const next = setDiagramNotes(this.doc, notes);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, 'Updated diagram notes');
    },

    /**
     * Draws the icons of the diagram's devices, switches and groups at a
     * size, in one undo step (see setIconSize in model.js); a node with a
     * size of its own keeps it. The size the diagram has already takes no
     * step.
     *
     * @param {string} size one of ICON_SIZES
     * @returns {object|null} the history entry, or null when nothing changed
     *   or the draft is read only
     */
    setIconSize(size) {
      const next = setIconSize(this.doc, size);

      if (next === this.doc) {
        return null;
      }

      return this.commit(
        next,
        `Changed the diagram's icon size to ${ICON_SIZE_TITLES[documentIconSize(next)]}`,
      );
    },

    setViewport(viewport) {
      this.doc = setViewport(this.doc, viewport);
    },

    setGrid(patch) {
      this.commit(setGrid(this.doc, patch), 'Changed grid');
    },

    /**
     * Replaces the Scenario configs the diagram lists, in one undo step (see
     * setScenarios in model.js). The list the diagram has already takes no
     * step.
     *
     * @param {string[]} names
     * @returns {object|null} the history entry, or null when nothing changed
     *   or the draft is read only
     */
    setScenarios(names) {
      const current = documentScenarios(this.doc);

      if (
        names.length === current.length &&
        names.every((name, index) => name === current[index])
      ) {
        return null;
      }

      return this.commit(
        setScenarios(this.doc, names),
        names.length ? 'Updated scenarios' : 'Removed scenarios',
      );
    },

    /**
     * Stores a Scenario config on the server for the Scenario dialog: a new
     * one (POST /configs), or with `replace` in place of the one of its name
     * (PUT /configs/Scenario/<name>). A replacement takes the config's spec
     * and keeps the stored config's annotations, which it reads first (GET
     * /configs/Scenario/<name>), with the config's own over them and no
     * topology of the stored topology annotation lost
     * (replacedScenarioConfig): a PUT replaces the annotations too, and
     * phenix creates an experiment from a scenario only while that
     * annotation names the experiment's topology. The server checks the
     * caller's `configs` permission and the config, and its refusal is
     * thrown. The scenarios offered are then read again, and the content
     * read before of a scenario of that name is dropped, so the Inspector
     * reads it again.
     *
     * @param {object} config apiVersion, kind Scenario, metadata, spec
     * @param {{replace?: boolean}} [options]
     * @returns {Promise<void>}
     */
    async saveScenarioConfig(config, { replace = false } = {}) {
      if (replace) {
        const stored = await builderApi.getConfig(
          'Scenario',
          config.metadata.name,
        );

        await builderApi.updateConfig(replacedScenarioConfig(stored, config));
      } else {
        await builderApi.createConfig(config);
      }

      delete this.storedScenarios[config.metadata.name];

      await this.fetchSources();
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

    /**
     * Pastes a copy of the selected nodes beside them, as Copy and Paste
     * would, from a copy made of the selection now: the clipboard is
     * neither read nor changed. A connection goes with its two nodes, so a
     * selection of connections alone has nothing to duplicate, and says so.
     *
     * @returns {string[]} the ids of the copies, which become the selection
     */
    duplicate() {
      const payload = copySelection(this.doc, this.selection);

      if (!payload.nodes.length) {
        this.announce(
          this.selection.edges.length
            ? DUPLICATE_NEEDS_NODES
            : 'Nothing is selected to duplicate.',
        );

        return [];
      }

      const result = pasteClipboard(this.doc, payload);

      this.commit(
        result.doc,
        `Duplicated ${count(result.nodeIds.length, 'node')}`,
      );
      this.selection = { nodes: result.nodeIds, edges: [] };

      return result.nodeIds;
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

    /**
     * Go to, from a list of checks: selects the node or connection the issue
     * is about (a network's first switch for a network, see issueTarget) and
     * asks for focus on the Inspector field it names, or on the element
     * itself. The editor brings the element into view and shows the
     * Inspector (see Builder.vue), and the Inspector focuses the field.
     *
     * @param {string|object} issue see toIssue in issues.js
     * @returns {boolean} whether the diagram has what the issue names
     */
    goToIssue(issue) {
      const target = issueTarget(this.doc, toIssue(issue));

      if (!target) {
        return false;
      }

      this.select({ [target.kind]: [target.id] });
      this.focusRequest = {
        ...target,
        token: (this.focusRequest?.token || 0) + 1,
      };

      return true;
    },

    /**
     * The Go to request with that token, for the Inspector to focus the
     * field it names (see goToIssue), once: taking it marks it taken. So an
     * Inspector that mounts after the request, as when Go to on the drafts
     * page opens the editor, still acts on it, and no Inspector acts on a
     * request twice.
     *
     * @param {number} token
     * @returns {{kind: string, id: string, field: string, token: number}|null}
     *   the request, or null when it names no field, has been taken, or a
     *   later request replaced it
     */
    takeFocusRequest(token) {
      const request = this.focusRequest;

      if (
        !token ||
        request?.token !== token ||
        !request.field ||
        request.taken
      ) {
        return null;
      }

      this.focusRequest = { ...request, taken: true };

      return request;
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
      const storage = pageStorage();
      const mm = pageMatchMedia();

      this.theme = readStoredTheme(storage);
      this.resolvedTheme = applyTheme(element, this.theme, mm);

      return this.resolvedTheme;
    },

    // The Settings dialog says nothing: its radio buttons speak for
    // themselves, and the live region would say it once the dialog closes.
    setTheme(theme, element, { announce = true } = {}) {
      const storage = pageStorage();
      const mm = pageMatchMedia();

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
      const mm = pageMatchMedia();

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
