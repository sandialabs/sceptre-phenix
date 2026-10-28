// Pinia store of the Builder editor: selection, undo history, autosave queue,
// drafts, libraries, scenarios, preflight and publish. Edits use src/builder.

import { defineStore } from 'pinia';
import { markRaw, toRaw } from 'vue';

import { usePhenixStore } from '@/store.js';
import { roleAllowed } from '@/utils/rbac.js';

import { assignAddresses, parseSubnet } from './addressing.js';
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
  serverCode,
  serverRefusal,
  serverSentence,
  sourceFileName,
} from './api.js';
import {
  appliedOperations,
  createAutosave,
  describeState,
  headOf,
  initialState,
  replayHistory,
  sameHead,
  saveAnnouncement,
  serverCopyOf,
  snapshotIdOf,
  stampOf,
} from './autosave.js';
import { BulkError, bulkOutcome, runBulk } from './bulk.js';
import {
  copySelection,
  DUPLICATE_NEEDS_NODES,
  pasteClipboard,
} from './clipboard.js';
import { DocumentError, parseDocument } from './decode.js';
import { diagramFile, downloadedDiagram } from './diagramFile.js';
import { newestFirst } from './draftMessages.js';
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
import {
  emptyLayerSets,
  layerSetActions,
  layerSetGetters,
} from './layerSetStore.js';
import { newId, uniqueName } from './ids.js';
import { copyName, duplicateDocument } from './duplicate.js';
import { issueFix } from './issueFixes.js';
import { historyAfter, newerSnapshots } from './newerHistory.js';
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
import {
  changesFrom,
  mergeDocuments,
  mergeNotes,
  possessive,
} from './merge.js';
import {
  FILE_TOKEN,
  LEGACY_TOKEN,
  PUBLISHED_TOKEN,
  PUBLISH_RETRY_ADVICE,
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
import { onBuilderSessionEnd } from './session.js';
import { builderSettings } from './settings.js';
import { pageStorage } from './storage.js';
import { builderTabs } from './tabs.js';
import { importProblem, uniqueCollectionName } from './templateFile.js';
import {
  paletteTemplateGroups,
  templateByKey,
  templateContent,
  templateLinks,
  templateSource,
  templateUnlinkedLabel,
  templatesFull,
  templatesUpdatedLabel,
  updatedCopy,
  withLinkedCopy,
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
  LAYER_KINDS,
  includedReason,
  insertLinePoint,
  kindLabel,
  layerIdOf,
  linePointName,
  metadataOf,
  moveLinePoint,
  moveNodes,
  namedSwitches,
  networkRefusal,
  nodeLabel,
  addLayer,
  layerLabel,
  layerName,
  matchedNodeLayer,
  nodeLayer,
  placeholderLabel,
  relabelLayer,
  removalKept,
  removalRefusal,
  removeElements,
  removeInterface,
  removeLayer,
  removeLinePoint,
  removeNetworks,
  removeTemplate,
  renumberLayer,
  resizedBox,
  resizeNode,
  savedStamp,
  setDiagramNotes,
  setGrid,
  setIconSize,
  setLayers,
  setLinePoints,
  setNodeLayer,
  setNodeLayers,
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

// What a change of one node's layer is called in the history: "Changed the
// layer of Device alpha to Control", "Removed the layer of Switch sw" or,
// for a device that is then at the layer its fields match, "Changed the
// layer of Device alpha to the matched layer Control".
function nodeLayerLabel(doc, nodeId) {
  const node = findNode(doc, nodeId);
  const name = `${kindLabel(node)} ${nodeLabel(node)}`;
  const id = nodeLayer(node);
  const matched = matchedNodeLayer(doc, node);

  if (id === null && matched) {
    return `Changed the layer of ${name} to the matched layer ${layerLabel(doc, matched.id)}`;
  }

  return id === null
    ? `Removed the layer of ${name}`
    : `Changed the layer of ${name} to ${layerLabel(doc, id)}`;
}

// What the Inspector says when it uses the bundled schema.
const BUNDLED_FIELDS =
  'The Inspector shows the built-in fields of the Builder instead. These fields may differ from what this server accepts.';

// The codes of the server's refusals of the new topology name of a copy or
// a combined import (see generate).
const NEW_NAME_CODES = new Set(['import.name.invalid', 'import.name.source']);

// The disk listing in progress (see fetchDisks).
let disksRequest = null;

// Removes what this device keeps for a deleted draft, for every user who
// edited it here. A record that cannot be removed stays, unread. It is read
// again only for the same draft.
async function forgetLocalDraft(id) {
  try {
    const local = createDraftStore();
    const records = await local.all();

    await Promise.all(
      records
        .filter((record) => record.draftId === id)
        .map((record) => local.remove(record.key)),
    );
  } catch {}
}

// The IDs of drafts with changes on this device that the server lacks, of any
// user and tab. Clean up keeps away from them (see cleanUpStorage).
async function unsentDraftIds() {
  const records = await createDraftStore()
    .all()
    .catch(() => []);

  return new Set(
    records
      .filter((record) => record.draftId && (record.queue?.length || 0) > 0)
      .map((record) => record.draftId),
  );
}

// Why a draft that changed again while its delete was sent a second time
// was not deleted.
const CHANGED_WHILE_DELETING =
  'It changed on the server during the delete. Try again.';

// What the page alert says when Clean up deleted the open draft. Its next
// save fails as for any draft deleted elsewhere.
const OPEN_DRAFT_CLEANED =
  'Clean up deleted this draft, which was published and unchanged. Its published topology stays. Select Download to keep a copy of the diagram.';

// Why a bulk action left out a listed draft that the server no longer
// finds.
const DRAFT_DELETED = 'Someone deleted it after Builder read the list.';

// Deletes a draft and what this device kept for it. On a 412 it retries once
// with the current ETag. Resolves with null, or with the failure and whether
// the draft changed again.
async function removeDraft(id, etag) {
  let retried = false;

  try {
    try {
      await builderApi.deleteDraft(id, etag);
    } catch (error) {
      if (error?.response?.status !== 412) {
        throw error;
      }

      const current =
        preconditionETag(error) || (await builderApi.getDraft(id)).etag;

      retried = true;
      await builderApi.deleteDraft(id, current);
    }

    await forgetLocalDraft(id);

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
  'Builder cannot read its diagram and did not download it. A newer version of phenix may have saved it.';

// Whether a failed download ends the run, as for the other batches. But a
// diagram that this version cannot read (a DocumentError, which carries no
// response) is about that item alone, because the server did answer.
function endsDownloads(error) {
  return !(error instanceof DocumentError) && endsBulk(error);
}

/**
 * Saves each item as a Builder JSON file, as Download saves the open diagram
 * (see downloadedDiagram in diagramFile.js). Stops only when the session ends or
 * the server cannot be reached.
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

// What a layout run is for, in the viewer's terms, and what is dropped
// when the diagram changes before the run ends.
const LAYOUT_TASKS = {
  layout: { name: 'Auto layout', dropped: 'Builder did not apply the layout' },
  group: { name: 'Auto-group', dropped: 'Builder made no groups' },
};

// Why commit refuses an edit: the draft is read only, or a conflict is
// being resolved (see refuseWhileResolving).
const READ_ONLY_REFUSAL = 'This draft is read only.';
const RESOLVING_REFUSAL =
  'Not changed: the conflict is not resolved yet. Edit again after you resolve it.';

// Lays `doc` out for `task` (a LAYOUT_TASKS key). ELK runs in a Web Worker,
// so layoutRunning refuses a second request. Resolves to the geometry, or to
// null when the layout cannot apply. An edit made meanwhile wins over it.
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
    // The selected nodes are where By layer starts (see layouts/tiers.js).
    laid = await runLayout(id, doc, {
      ...(store.selection.nodes.length
        ? { selected: [...store.selection.nodes] }
        : {}),
      ...options,
    });
  } catch (error) {
    // Stopped as the Builder closed or the session ended (see
    // stopLayoutEngine): there is no one to tell.
    if (error?.name === 'AbortError') {
      return null;
    }

    // A layout library's own words mean nothing to the viewer, so they go
    // to the console.
    const known = error instanceof LayoutError;

    if (!known) {
      console.error(`${name} failed.`, error);
    }

    store.setError(
      `${name} failed. ${known ? error.message : 'Builder could not compute the layout.'}`,
    );

    return null;
  } finally {
    store.layoutRunning = false;
    store.autoGrouping = false;
  }

  // Another edit, an undo or another document since the layout started.
  // Pan and zoom change no entry.
  if (store.history !== history || history.currentEntry().id !== entryId) {
    store.announce(`The diagram changed during ${name}. ${dropped}.`);

    return null;
  }

  return laid;
}

// Whether a connection gives its interface an address (see connect in
// model.js): the Assign addresses on connect setting.
function connectOptions() {
  return { assign: builderSettings.assignAddressesOnConnect };
}

// How the model places a node it moves or adds (see freeSpot): on the grid
// while the user's Snap to grid setting is on.
function placing() {
  return { snap: builderSettings.snapToGrid };
}

// Makes the planned groups, lays them out so they do not overlap, and
// commits both as one edit. Resolves to the groups, or null (see layOut).
async function makeGroups(store, planned, phrase, options) {
  const grouped = applyGroups(store.doc, planned);
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
// after the user logged out is dropped and not shown to the next user.
let sessionEpoch = 0;

// Bumped on every read of a draft's snapshots, so only the last requested
// read populates the History dialog (see fetchHistory).
let historyRequest = 0;
// The same for the reads of the notice of the newer snapshots (see
// readNewerHistory).
let newerRequest = 0;

// Bumped on every preview of a publication, so only the last requested
// preview is answered (see previewPublish).
let previewRequest = 0;

// Bumped by every merge of a conflict (see mergeConflict), so only the last
// merge allows edits again. A save of a merged diagram can meet another
// conflict, whose merge starts before the first merge ends.
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

// Why a published topology the server no longer lists was not deleted.
const PUBLISHED_GONE =
  'Someone deleted it or published it again after Builder read the list.';

// Why a published topology that changed after the list was read (412) was
// not deleted.
const PUBLISHED_CHANGED =
  'It changed after Builder read the list. The list now shows it as it is. Check it, and then delete it again.';

// Why a deleted published topology failed, for a bulk outcome.
function publishedReason(error) {
  switch (error?.response?.status) {
    case 404:
      return PUBLISHED_GONE;
    case 412:
      return PUBLISHED_CHANGED;
    default:
      return bulkReason(error);
  }
}

// The lists as they are before they are read.
function emptyLists() {
  return { all: [], published: [], damaged: [] };
}

// What the template library holds at most (the limits of api/builder), used
// until the server says.
const LIBRARY_LIMITS = Object.freeze({
  templates: 200,
  collections: 50,
  nameBytes: 128,
  descriptionBytes: 1024,
  deviceBytes: 16384,
});

// The template library as it is before it is read.
function emptyLibrary() {
  return {
    status: 'idle',
    error: '',
    loaded: false,
    items: [],
    collections: [],
    builtins: [],
    damaged: false,
    limits: { ...LIBRARY_LIMITS },
  };
}

// The requested reads of the template library, and the last read whose
// answer was kept. An answer older than the kept answer is dropped (see
// fetchTemplates).
let libraryReads = 0;
let libraryReadKept = 0;

// The server changes the library with one compare-and-swap of its record,
// which it tries a few times. When the record continues to change during
// these tries (for example, from other users' changes), it answers 503.
// The change is then sent again, a moment later, this many times.
const LIBRARY_RETRIES = 2;
export const LIBRARY_RETRY_MS = 1000;

// How many of the messages that dialogs said the store keeps (see
// noteDialogSaid). The live region holds only a few announcements.
const DIALOG_SAID_KEPT = 50;

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

// Why a delete of library items was not made: one of them changed after
// the library was read (412).
const LIBRARY_CHANGED =
  'Someone changed the library after Builder read the list. The list now shows the library as it is. Check it, and then delete again.';

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

// Checks a diagram the server sent (see parseDocument). Any error that the
// check throws becomes a DocumentError. The server answered, so the failure
// is about the diagram, and is never mistaken for a request that did not
// arrive (see describeError).
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

// The document that setDocument opens from a payload, decoded strictly. An
// unsupported or invalid payload is refused, not silently repaired. Gives
// {document}, or {error}, the reason it cannot be opened.
function openableDocument(payload) {
  try {
    // A switch is named after its network, even when a document saved or
    // imported with another name still carries another label.
    return { document: namedSwitches(parseDocument(payload)) };
  } catch (error) {
    return {
      error:
        error instanceof DocumentError
          ? error.message
          : 'Builder could not read the document.',
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
              : 'Builder could not read the merged diagram.',
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
export function signedIn() {
  const mode = import.meta.env.VITE_AUTH;

  return Boolean(mode) && mode !== 'disabled';
}

// Why merging a conflict is not available (see mergeConflict).
const MERGE_NO_BASE =
  'Merging is not available: the server no longer keeps the version that your changes started from, or Builder could not read it.';
const MERGE_SLOW_BASE =
  'Merging is not available: the server took too long to send the version your changes started from.';
const MERGE_NO_THEIRS =
  'Merging is not available: Builder could not read the version on the server.';

// What the save state says, and the live region once, while a conflict's
// merge runs (see mergeConflict).
const MERGING_TEXT = 'Merging your changes with the server version…';

// How long a merge waits for the server to send the version the unsaved
// changes started from (see mergeBase). The conflict panel stays hidden
// while it waits, so the wait has a limit. After it, merging is not
// available, and the panel offers the other choices.
export const MERGE_BASE_TIMEOUT_MS = 15000;

// What withTimeout resolves to when the time ran out first.
const TIMED_OUT = Symbol('timed out');

// Settles when `promise` settles, or resolves to TIMED_OUT when `ms` pass
// first.
function withTimeout(promise, ms) {
  let timer;
  const expired = new Promise((resolve) => {
    timer = setTimeout(() => resolve(TIMED_OUT), ms);
  });

  return Promise.race([promise, expired]).finally(() => clearTimeout(timer));
}

// Why a merge that threw is not available, in words. The message is the
// error's own. Nothing of the diagram or the request is added to it, and
// nothing is logged.
function mergeFailure(error) {
  return `Merging failed: ${failureMessage(error)}`;
}

// Why the merge the user reviewed was not saved, in words, for the merge
// dialog's alert (see saveMergeChoices). As in mergeFailure, the message is
// the error's own.
export function mergeSaveFailure(error) {
  return `Builder could not save the merged diagram: ${failureMessage(error).replace(/\.$/, '')}.`;
}

function failureMessage(error) {
  return error instanceof Error && error.message
    ? error.message
    : 'unknown error';
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

// The drafts made from a published diagram, by their source token,
// the draft changed last first. A Builder file's token names what the file
// held, so a draft made before the file changed is not one of them.
function draftsFrom(drafts, token) {
  return newestFirst(
    (drafts || []).filter((draft) => token && draft.sourceToken === token),
  );
}

/**
 * The draft a published diagram is edited in: its publisher first, then a
 * draft made from it, or null. The publisher is found by its last
 * publication, so a new publish (a new id) does not lose it.
 */
export function draftForPublished(drafts, published, token) {
  const id = published?.id && !fileTopology(published.id) ? published.id : '';
  const publishers = newestFirst(
    (drafts || []).filter(
      (draft) => id && draft?.publication?.documentId === id,
    ),
  );
  const publisher =
    publishers.find((draft) => draft.id === published.draftId) || publishers[0];

  if (publisher) {
    return { draft: publisher, how: 'publisher' };
  }

  const [opened] = draftsFrom(drafts, token);

  return opened ? { draft: opened, how: 'opened' } : null;
}

// The title of a draft saved from local history: the diagram's name with
// " (local copy)". A name near the server's limit on names is cut first,
// without a split character, so the title still fits the limit.
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

// The moves of the nodes `ids` of `doc` by a delta.
function nudgedNodes(doc, ids, dx, dy) {
  return ids
    .map((id) => findNode(doc, id))
    .filter(Boolean)
    .map((node) => ({
      id: node.id,
      position: { x: node.position.x + dx, y: node.position.y + dy },
    }));
}

// Where a delta moves nodes: "right by 10", "left by 20 and down by 10".
function nudgeDirection(dx, dy) {
  const parts = [
    dx && `${dx > 0 ? 'right' : 'left'} by ${Math.abs(dx)}`,
    dy && `${dy > 0 ? 'down' : 'up'} by ${Math.abs(dy)}`,
  ].filter(Boolean);

  return parts.join(' and ');
}

export const useBuilderStore = defineStore('builder', {
  state: () => ({
    doc: createDocument(),
    history: markRaw(new History(createDocument(), DEFAULT_HISTORY_LIMIT)),
    // The History object is markRaw (its snapshots are not made reactive),
    // so nothing observes its index. Every action that moves the index bumps
    // this, and the canUndo/canRedo getters read it.
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
    // entry that layout made. It can be restored only while that entry is
    // current. Any other edit, an undo or redo, or another document drops
    // it, so a restore never moves nodes to stale positions.
    layoutRestore: null,
    // Whether an automatic layout is in progress (see layout), and whether
    // it is Auto-group's (see autoGroup).
    layoutRunning: false,
    autoGrouping: false,
    schema: builderSchemaV1,
    schemaError: '',
    // Who made the open draft, as the server recorded it.
    createdBy: '',
    draftId: '',
    // Goes up each time a diagram is opened (not when a fork renames the
    // draft), so the canvas starts fresh for each diagram.
    openedSeq: 0,
    etag: null,
    // What Publish needs about the draft record (see draftCanUpdate in
    // publish.js), with the time for Download and the source file name.
    draftRecord: {
      id: '',
      sourceToken: '',
      sourceFile: '',
      digest: '',
      publication: null,
      forked: null,
      updated: '',
    },
    // The experiment that a publication of the open draft made, by name,
    // while it still exists and the user may get it. This is as the server
    // last said: when the draft was read or made, and when it published. ''
    // for none.
    experiment: '',
    readOnly: false,
    // The published diagram shown read only, with no draft yet (see
    // viewPublishedDocument), or null. `read` is the diagram as the server
    // sent it, which an edit sends back unchanged.
    published: null,
    announcement: '',
    // Messages in the same slot replace each other while they wait to be
    // spoken ('save' for the save state, 'newer' for the notice of the
    // newer snapshots, '' for everything else).
    announcementSlot: '',
    // Bumped on every announcement, so a repeated message is announced
    // again.
    announcementSeq: 0,
    // What modal dialogs said in their own status regions, the latest
    // first, each with the announcementSeq at that time (see noteDialogSaid).
    dialogSaid: [],
    // Bumped on every save-state announcement, so Save now and Retry saving
    // know whether their outcome was spoken already.
    saveAnnouncementSeq: 0,
    // A refused canvas gesture, shown on the canvas until it is dismissed.
    notice: null,
    // A held arrow key's move or resize of nodes, which shows at once and is
    // committed when the key is released (see holdNudge), or null.
    keyGesture: null,
    theme: DEFAULT_THEME,
    resolvedTheme: 'light',
    drafts: emptyLists(),
    // The server's template library (see fetchTemplates). status goes from
    // 'idle' to 'loading' to 'ready' or 'failed'. A failed read keeps what an
    // earlier read answered.
    templates: emptyLibrary(),
    // The server's layer set library and its default set (see
    // fetchLayerSets in layerSetStore.js).
    layerSets: emptyLayerSets(),
    publishing: false,
    // The issues the server listed when it refused the last publish (see
    // responseIssues in issues.js), which the Publish dialog lists.
    publishIssues: [],
    // Go to, from a list of checks (see goToIssue): the node or connection
    // selected for it, the Inspector field to focus ('' for none), a
    // number that changes with every request, and `taken` after the
    // Inspector acted on it (see takeFocusRequest).
    focusRequest: null,
    // Whether resolveConflict or a merge is in progress (see
    // refuseWhileResolving).
    resolvingConflict: false,
    // The merge of a conflict's server version with the unsaved changes (see
    // mergeConflict), or null. status is 'merging', 'review' or
    // 'unavailable'.
    merge: null,
    // The configs ("<kind>/<name>") the server refused to let this draft
    // update because someone else changed them after it published them (see
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
    // headOf() the open draft as the server last answered for it: its
    // current snapshot, its cursor and how many snapshots it keeps. null
    // when no draft is open.
    draftHead: null,
    // The newest snapshot that Save newer history as a new draft copied:
    // {draftId, snapshotId, name}, the name of the new draft. null before.
    newerSaved: null,
    // Whether the History dialog is reading serverHistory from the server,
    // and why that failed, if it did (see fetchHistory).
    historyLoading: false,
    historyError: '',
    // The ETag of the history the History dialog shows, when it is newer
    // than the queue's ETag. It is read again after a delete met a change
    // made elsewhere (see deleteSnapshot). null otherwise.
    historyEtag: null,
    loading: false,
    error: '',
    // The dialog form field the error is about, which the dialog marks, or ''
    // (see publishRefusal).
    errorField: '',
    // Bumped whenever an error is set, so the same failure twice is shown
    // (and announced) twice.
    errorSeq: 0,
  }),

  getters: {
    ...layerSetGetters,
    canUndo: (state) => state.historyVersion >= 0 && state.history.canUndo(),
    canRedo: (state) => state.historyVersion >= 0 && state.history.canRedo(),
    // The layout that made the diagram's positions, which the draft keeps
    // (see ownLayout), or '' for none: the toolbar's Default.
    currentLayout: (state) => ownLayout(state.doc),
    // The layout a layout run uses: the draft's own, or the viewer's
    // default (see documentLayout).
    layoutToRun: (state) =>
      documentLayout(state.doc, builderSettings.layoutAlgorithm),
    // Whether the layout menu offers to restore the previous layout.
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
    // Whether a conflict's merge, which may end the conflict without a
    // question to the user, is in progress (see mergeConflict). Edits are
    // then refused, the conflict panel waits, and the save state says the
    // changes are being merged.
    mergingConflict: (state) =>
      state.saveState.status === 'conflict' &&
      state.merge?.status === 'merging',
    // Whether the conflict panel shows: a conflict, unless a merge that may
    // end it without a question is in progress (see mergeConflict).
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
    saveStateText: (state) =>
      state.saveState.status === 'conflict' && state.merge?.status === 'merging'
        ? MERGING_TEXT
        : describeState(state.saveState),
    // Whether the signed-in role includes the Builder (builderAllowed in
    // web/builder.go). The server decides. This only hides what it would
    // refuse.
    canUseBuilder: () => roleAllowed('builder', 'use'),
    // Publishing writes phenix configs, which the role must also allow.
    canPublish: () => roleAllowed('configs', 'update'),
    // The experiment the open diagram's publication made: a draft's own, or
    // that of the published diagram shown read only, as its listed row says.
    experimentName: (state) =>
      state.published
        ? state.documents.find((row) => row.id === state.published.id)
            ?.experiment || ''
        : state.experiment,
    // The drafts the server can no longer read.
    damagedDrafts: (state) => state.drafts.damaged || [],
    // The newer snapshots that the next change removes, while the editor
    // shows a snapshot behind the newest (see newerSnapshots in
    // newerHistory.js), with the name of the draft the newest was saved as
    // (saved, or ''). null otherwise.
    newerHistory: (state) => {
      if (
        state.historyVersion < 0 ||
        !state.autosave ||
        state.readOnly ||
        state.published
      ) {
        return null;
      }

      const newer = newerSnapshots(
        state.draftHead,
        state.serverHistory,
        state.history.currentEntry().serverSnapshotId,
      );
      const saved = state.newerSaved;

      return (
        newer && {
          ...newer,
          saved:
            saved?.draftId === state.draftId &&
            saved.snapshotId === newer.newest?.id
              ? saved.name
              : '',
        }
      );
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
    // the open diagram, then those of the library and of the server's
    // template files, then the built-in templates (see templates.js).
    paletteTemplateGroups: (state) =>
      paletteTemplateGroups(state.doc, state.templates),
    // The template a palette entry's key names ("diagram:<id>",
    // "library:<id>", "preloaded:<id>", "builtin:<id>"), or undefined.
    templateByKey: (state) => (key) =>
      templateByKey(state.doc, key, state.templates),
    // The templates and collections of the library, in the order it keeps
    // them: the order they were added in. Without the read-only ones.
    libraryTemplates: (state) =>
      state.templates.items.filter((template) => template.source === 'library'),
    libraryCollections: (state) =>
      state.templates.collections.filter(
        (collection) => collection.source === 'library',
      ),
    // The templates of the open diagram that are copies of a template of
    // the library, each with how it stands against it (see templateLinks
    // in templates.js). They are checked again when the diagram or the
    // library changes: when a draft opens, and when the library is read.
    templateLinks: (state) => templateLinks(state.doc, state.templates),
    // The copies that need attention: those the library has a later
    // version of, and those whose library template was deleted. The
    // Inspector's diagram panel lists them.
    templateLinkNotices: (state) =>
      state.templateLinks.filter((link) =>
        ['update', 'deleted'].includes(link.state),
      ),
    // The copies that an update changes (see updateTemplateCopies).
    templateUpdates: (state) =>
      state.templateLinks.filter((link) => link.state === 'update'),
  },

  actions: {
    ...layerSetActions,
    /**
     * @param {string} message
     * @param {object} [options] slot: see announcementSlot
     */
    announce(message, { slot = '' } = {}) {
      this.announcement = message;
      this.announcementSlot = slot;
      this.announcementSeq += 1;
    },

    /**
     * Records text a modal dialog said in its own status region, so the page's
     * live region does not say it again (see unsaidPart in announce.js).
     */
    noteDialogSaid(text) {
      if (!text) {
        return;
      }

      this.dialogSaid = [
        { text, seq: this.announcementSeq },
        ...this.dialogSaid,
      ].slice(0, DIALOG_SAID_KEPT);
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

      this.draftHead = headOf({ draft });
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

    // Shows why a canvas gesture did nothing, and announces it.
    refuse(message) {
      this.announce(message);
      this.notice = { text: message, seq: this.announcementSeq };
    },

    dismissNotice() {
      this.notice = null;
    },

    /**
     * Records one semantic edit: local history, then the autosave queue. Each
     * call becomes one server snapshot. This is the one place that drops icon
     * copies the nodes do not need (see settleIcons).
     */
    commit(doc, label) {
      // A held key's move or resize goes first, as its own undo step. `doc`
      // was made from the diagram it shows.
      this.endKeyGesture();

      if (this.readOnly) {
        // The page alert reads it. An announcement too would say it twice.
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

    // While a conflict is being resolved, an edit or an undo would be lost.
    // A save of the history as a new draft saves it as it was when the
    // resolution started, and a discard loads the server's version. So the
    // edit is refused and announced.
    refuseWhileResolving() {
      if (!this.resolvingConflict) {
        return false;
      }

      this.announce(RESOLVING_REFUSAL);

      return true;
    },

    // While the queue is blocked, an edit is kept only here. Its
    // announcement says so, and does not sound saved.
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

    // Handles a queue promise so it is not fire and forget. saveNow awaits the
    // last one.
    trackQueue(promise) {
      this.queueWork = markRaw(
        Promise.resolve(promise).catch((error) => {
          this.setError(this.describeError(error, 'save your changes'));

          return this.saveState;
        }),
      );

      return this.queueWork;
    },

    initAutosave({ draftId, etag, serverHead }) {
      const phenix = usePhenixStore();

      this.draftId = draftId;

      if (etag) {
        this.etag = etag;
      }

      this.autosave?.dispose();
      // The previous draft's queue continues in the background (see
      // createBackgroundSaves in leave.js). This draft does not wait for it.
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
          // Someone else saved the draft first: their version is merged
          // with the changes queued here.
          onConflict: (server) => this.mergeConflict(server),
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

            const snapshotId = snapshotIdOf(envelope);
            const position =
              operation?.kind === 'snapshot'
                ? this.history.entries.findIndex(
                    (item) => item.id === operation.commitId,
                  )
                : -1;
            const entry = this.history.entries[position];

            // A save and a cursor move answer with only the draft. The
            // history is then changed here as the server changed it, until
            // History reads it again.
            this.serverHistory = Array.isArray(envelope.history)
              ? envelope.history
              : historyAfter(
                  this.serverHistory,
                  headOf(envelope),
                  operation?.kind === 'snapshot'
                    ? {
                        id: snapshotId,
                        createdAt: envelope.draft?.updated || '',
                        createdBy:
                          stampOf(envelope)?.updatedBy ||
                          envelope.draft?.lastModifiedBy ||
                          '',
                        summary: operation.label || entry?.label || '',
                      }
                    : null,
                );

            if (operation?.kind === 'snapshot') {
              if (entry && snapshotId) {
                entry.serverSnapshotId = snapshotId;
              }

              this.stampEntry(entry, stampOf(envelope));

              // The server keeps fewer snapshots than this history when they
              // are large (MaxDraftHistoryBytes). The snapshot just saved is
              // its newest. Undo stops at the oldest snapshot it keeps,
              // because a move to a dropped snapshot would be refused.
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

      // Entries and queue are left out so attach keeps this device's log.
      return this.autosave.attach({ draftId, etag, serverHead });
    },

    /**
     * Copies a save's stamp (see withStamp in model.js) into the history entry
     * it stored, so an undo shows what the server holds. The diagram takes the
     * stamp too while that entry is shown.
     */
    stampEntry(entry, stamp) {
      if (!entry || !stamp) {
        return;
      }

      const shown = entry === this.history.currentEntry();
      const same = shown && toRaw(this.doc) === toRaw(entry.snapshot);
      const gesture = this.keyGesture;
      const held = shown && gesture && this.keyGestureShown(gesture);

      entry.snapshot = withStamp(entry.snapshot, stamp);

      if (shown) {
        this.doc = same ? entry.snapshot : withStamp(this.doc, stamp);
      }

      // A held key's move continues on the stamped diagram.
      if (held) {
        gesture.base = withStamp(gesture.base, stamp);
        gesture.shown = toRaw(this.doc);
      }
    },

    // Announces a new save problem or the recovery from one (see
    // saveAnnouncement). Returns whether anything was announced.
    announceSaveState(state) {
      const message = saveAnnouncement(this.saveAnnounced, state);

      // The panel announces a conflict, and the chosen action announces its
      // resolution. The save that follows is not a recovery to announce.
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
     * Says how an explicit save ended, unless that was announced already. A
     * `note` names what the save left out, and then says "Saved." only.
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

    /**
     * Opens a new, empty document that is no draft yet (see createDraft), with
     * a linked copy of the server's default layer set unless init names layers.
     */
    newDocument(init = {}) {
      this.autosave?.dispose();
      this.autosave = null;
      this.queueWork = null;

      const doc = init.layers
        ? createDocument(init)
        : this.withDefaultLayerSet(createDocument(init));

      this.doc = doc;
      this.history = markRaw(new History(doc, DEFAULT_HISTORY_LIMIT));
      this.historyChanged();
      this.selection = emptySelection();
      this.openedSeq += 1;
      this.createdBy = '';
      this.draftId = doc.metadata.id;
      this.etag = null;
      this.readOnly = false;
      this.published = null;
      this.rememberDraft({});
      this.experiment = '';
      this.saveState = initialState();
      this.saveAnnounced = null;
      this.merge = null;

      return doc;
    },

    /**
     * Replaces the document. The payload is decoded strictly. An invalid one is
     * rejected with a message and is not repaired. Returns null on reject.
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

    // replaceOpen closes the open draft only after the server made the new
    // one.
    async createDraft({
      document,
      title,
      sourceToken,
      sourceFile,
      announcement = 'Draft created.',
      replaceOpen = false,
    } = {}) {
      const phenix = usePhenixStore();
      const doc = document || this.doc;
      const epoch = sessionEpoch;

      this.clearError();

      try {
        const envelope = await builderApi.createDraft({
          title: title || metadataOf(doc).name || 'Untitled diagram',
          sourceToken,
          // Left out of the request when there is no file name, or no name
          // that the server would record.
          sourceFile: sourceFileName(sourceFile) || undefined,
          document: doc,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (replaceOpen) {
          this.newDocument({ name: metadataOf(doc).name });
        }

        this.createdBy = envelope.draft?.createdBy || phenix.username || '';
        this.draftId = envelope.draft?.id || metadataOf(doc).id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = false;
        this.published = null;
        this.rememberDraft(envelope.draft);
        this.experiment = envelope.draft?.experiment || '';
        // The server stored the document with the stamp it answers with,
        // and the editor's copy takes that stamp. Its switches are named
        // after their networks, as for a draft read from the server (see
        // setDocument). The document sent may be a published one, sent as
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
     * Reads a draft from the server and opens it. `quiet` skips the open
     * announcement. Changes recovered from this device are still announced.
     */
    async loadDraft(id, { quiet = false, envelope: read = null } = {}) {
      const epoch = sessionEpoch;

      this.loading = true;
      this.clearError();

      try {
        const envelope = read || (await builderApi.getDraft(id));

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        this.createdBy = envelope.draft?.createdBy || '';
        this.draftId = envelope.draft?.id || id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = false;
        this.published = null;
        this.rememberDraft(envelope.draft);
        this.experiment = envelope.draft?.experiment || '';

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

        return this.doc;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(this.describeError(error, 'load the draft'));

          // A draft that someone deleted is removed from the lists, so its
          // card does not offer the same failure again.
          if (classifyError(error) === 'missing') {
            await this.fetchDrafts({ keepError: true });
          }
        }

        return null;
      } finally {
        this.loading = false;
      }
    },

    /**
     * Restores the pending queue and undo history a previous session on this
     * device left (see replayHistory). It skips operations the server holds,
     * and merges a queue based on an older ETag (see mergeConflict).
     */
    async recoverLocalHistory(
      serverETag = this.etag,
      serverHead = null,
      server = null,
    ) {
      if (!this.autosave) {
        return false;
      }

      const local = await this.autosave.recover(this.draftId);
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
          draftId: this.draftId,
          etag: serverETag,
          serverHead,
          entries: [],
          cursor: 0,
          queue: [],
        });

        return false;
      }

      // An entry that the server holds is the snapshot it lists. The entry
      // takes that snapshot's id, and the stamp that the save wrote into it
      // (see savedStamp). The answer of that save never arrived.
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
      // holds its first operations, the rest continues from the snapshot
      // that the last of them stored. When that snapshot is still the
      // draft's newest and current one, the server's version is exactly
      // what the queue left.
      const lastApplied = applied.snapshotIds.get(
        queue[applied.count - 1]?.commitId,
      );
      const newest = this.serverHistory.at(-1);
      const etag =
        lastApplied && newest?.id === lastApplied && newest.current
          ? serverETag
          : local.etag;

      await this.autosave.attach({
        draftId: this.draftId,
        etag,
        // The head of the ETag that the queue continues from.
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
     * current. This is the draft history cursor. The editor has no presence
     * or live collaboration signal.
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
     *   now's key), even when nothing was waiting to be saved. note: what
     *   the save left out, said with the outcome (see announceSaveOutcome)
     */
    async saveNow({ announce = false, note = '' } = {}) {
      if (!this.autosave) {
        if (announce) {
          this.announce('Nothing to save: this diagram is not a draft yet.');
        }

        return this.saveState;
      }

      const before = this.saveAnnouncementSeq;

      // Anything already queued is awaited first, so callers (publish) see
      // the real end state and do not race a commit in progress.
      await this.queueWork;

      const state = await this.autosave.flush();

      if (announce) {
        this.announceSaveOutcome(state, before, note);
      }

      return state;
    },

    /** Sends the queue again. */
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

    // Resolves an ETag conflict. Local work never overwrites the server copy:
    // 'reload' loads the server copy, and 'fork' saves the history as a new
    // draft.
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
        // The server gives a draft the title of its document's name. So
        // every saved snapshot carries the new name, or else the fork would
        // look like the draft it leaves.
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

          this.createdBy = envelope.draft?.createdBy || this.createdBy;
          this.draftId = envelope.draft?.id || this.draftId;
          this.merge = null;
          this.rememberDraft(envelope.draft);
          this.experiment = envelope.draft?.experiment || '';
          this.readOnly = false;
          this.etag = envelope.etag;
          this.history.entries = entries;

          // Each entry now names its snapshot in the new draft, so undo and
          // redo move that draft's cursor, never the old draft's. Each entry
          // also holds the stamp that the new draft stored it with.
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
            .listSnapshots(this.draftId)
            .catch(() => []);
          this.announce(`Saved your local history as a new draft, ${title}.`);

          return envelope;
        } catch (error) {
          this.setError(this.describeError(error, 'save a new draft'));

          return null;
        }
      }

      const discarded = this.saveState.pending;
      const epoch = sessionEpoch;
      // Read first. A failed read keeps the local changes and their queue.
      const envelope = await builderApi
        .getDraft(this.draftId)
        .catch((error) => {
          if (!this.sessionEndedSince(epoch)) {
            this.setError(this.describeError(error, 'load the draft'));
          }

          return null;
        });

      if (!envelope || this.sessionEndedSince(epoch)) {
        return null;
      }

      await this.autosave.discardLocal();

      const loaded = await this.loadDraft(this.draftId, { envelope });

      if (loaded) {
        this.announce(
          `Discarded ${count(discarded, 'unsaved change')} and loaded the server version.`,
        );
      }

      return loaded;
    },

    /**
     * Merges the server's version with the unsaved changes (see merge.js). A
     * clean merge saves at once (see saveMerged), else status is 'review' or
     * 'unavailable'. Edits are refused while it runs, as the queue is replaced.
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

      // A merge made again when a newer version arrives is announced once,
      // by the first merge.
      const announced = this.merge?.status === 'merging';

      blocked('merging');
      this.resolvingConflict = true;

      if (!announced) {
        this.announce(MERGING_TEXT);
      }

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
          return await this.saveMerged(merged, checked, from, result);
        }

        blocked('review', '', {
          clashes: result.clashes.length,
          base,
          server: checked,
        });

        return null;
      } catch (error) {
        // For any failure, the merge ends. The conflict panel shows again,
        // with why, and still offers to save the history as a new draft and
        // to discard it. A later merge has the panel to itself.
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
     * The server snapshot the unsaved changes started from: from the history
     * or the queue, else read within MERGE_BASE_TIMEOUT_MS. doc is null, with
     * a reason, when it cannot be read.
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
          builderApi.getSnapshot(record.draftId, id),
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
     * Saves a merged document on top of the server's version (see rebase in
     * autosave.js). The history drops the unsaved entries, which lack the other
     * editor's changes. If rebase throws, the editor state is restored.
     */
    async saveMerged(merged, server, from, result = {}) {
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
      const before = {
        entries: this.history.entries,
        index: this.history.index,
        doc: this.doc,
        layoutRestore: this.layoutRestore,
        selection: this.selection,
        etag: this.etag,
        serverHistory: this.serverHistory,
      };

      this.history.restore(entries, 1);
      this.historyChanged();
      this.doc = doc;
      this.layoutRestore = null;
      this.selection = keptSelection(this.selection, doc);
      this.etag = server.etag;
      this.rememberDraft(server.draft);

      if (Array.isArray(server.history)) {
        this.serverHistory = server.history;
      }

      try {
        await autosave.rebase({
          etag: server.etag,
          head: server.head,
          entries,
        });
      } catch (error) {
        this.history.restore(before.entries, before.index);
        this.historyChanged();
        this.doc = before.doc;
        this.layoutRestore = before.layoutRestore;
        this.selection = before.selection;
        this.etag = before.etag;
        this.serverHistory = before.serverHistory;

        throw error;
      }

      // The queue holds the merge now and the conflict has ended. So the
      // merge dialog closes with the conflict panel already gone (see
      // mergeReview in Builder.vue), and an edit goes after the merge.
      this.merge = null;
      this.resolvingConflict = false;
      this.announce(
        [`Merged ${possessive(from)} changes with yours.`, mergeNotes(result)]
          .filter(Boolean)
          .join(' '),
      );
      this.trackQueue(autosave.flush());

      return doc;
    },

    // The mergeDocuments() result the merge dialog reviews, or null when no
    // review is offered.
    mergeReview() {
      const { merge } = this;

      if (merge?.status !== 'review') {
        return null;
      }

      return markRaw(
        mergeDocuments(merge.base, toRaw(this.doc), merge.server.document),
      );
    },

    // Why the strict check would refuse the merge with `choices`, as
    // "path: message" items (see parseDocument). Empty when it would save.
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
     * Saves the reviewed merge with the user's choices (see saveMerged). It
     * saves nothing when the strict check refuses it or another merge runs. A
     * failed save keeps the review, so the user can try again.
     */
    async saveMergeChoices(choices = {}) {
      const { merge, autosave } = this;

      if (merge?.status === 'merging' || this.resolvingConflict) {
        return { saved: false, busy: true, issues: [] };
      }

      if (merge?.status !== 'review' || !autosave) {
        return { saved: false, issues: [] };
      }

      const result = mergeDocuments(
        merge.base,
        toRaw(this.doc),
        merge.server.document,
        choices,
      );
      const checked = checkedMerge(result.doc);

      if (!checked.doc) {
        return { saved: false, issues: checked.issues };
      }

      const run = (mergeRuns += 1);

      this.resolvingConflict = true;

      try {
        await this.saveMerged(checked.doc, merge.server, merge.from, result);
      } catch (error) {
        return { saved: false, issues: [], error: mergeSaveFailure(error) };
      } finally {
        if (run === mergeRuns) {
          this.resolvingConflict = false;
        }
      }

      return { saved: true, issues: [] };
    },

    // Deletes a draft. A card's ETag can be stale, so a 412 retries once with
    // the current ETag (see removeDraft).
    async deleteDraft(id, etag, title = '') {
      this.clearError();

      const failure = await removeDraft(id, etag);

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
     * Deletes several drafts, a few at a time, as deleteDraft does. Failures
     * return with reasons and are not announced. A draft the server no longer
     * finds counts as deleted.
     */
    async deleteDrafts(items, { before, onProgress } = {}) {
      const results = await runBulk(
        items,
        async (item) => {
          await before?.(item);

          const failure = await removeDraft(item.id, item.etag);

          if (!failure) {
            return;
          }

          if (failure.changed) {
            throw new BulkError(CHANGED_WHILE_DELETING);
          }

          if (classifyError(failure.error) !== 'missing') {
            throw failure.error;
          }

          // The server no longer finds it: it was deleted elsewhere after
          // the list was read, which is what the user asked for. It counts
          // as deleted, and what this device kept for it is also removed.
          await forgetLocalDraft(item.id);
        },
        { onProgress, stop: endsBulk },
      );

      await this.fetchDrafts({ keepError: true });

      return bulkOutcome(results, bulkReason);
    },

    // A dry run of Clean up in the Builder settings (see cleanUpStorage in
    // api.js). The settings report a failure.
    planStorageCleanup() {
      return builderApi.cleanUpStorage({ dryRun: true });
    },

    /**
     * Clean up in the Builder settings: deletes published, unchanged drafts and
     * unused data. It saves the open draft first, and cleans nothing if that
     * save fails or a draft has unsent changes here (see unsentDraftIds).
     */
    async cleanUpStorage() {
      if (this.autosave) {
        const saved = await this.saveNow().catch(() => null);

        if (!saved || saved.status !== 'saved' || saved.pending > 0) {
          return null;
        }
      }

      const unsent = await unsentDraftIds();

      if (unsent.size > 0) {
        const plan = await builderApi.cleanUpStorage({ dryRun: true });
        const held = plan.drafts.filter((id) => unsent.has(id)).length;

        if (held > 0) {
          return { unsent: held };
        }
      }

      const result = await builderApi.cleanUpStorage();
      const open =
        Boolean(this.draftId) && result.drafts.includes(this.draftId);

      await Promise.all(result.drafts.map((id) => forgetLocalDraft(id)));

      if (open) {
        this.setError(OPEN_DRAFT_CLEANED);
      }

      await this.fetchDrafts({ keepError: true });

      return { ...result, open };
    },

    /**
     * Deletes the topology a published diagram is current for, and its
     * published diagrams. Drafts and experiments stay. The server refuses a
     * diagram that is no longer current, or a topology with a stale etag.
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
        const status = error?.response?.status;
        const changed = status === 404 || status === 412;

        this.setError(
          changed
            ? `Could not delete topology ${name}. ${publishedReason(error)}`
            : this.describeError(error, `delete topology ${name}`),
        );

        if (changed) {
          await this.fetchDocuments();
        }

        return false;
      }
    },

    // Deletes a published topology, and removes it from the lists. Its card
    // says so in the meantime. It rejects with the server's failure.
    async removePublished(item) {
      const name = item.target || item.id;

      this.deletingDocuments = [...this.deletingDocuments, item.id];

      try {
        await builderApi.deleteDocument(item.id, item.etag);

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
     * Deletes the topologies of several published diagrams, one at a time, as
     * deletePublished does. Failures return with reasons and are not
     * announced.
     */
    async deletePublishedMany(items, { onProgress } = {}) {
      const results = await runBulk(
        items,
        (item) => this.removePublished(item),
        { limit: 1, onProgress, stop: endsBulk },
      );

      await this.fetchDocuments();

      return bulkOutcome(results, publishedReason);
    },

    // Saves each listed draft's server version as a Builder JSON file, with its
    // custom icons. Failures return with reasons and are not announced.
    async downloadDrafts(items, { save, onProgress } = {}) {
      return downloadEach(
        items,
        async (item) =>
          serverDocument((await builderApi.getDraft(item.id)).document),
        { save, onProgress, missing: () => DRAFT_DELETED },
      );
    },

    // Saves each listed published diagram as a Builder JSON file, read as
    // readPublished reads it.
    async downloadPublishedMany(items, { save, onProgress } = {}) {
      return downloadEach(
        items,
        async (item) => (await this.readPublished(item.id)).document,
        { save, onProgress, missing: () => PUBLISHED_GONE },
      );
    },

    /**
     * Loads the server schema. On failure the editor uses the bundled copy and
     * tells the user, because form fields can differ from what the server
     * accepts.
     */
    async fetchSchema() {
      try {
        const payload = await builderApi.getSchema();

        const usable = isSchemaBundle(payload);

        this.schema = normalizeSchemaBundle(payload);
        this.schemaError = usable
          ? ''
          : `The server sent form fields this editor does not recognize. ${BUNDLED_FIELDS}`;
      } catch (error) {
        this.schema = builderSchemaV1;
        this.schemaError = `${this.describeError(error, "load this server's form fields")} ${BUNDLED_FIELDS}`;
      }

      return this.schema;
    },

    // A listing that answers after the session ended is dropped, because it
    // is the previous user's (see endSession). A listing that the role may
    // not read empties the lists. Any other failure keeps what they showed.
    // With keepError, the page alert continues to say what failed before
    // the lists were read.
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

    // The Topology config a document publishes as, for the Download dialog
    // (see exportTopology in api.js).
    exportTopology(doc, name) {
      return builderApi.exportTopology(doc, name);
    },

    // The package of a document, for the Download dialog (see buildPackage in
    // api.js).
    buildPackage(doc, include) {
      return builderApi.buildPackage(doc, include);
    },

    // What this server has of a package's needs, for the Upload dialog (see
    // resolvePackage in api.js).
    resolvePackage(pkg) {
      return builderApi.resolvePackage(pkg);
    },

    /**
     * Stores a packaged config as a new config (POST /configs) for the Upload
     * dialog. It drops the cached content of a scenario of that name, so the
     * Inspector reads it again.
     */
    async createPackagedConfig(config) {
      await builderApi.createConfig(config);

      if (config.kind === 'Scenario') {
        delete this.storedScenarios[config.metadata.name];
      }
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
     * Reads a published diagram by id. For a topology's Builder file handle
     * (see fileHandle in api.js), it reads the document the topology names
     * now, which is a published document if it was published since.
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

    // Shows a published diagram read only, with no draft, until the user
    // chooses to edit it (editPublished). Returns null when it is not read.
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
     * Edits the published diagram shown read only. The draft is made from the
     * diagram as the server sent it, not as the editor shows it, so to open a
     * diagram is not to edit it.
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
     * Opens a published diagram in a draft: its publisher, else a draft made
     * from it, else a new one (see draftForPublished). A changed Builder file
     * gets a new draft, and its icons go to the icon library (see ingestIcons).
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

        // A file's draft is found by what the file holds, so the file is
        // read first.
        if (topology && !read) {
          read = await this.readPublished(id);

          if (this.sessionEndedSince(epoch)) {
            return null;
          }
        }

        const token = publishedToken(read || { id, source: 'store' });
        // The lists as they are now.
        const drafts = await this.currentDrafts();

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        const listed = this.documents.find((item) => item.id === id);
        const found = draftForPublished(
          drafts,
          { id, draftId: listed?.draftId },
          token,
        );

        if (found) {
          const { draft: existing, how } = found;
          const opened = await this.loadDraft(existing.id, { quiet: true });

          if (opened) {
            const name = metadataOf(opened).name || existing.title || id;

            this.announce(
              how === 'publisher'
                ? publisher ||
                    `Opened the draft that published diagram ${name}.`
                : resumed ||
                    (topology
                      ? `Opened the draft of the diagram of topology ${topology}.`
                      : `Opened the draft of published diagram ${name}.`),
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
        // first, as those of an uploaded file do (see ingestIcons). So the
        // draft keeps only the copies that the library refused or holds with
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

    // The drafts as the server lists them now (another user or tab may
    // have made some), or as last listed when the server cannot say.
    async currentDrafts() {
      const epoch = sessionEpoch;

      try {
        const drafts = await builderApi.listDrafts();

        if (epoch === sessionEpoch) {
          this.drafts = drafts;
        }

        return drafts.all;
      } catch {
        return this.drafts.all;
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
     * Reads the server's disk images for the advisory drive checks. A refused,
     * failed or empty listing leaves the images unknown with no alert. With
     * minimega down the list is empty. Concurrent reads share one request.
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
     * Asks the server to run the named preflight checks on the open draft (see
     * preflight.js). The server checks the saved snapshot, so queued changes
     * save first, as Publish does. Nothing is written.
     */
    async runPreflight({ checks, experiment = '' }) {
      const { draftId } = this;

      if (!this.autosave || !draftId) {
        return {
          error:
            'Preflight checks run on a saved draft. Save this diagram as a draft first.',
        };
      }

      if (!this.readOnly && this.autosave) {
        const state = await this.saveNow().catch(() => null);

        if (!state || state.status !== 'saved' || state.pending > 0) {
          return {
            error:
              'Your latest changes are not saved on the server yet. The checks would not see them. Wait for the save to finish, or select Retry saving.',
          };
        }
      }

      try {
        return {
          report: await builderApi.preflight(draftId, {
            checks,
            experiment,
          }),
        };
      } catch (error) {
        return { error: this.describeError(error, 'run the preflight checks') };
      }
    },

    // Reads a stored scenario's content for the Inspector. A read that
    // answers after a later read or after the session ended is dropped.
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

    // Reads a stored scenario's content again, for Download (see
    // fetchScenario). Returns the content, or null and the problem.
    async readScenario(name) {
      await this.fetchScenario(name);

      const read = this.storedScenarios[name];

      return read?.content
        ? { content: read.content, problem: '' }
        : { content: null, problem: read?.problem || 'error' };
    },

    // Reads a whole Scenario config for the Scenarios dialog (GET
    // /configs/Scenario/<name>). The server's refusal is thrown.
    readScenarioConfig(name) {
      return builderApi.getConfig('Scenario', name);
    },

    // Reads the open draft's snapshots for the History dialog. The dialog
    // opens at once and shows historyLoading until they arrive, or
    // historyError if they do not. The page alert behind the dialog does not
    // change. A list that answers after another read started, another draft
    // opened or the session ended is dropped.
    async fetchHistory() {
      const { draftId } = this;
      if (!this.autosave || !draftId) {
        return [];
      }

      const epoch = sessionEpoch;
      const request = (historyRequest += 1);
      const latest = () => epoch === sessionEpoch && request === historyRequest;
      const current = () => latest() && this.draftId === draftId;

      this.clearError();
      this.historyLoading = true;
      this.historyError = '';
      this.historyEtag = null;

      try {
        const history = await builderApi.listSnapshots(draftId);

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

    // Restores a snapshot: the server's cursor moves to it, and the editor
    // shows it with a new undo history. Returns null when it fails.
    async restoreSnapshot(snapshotId, { label = 'Snapshot restored' } = {}) {
      this.clearError();

      try {
        if (!this.autosave) {
          this.setError('Open a saved draft before restoring history.');

          return null;
        }

        const saved = await this.saveNow();
        if (saved.status !== 'saved' || saved.pending > 0) {
          this.setError(
            'Your latest changes are not saved yet. You cannot restore history now. Wait for the save to finish, or select Retry saving.',
          );

          return null;
        }

        if (!this.serverHistory.some((entry) => entry.id === snapshotId)) {
          this.setError(
            'That snapshot is no longer in the draft history. Open Draft History again to see the current list.',
          );

          return null;
        }

        const envelope = await builderApi.getSnapshot(this.draftId, snapshotId);

        const cursorState = await this.trackQueue(
          this.autosave.moveCursor({ snapshotId }),
        );
        if (cursorState.status !== 'saved') {
          // The editor keeps the document on screen, so the server cursor
          // must stay with it.
          await this.autosave.withdrawCursor(snapshotId);
          this.setError(
            'Could not restore the snapshot: the server did not move the draft to it. Try again.',
          );

          return null;
        }

        // setDocument announces the label.
        const document = this.setDocument(envelope.document, {
          label,
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
     * Reads the open draft's snapshots again, silently, for the newer snapshots
     * notice (see newerHistory). It drops a stale answer or a failure, and
     * the notice then names no user.
     */
    async readNewerHistory() {
      const { autosave, draftId, draftHead } = this;

      // While the History dialog reads the list, its read keeps it.
      if (!autosave || !draftId || !draftHead || this.historyLoading) {
        return this.newerHistory?.newest || null;
      }

      const epoch = sessionEpoch;
      const request = (newerRequest += 1);

      try {
        const history = await builderApi.listSnapshots(draftId);

        if (
          epoch === sessionEpoch &&
          request === newerRequest &&
          this.autosave === autosave &&
          !this.historyLoading &&
          sameHead(this.draftHead, draftHead)
        ) {
          this.serverHistory = history;
        }
      } catch {
        // The notice says the count without the newest snapshot.
      }

      return this.newerHistory?.newest || null;
    },

    // The newest snapshot of the open draft, while the next change removes
    // newer snapshots: as the editor knows it, or else as the server lists
    // it now. null, with the page alert saying why, when it is not known.
    async newestSnapshot() {
      const newest =
        this.newerHistory?.newest || (await this.readNewerHistory());

      if (!newest && this.newerHistory) {
        this.setError(
          'Could not read the draft history. Open Draft History to see the newer snapshots.',
        );
      }

      return newest;
    },

    /**
     * Return to latest: shows the newest snapshot again. It goes there as redo
     * does when the undo history holds it, else it restores it (see
     * restoreSnapshot).
     */
    async returnToLatest() {
      if (!this.newerHistory || this.refuseWhileResolving()) {
        return false;
      }

      this.clearError();

      const newest = await this.newestSnapshot();

      if (!newest) {
        return false;
      }

      this.endKeyGesture();

      const { entries, index } = this.history;
      const at = entries.findIndex(
        (entry, place) => place > index && entry.serverSnapshotId === newest.id,
      );

      if (at < 0) {
        return (
          (await this.restoreSnapshot(newest.id, {
            label: 'Returned to the latest snapshot',
          })) !== null
        );
      }

      this.history.restore(entries, at);
      this.doc = this.history.current();
      this.layoutRestore = null;
      this.historyChanged();
      this.selection = emptySelection();
      this.announce(
        `Returned to the latest snapshot${this.unsavedNote() || '.'}`,
      );
      this.moveHistoryCursor();

      return true;
    },

    // Save newer history as a new draft: copies the newest snapshot into a new
    // draft (see duplicateCopy) and stays on the open draft.
    async saveNewerAsDraft() {
      if (!this.newerHistory) {
        return null;
      }

      this.clearError();

      const { draftId } = this;
      const epoch = sessionEpoch;
      const newest = await this.newestSnapshot();

      if (!newest) {
        return null;
      }

      try {
        const envelope = await builderApi.getSnapshot(draftId, newest.id);
        const made = await this.duplicateCopy(
          serverDocument(envelope.document),
          epoch,
        );

        if (!made) {
          return null;
        }

        const created = await builderApi.createDraft({
          title: made.name,
          sourceToken: '',
          document: made.copy,
        });

        if (this.sessionEndedSince(epoch)) {
          return null;
        }

        if (this.draftId === draftId) {
          this.newerSaved = {
            draftId,
            snapshotId: newest.id,
            name: made.name,
          };
        }

        this.announce(`Saved the latest snapshot as draft ${made.name}.`);

        return created.draft;
      } catch (error) {
        if (!this.sessionEndedSince(epoch)) {
          this.setError(
            this.describeError(error, 'save the newer history as a new draft'),
          );
        }

        return null;
      }
    },

    /**
     * Deletes a snapshot other than the current one. The queue saves first, as
     * for a restore, then takes the new ETag (see adoptAfter in autosave.js).
     * A 412, 409 or 404 reads the draft again for the next try.
     */
    async deleteSnapshot(snapshotId) {
      const { autosave, draftId } = this;

      if (!autosave || this.readOnly) {
        return { deleted: false, message: 'This draft is read only.' };
      }

      const saved = await this.saveNow();

      if (saved.status !== 'saved' || saved.pending > 0) {
        return {
          deleted: false,
          message:
            'Your latest changes are not saved yet. You cannot change history now. Wait for the save to finish, or select Retry saving.',
        };
      }

      const epoch = sessionEpoch;
      const release = autosave.hold();
      // The draft is the same one, in the same session.
      const current = () =>
        !this.sessionEndedSince(epoch) &&
        this.autosave === autosave &&
        this.draftId === draftId;

      try {
        await autosave.idle();

        const sent = this.historyEtag || this.etag;
        const envelope = await builderApi.deleteSnapshot(
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
          409: 'That is the current snapshot. You cannot delete it. The list now shows the latest history.',
          412: 'This draft changed on the server after Builder read its history. The list now shows the latest history. Try again.',
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
    // is held: its history, and the ETag that the next change to it is
    // sent with. The queue takes that ETag only when the draft's content is
    // as it last confirmed (see adoptIfSameHead in autosave.js).
    async rereadHistory(autosave) {
      const fresh = await builderApi.getDraft(this.draftId);

      if (Array.isArray(fresh.history)) {
        this.serverHistory = fresh.history;
      }

      const adopted = await autosave.adoptIfSameHead(fresh.draft, fresh.etag);

      this.historyEtag = adopted ? null : fresh.etag;
    },

    /**
     * Publishes the draft. Publish never carries document bytes. The server
     * reads the cursor's snapshot, so it must first confirm the queue. Unsent
     * work, a blocked queue or local errors block the publish.
     */
    async publish(intent) {
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

      if (!this.autosave || !this.draftId) {
        this.setError('Save this diagram as a draft before publishing it.');

        return null;
      }

      // The queue of the draft being published, even if another draft opens
      // in the meantime.
      const autosave = this.autosave;
      const { draftId } = this;
      // Whether that draft is still the open one. Its answer says nothing
      // about a draft opened since.
      const stillOpen = () => this.draftId === draftId;

      // In progress from here, including the save it starts with, for any
      // caller that waits for the draft to be free again.
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
            : 'Your latest changes are not saved on the server yet. There is nothing new to publish. Wait for the save to finish, or select Retry saving.',
        );
        this.announce(this.error);

        return null;
      }

      // Edits made during the publish stay queued until it ends. So no save
      // occurs between the publish and the ETag it answers with. Such a save
      // would make that ETag stale and cause a conflict with the user's own
      // publish.
      const release = autosave.hold();

      try {
        await autosave.idle();

        const sent = autosave.sent;
        const { result, etag } = await builderApi.publish(
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

        if (stillOpen()) {
          this.rememberDraft(result.draft);

          // The draft the server answers with names the experiment this
          // publication, or an earlier one, made.
          if (result.draft) {
            this.experiment = result.draft.experiment || '';
          }
        }

        // The configs just written now exist, so the next publish of the
        // same names must update them. The topology now points at the
        // diagram just published.
        if (result.ok || result.partial) {
          await Promise.all([this.fetchSources(), this.fetchDocuments()]);
        }

        if (result.ok) {
          this.clearError();
          this.announce('Diagram published.');
        } else {
          const partial = result.retry
            ? `Publish finished with failures. It wrote some configs. ${PUBLISH_RETRY_ADVICE}`
            : 'Publish finished with failures. It wrote some configs.';

          this.setError(
            result.partial ? partial : 'Publish failed. It wrote no configs.',
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
          // A 409 is about a config the publish would write. It can mean the
          // config list is stale, so the lists are read again first.
          await Promise.all([this.fetchSources(), this.fetchDocuments()]);

          const refusal = publishRefusal(serverRefusal(error), intent, {
            topologies: this.sources.topologies,
            experiments: this.sources.experiments,
            draft: this.publishDraft,
          });

          // A config that someone else changed after this draft published it
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
          // The refusal's code names a target that the server refuses, such
          // as an experiment name it reserves. The dialog marks its field.
          this.setError(
            this.describeError(error, 'publish the diagram'),
            publishRefusal(serverRefusal(error), intent).field,
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
     * Asks the server for a dry run of the publish. A later preview or another
     * open draft makes an answer stale, and it is dropped (null). A failure is
     * returned, not thrown. The preview never blocks publishing.
     */
    async previewPublish(intent) {
      previewRequest += 1;

      const request = previewRequest;
      const { draftId } = this;
      const current = () =>
        request === previewRequest && this.draftId === draftId;

      if (!this.autosave || !draftId) {
        return {
          failed: true,
          message:
            'Save this diagram as a draft to see what publishing changes.',
        };
      }

      try {
        const preview = await builderApi.previewPublish(draftId, intent);

        return current() ? preview : null;
      } catch (error) {
        return current()
          ? {
              failed: true,
              message: this.describeError(
                error,
                'read what publishing changes',
              ),
            }
          : null;
      }
    },

    /**
     * Asks the server to build a diagram from a topology or experiment config,
     * for Import. openImported opens it after the user accepts it. Returns null
     * on failure, which `error` and `errorField` then say.
     */
    async generate(request) {
      this.clearError();

      try {
        const result = await builderApi.generate(request);
        const document = serverDocument(result.document);

        // The server's answer does not show whether it is a copy or a plain
        // import.
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

        // A new name that the server refuses is about the field it was typed
        // in: a name that is not a config name, or the imported topology's
        // own name.
        if (NEW_NAME_CODES.has(serverCode(error))) {
          this.setError(
            this.describeError(error, 'import the diagram'),
            'newName',
          );

          return null;
        }

        // A stored config removed after the list was read. The list is read
        // again, so the config is no longer offered, and the user picks
        // another.
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
     * Opens an accepted generate() result as a new draft. It checks the diagram
     * first, as setDocument does, then detaches the open draft and its queue.
     * A diagram that cannot open keeps the open draft as it is.
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
     * Asks the server to convert a legacy Builder diagram (XML, or a Topology
     * config that holds one), for Upload. The open draft stays as it is.
     * Returns null when it fails, which `error` then says.
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
     * Makes and opens a new draft in which the included topologies' nodes are
     * its own (see combineIncluded in model.js). It links to no config and
     * gets a name that no topology or draft has.
     */
    async combineIncluded() {
      const drafts = await this.currentDrafts();
      const { name: current } = metadataOf(this.doc);
      const from = current || 'Untitled diagram';
      const included = this.summary.included;
      const name = uniqueName(`${configName(current) || 'diagram'}-combined`, [
        ...(this.sources.topologies || []).map((entry) =>
          typeof entry === 'string' ? entry : entry?.name,
        ),
        ...(drafts || []).map((draft) => draft?.title),
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

    /**
     * Duplicate draft: opens a new draft of a copy of a draft's diagram,
     * without its history (see duplicateDocument). The copy links to no config
     * or publication, and is named "<name> (copy)" (see copyName).
     */
    async duplicateDraft({ id, document = null }) {
      const epoch = sessionEpoch;
      let doc = document;

      this.clearError();

      if (!doc) {
        try {
          doc = serverDocument((await builderApi.getDraft(id)).document);
        } catch (error) {
          if (!this.sessionEndedSince(epoch)) {
            this.setError(this.describeError(error, 'read the draft to copy'));
          }

          return null;
        }
      }

      const made = await this.duplicateCopy(doc, epoch);

      if (!made) {
        return null;
      }

      // The open draft stays open until the server made the copy.
      return this.createDraft({
        document: made.copy,
        title: made.name,
        sourceToken: '',
        announcement: `Duplicated draft ${made.from} as ${made.name}.`,
        replaceOpen: true,
      });
    },

    // The copy a new draft is made of, with a name no draft has (see
    // copyName). Returns null when the session ended or the copy cannot open.
    async duplicateCopy(doc, epoch) {
      const drafts = await this.currentDrafts();

      if (this.sessionEndedSince(epoch)) {
        return null;
      }

      const from = metadataOf(doc).name || 'Untitled diagram';
      const name = copyName(
        from,
        (drafts || []).map((draft) => draft?.title || draft?.name),
      );
      const { document: copy, error } = openableDocument(
        duplicateDocument(toRaw(doc), name),
      );

      if (!copy) {
        this.setError(error);

        return null;
      }

      return { from, name, copy };
    },

    describeError(error, action) {
      // The server answered, but the diagram it sent did not pass the
      // editor's check (see serverDocument). That is not a connection
      // problem. Only the decoder's reason, when it gave one, says what is
      // wrong.
      if (error instanceof DocumentError) {
        const reason = error.message.trim().replace(/\.$/, '');

        return `Could not ${action}. The server sent a diagram the Builder cannot open${reason ? `: ${reason}` : ''}.`;
      }

      const kind = classifyError(error);
      const detail = errorMessage(kind, error);

      return `Could not ${action}. ${detail}`;
    },

    // Why a request about a topology's Builder file failed. The server writes
    // the sentence about the file to be shown, so it is shown as it is.
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
      // A device made from a template of the library also adds a copy of
      // the template, linked to it, in the same step (see withLinkedCopy).
      const linked = withLinkedCopy(result.doc, options.fromTemplate);

      const entry = this.commit(
        linked.doc,
        linked.template
          ? `Added ${kind} and template ${linked.template.name}`
          : `Added ${kind}`,
      );

      // commit has said why it refused.
      if (!entry) {
        return null;
      }

      // The device has no link to its template, so no update is offered
      // for it. Say so, not only "Added device".
      if (linked.full) {
        this.refuse(
          `Added ${kind}. ${linked.full} The device has no link to template ${options.fromTemplate.name}.`,
        );
      }

      this.selection = { nodes: [result.node.id], edges: [] };

      return result.node;
    },

    updateNode(id, patch, label = 'Updated node') {
      const reason = includedReason(findNode(this.doc, id));

      // The position of an included device belongs to the diagram. Its
      // other fields do not.
      if (reason && (patch.device || patch.label !== undefined)) {
        this.refuse(reason);

        return null;
      }

      return this.commit(updateNode(this.doc, id, patch, placing()), label);
    },

    /**
     * Moves nodes as one history commit, so a multi-node drag or nudge is one
     * undo step and one snapshot. A move that leaves every node in place is
     * not an edit, and is dropped.
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

    // Nudges the current selection by a delta, as one commit.
    nudgeSelection(dx, dy) {
      this.nudgeNodes(this.selection.nodes, dx, dy);
    },

    // Moves the nodes `ids` by a delta, as one commit, and says where.
    nudgeNodes(ids, dx, dy) {
      const moves = nudgedNodes(this.doc, ids, dx, dy);

      // Keyboard users cannot see where a node went, so say it.
      const [only] = moves;
      const label =
        moves.length === 1
          ? `Moved ${nodeLabel(findNode(this.doc, only.id)) || 'node'} to ` +
            `x ${only.position.x}, y ${only.position.y}`
          : `Moved ${moves.length} nodes ${nudgeDirection(dx, dy)}`;

      return this.moveNodes(moves, label);
    },

    /**
     * Shift and an arrow key, while held: moves the selection at once with no
     * undo step, snapshot or announcement. endKeyGesture then makes the whole
     * move one undo step.
     */
    holdNudge(dx, dy) {
      if (this.editRefusal) {
        this.nudgeSelection(dx, dy);

        return;
      }

      const gesture = this.keyGestureFor('nudge', this.selection.nodes);

      gesture.dx += dx;
      gesture.dy += dy;
      this.showKeyGesture(
        gesture,
        moveNodes(
          gesture.base,
          nudgedNodes(gesture.base, gesture.ids, gesture.dx, gesture.dy),
        ),
      );
    },

    // Alt, Shift and an arrow key, while held: resizes a node at once, as
    // holdNudge moves nodes. endKeyGesture makes it one undo step.
    holdResize(id, size) {
      if (this.editRefusal) {
        this.resizeNode(id, size);

        return;
      }

      const gesture = this.keyGestureFor('resize', [id]);

      gesture.size = size;
      this.showKeyGesture(gesture, resizeNode(gesture.base, id, size));
    },

    // The held key's gesture of `kind` on the nodes `ids`: the one in
    // progress, or a new one, after the one before it ends.
    keyGestureFor(kind, ids) {
      const gesture = this.keyGesture;

      if (
        gesture?.kind === kind &&
        gesture.ids.join('\n') === ids.join('\n') &&
        this.keyGestureShown(gesture)
      ) {
        return gesture;
      }

      this.endKeyGesture();
      this.keyGesture = markRaw({
        kind,
        ids: [...ids],
        base: toRaw(this.doc),
        entryId: this.history.currentEntry().id,
        shown: toRaw(this.doc),
        dx: 0,
        dy: 0,
        size: null,
      });

      return this.keyGesture;
    },

    showKeyGesture(gesture, doc) {
      this.doc = doc;
      gesture.shown = doc;
    },

    // Whether the diagram is still the one the gesture shows. Another
    // document, an undo or a restore replaces it.
    keyGestureShown(gesture) {
      return (
        toRaw(this.doc) === gesture.shown &&
        this.history.currentEntry().id === gesture.entryId
      );
    },

    /**
     * Ends the held key's move or resize as one undo step, and says once where
     * the nodes are. It records nothing if nothing changed or the diagram was
     * replaced.
     */
    endKeyGesture() {
      const gesture = this.keyGesture;

      if (!gesture) {
        return false;
      }

      this.keyGesture = null;

      if (!this.keyGestureShown(gesture)) {
        return false;
      }

      this.doc = gesture.base;

      if (gesture.kind === 'resize') {
        return gesture.size
          ? this.resizeNode(gesture.ids[0], gesture.size) !== null
          : false;
      }

      return this.nudgeNodes(gesture.ids, gesture.dx, gesture.dy);
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

    // Resizes a node to the box a mouse resize left, as one undo step. A drag
    // of the top or left side also moves it (see resizedBox).
    resizeNodeBox(id, box) {
      const node = findNode(this.doc, id);

      if (!node) {
        return null;
      }

      const { position, size } = resizedBox(this.doc, id, box);
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

    // Sets a line's relative points as one undo step, at the end of a point
    // drag. Returns the history entry, or null when nothing changed.
    setLinePoints(id, points, label) {
      const node = findNode(this.doc, id);
      const next = setLinePoints(this.doc, id, points);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, label || `Moved a point of ${nodeLabel(node)}`);
    },

    // Moves one point of a line, as the arrow keys on its handle do.
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

    // Adds a bend in the segment that ends at point `index`, at `point` or the
    // segment's middle. Returns null when the line takes no more points.
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

    // Removes a point of a line. A line always keeps at least two points.
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

    // Returns the history entry, or null when nothing moved. Pass announce:
    // false when the caller shows the refusal itself.
    setParent(id, parentId, { announce = true } = {}) {
      const next = setParent(this.doc, id, parentId, placing());

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

    // Refuses a change to the interfaces of an included device. Returns
    // whether it refused.
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

    // Returns {edge} or {error}. Pass announce: false when the caller shows the
    // refusal in its own alert.
    connect(connection, { announce = true } = {}) {
      const result = connect(this.doc, connection, connectOptions());
      // When the caller says why itself, an edit that commit would refuse
      // is found first, because commit would also say it.
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

      this.announceAddresses(
        result.assigned ? [result.assigned] : [],
        result.full,
        result.edge.networkId,
        result.unaddressed ? [result.unaddressed] : [],
      );

      return { edge: result.edge };
    },

    // Says what connections did to interface addresses (see connect in
    // model.js). An unaddressed static interface needs a typed address before
    // the Inspector applies its device.
    announceAddresses(assigned, full, networkId, unaddressed = []) {
      const named = (entry) =>
        `${entry.name} of ${findNode(this.doc, entry.nodeId)?.device?.hostname}`;
      const set = assigned.map(
        (entry) => `${named(entry)} to ${entry.address}/${entry.mask}`,
      );

      if (set.length) {
        this.announce(`Set the address of ${listOf(set)}.`);
      }

      this.announceGateway(assigned, networkId);

      if (unaddressed.length) {
        const network = findNetwork(this.doc, networkId);
        const interfaces = listOf(unaddressed.map(named));

        this.announce(
          parseSubnet(network?.subnet)
            ? `No address set. Type an address for ${interfaces} in the Inspector, or select Assign addresses.`
            : `No address set: network ${network?.name} has no subnet. Type an address for ${interfaces} in the Inspector.`,
        );
      }

      if (full) {
        const network = findNetwork(this.doc, networkId);

        this.announce(
          `No address set: subnet ${network?.subnet} of network ${network?.name} is full.`,
        );
      }
    },

    // Which interface of `assigned` now has its network's gateway address,
    // and whether it set the gateway (see assignAddresses in addressing.js).
    announceGateway(assigned, networkId) {
      const holder = assigned.find((entry) => entry.holdsGateway);

      if (!holder) {
        return;
      }

      const network = findNetwork(this.doc, networkId)?.name;
      const named = `${holder.name} of ${findNode(this.doc, holder.nodeId)?.device?.hostname}`;

      this.announce(
        holder.setGateway
          ? `Set the gateway of network ${network} to ${holder.address}, the address of ${named}.`
          : `${named} has the gateway address of network ${network}.`,
      );
    },

    // Assign addresses: gives each unaddressed static interface on a network
    // the next free address, as one undo step (see assignAddresses in
    // addressing.js).
    assignAddresses(networkId) {
      const network = findNetwork(this.doc, networkId);

      if (!network) {
        return null;
      }

      if (!parseSubnet(network.subnet)) {
        this.refuse(
          `Network ${network.name} has no subnet. Set its Subnet in the Inspector, then select Assign addresses again.`,
        );

        return null;
      }

      const result = assignAddresses(this.doc, networkId);
      const full = result.full
        ? ` Subnet ${network.subnet} is full: ${count(result.left, 'interface')} ${result.left === 1 ? 'has' : 'have'} no address.`
        : '';

      if (result.assigned.length === 0) {
        this.announce(
          full
            ? `No addresses assigned on network ${network.name}.${full}`
            : `No addresses assigned: each static interface on network ${network.name} has an address.`,
        );

        return null;
      }

      if (
        !this.commit(
          result.doc,
          `Assigned ${count(result.assigned.length, 'address', 'addresses')} on network ${network.name}`,
        )
      ) {
        return null;
      }

      this.announceGateway(result.assigned, networkId);

      if (full) {
        this.announce(full.trim());
      }

      return result;
    },

    // Canvas drag-to-connect: any device or switch to any device or switch.
    // A new switch between two devices is on the grid while Snap to grid is
    // on, and an interface gets an address while Assign addresses on
    // connect is on.
    connectNodes(connection) {
      const result = connectNodes(this.doc, connection, {
        ...placing(),
        ...connectOptions(),
      });

      if (result.error) {
        this.refuse(result.error);

        return { error: result.error };
      }

      const took = this.commit(
        result.doc,
        result.node
          ? 'Connected devices through a new switch'
          : 'Connected nodes',
      );

      if (took) {
        this.announceAddresses(
          result.assigned || [],
          result.full,
          result.edges[0]?.networkId,
          result.unaddressed || [],
        );
      }

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

    // Selected items outside `selection` stay selected. Items that would
    // change an included device stay too (see removalRefusal), and the
    // removal says how many it kept. Returns whether anything was removed.
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
            : `Builder deleted nothing: the ${count(kept.length, 'selected item')} ` +
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

      if (!this.commit(result.doc, 'Grouped selection')) {
        return null;
      }

      this.selection = { nodes: [result.group.id], edges: [] };

      return result.group;
    },

    ungroup(groupId) {
      const id = groupId || this.selection.nodes[0];
      const next = id ? ungroup(this.doc, id) : this.doc;

      // Only a group ungroups. For anything else, there is no edit, and the
      // store says so.
      if (next === this.doc) {
        this.announce('Select a group to ungroup.');

        return;
      }

      this.commit(next, 'Ungrouped nodes');
      this.selection = emptySelection();
    },

    /**
     * Lays the diagram out as one commit that restoreLayout reverses. It runs
     * `algorithm`, else the draft's layout or the Settings default, and keeps
     * the layout that ran. A layout that changes nothing is not an edit.
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
        this.announce('The diagram already has this layout.');

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
     * Groups the ungrouped (or selected) devices and switches by network or name
     * (see grouping.js) and lays them out as one commit (see makeGroups).
     * 'pattern' does nothing here: use autoGroupByPattern.
     */
    async autoGroup(strategy = 'network') {
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

      return makeGroups(this, planned, groupingPhrase(strategy), {});
    },

    /**
     * Auto-group by name pattern, as autoGroup does (see planPatternGroups).
     * The pattern runs in a Web Worker. A stop returns `problem` and changes
     * nothing. A pattern that compiles is kept for the next time.
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
        return refused('This draft is read only.');
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
        // The dialog closed in the meantime, so there is no one to tell.
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

      // An edit, an undo or another document while the pattern ran. The
      // plan is for the earlier diagram.
      if (this.history !== history || history.currentEntry().id !== entryId) {
        this.announce(
          'The diagram changed during Auto-group. Builder made no groups.',
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

    // Restores what the last automatic layout changed, as one commit that undo
    // reverses. Offered only while the diagram is as that layout left it.
    restoreLayout() {
      if (!this.canRestoreLayout) {
        this.announce('There is no earlier layout to restore.');

        return null;
      }

      // commit() drops the saved layout, so it is restored only once.
      return this.commit(
        restoreLayoutChanges(this.doc, this.layoutRestore),
        'Restored previous layout',
      );
    },

    // --- device templates of the diagram -----------------------------------

    // Saves a device template in the open diagram, as one edit. Returns it with
    // its id, or null when the diagram did not take it.
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

    // Changes a template of the open diagram, as one edit. Devices made from it
    // before stay as they are.
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

    // Deletes a template from the open diagram, as one edit that Undo
    // restores.
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
     * Reads the template library, then checks the open diagram's copies against
     * it (see templateLinks). A failure goes in `templates.error`, never the
     * page alert. A stale answer is dropped.
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
            // A later read is still in progress.
            status: read === libraryReads ? 'ready' : 'loading',
            error: '',
            loaded: true,
            items: library.templates,
            collections: library.collections,
            builtins: library.builtins,
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

    // Why a change of the template library failed, for the dialog or page that
    // asked for it.
    describeLibraryError(error, action) {
      return `Could not ${action}. ${
        error instanceof LibraryError
          ? error.reason
          : libraryErrorMessage(error)
      }`;
    },

    // Adds templates to the library as copies with server ids, optionally in a
    // new collection, and reads the library again. Rejects with the failure.
    async createLibraryTemplates(templates, { collection = null } = {}) {
      const result = await libraryWrite(() =>
        builderApi.createTemplates({
          templates: templates.map(templateContent),
          collection,
        }),
      );

      await this.fetchTemplates();

      return result;
    },

    /**
     * Saves a diagram template to the library and links it, in one undo step.
     * A library template has no icon, so its custom icon goes to the icon
     * library first (see ingestSavedTemplateIcons).
     */
    async saveTemplateToLibrary(template, { library = iconLibrary } = {}) {
      const warnings = await ingestSavedTemplateIcons(
        savedTemplateIcons(template, this.doc?.icons),
        library,
      );
      const { created } = await this.createLibraryTemplates([template]);
      const id = created[0]?.id;
      const saved = this.templates.items.find(
        (item) => item.source === 'library' && item.id === id,
      );

      this.linkTemplateCopy(
        template.id,
        saved || { source: 'library', id, version: 1 },
      );

      return warnings;
    },

    // Links a diagram template to the library template it equals, in one undo
    // step after Save to library. The link records the copy's digest.
    linkTemplateCopy(id, target) {
      const copy = (this.doc.templates || []).find((entry) => entry.id === id);

      if (!copy || typeof target?.id !== 'string' || !target.id) {
        return null;
      }

      return this.commit(
        updateTemplate(this.doc, id, { source: templateSource(target, copy) }),
        `Linked template ${copy.name} to the library`,
      );
    },

    /**
     * Updates diagram templates from newer library versions (see templateLink),
     * in one undo step. No ids means every copy that has an update. Devices
     * made from the templates do not change.
     */
    updateTemplateCopies(ids = []) {
      const wanted = new Set(ids);
      const updates = this.templateUpdates.filter(
        (link) => wanted.size === 0 || wanted.has(link.template.id),
      );

      if (updates.length === 0) {
        return null;
      }

      const next = updates.reduce(
        (doc, link) =>
          updateTemplate(doc, link.template.id, updatedCopy(link.latest)),
        this.doc,
      );
      const updated = updates.map((link) =>
        next.templates.find((entry) => entry.id === link.template.id),
      );

      return this.commit(next, templatesUpdatedLabel(updated));
    },

    // Removes a diagram template's link to its library template, in one undo
    // step. The template stays as it is and gets no more updates.
    unlinkTemplate(id) {
      const copy = (this.doc.templates || []).find((entry) => entry.id === id);

      if (!copy?.source) {
        return null;
      }

      return this.commit(
        updateTemplate(this.doc, id, { source: null }),
        templateUnlinkedLabel(copy),
      );
    },

    /**
     * Imports a template file (see parseTemplateFile) into the library as a new
     * collection, with " (2)" and so on added to a taken name. Its custom icons
     * go to the icon library first (see ingestTemplateIcons).
     */
    async importTemplateFile(file, { library = iconLibrary } = {}) {
      const full = importProblem(this.templates, file.templates.length);

      if (full) {
        throw new LibraryError(full);
      }

      const warnings = await ingestTemplateIcons(file.icons, library);
      const name = uniqueCollectionName(
        file.name,
        this.libraryCollections.map((collection) => collection.name),
      );

      await this.createLibraryTemplates(file.templates, {
        collection: {
          name,
          ...(file.description ? { description: file.description } : {}),
        },
      });

      return { collection: name, templates: file.templates.length, warnings };
    },

    // Replaces a library template and raises its version, so diagram copies
    // offer an update. A change after `etag` was read is refused (412).
    async updateLibraryTemplate(id, content, etag) {
      const saved = await libraryWrite(() =>
        builderApi.updateTemplate(
          id,
          { description: '', ...templateContent(content) },
          etag,
        ),
      );

      await this.fetchTemplates();

      return saved;
    },

    // Adds a collection to the library and reads the library again. It rejects
    // with the failure.
    async createCollection({ name, description = '', templateIds = [] }) {
      const created = await libraryWrite(() =>
        builderApi.createTemplateCollection({
          name,
          description,
          templateIds,
        }),
      );

      await this.fetchTemplates();

      return created;
    },

    // Replaces a library collection whole. A change after `etag` was read is
    // refused (412). It rejects with the failure.
    async updateCollection(id, { name, description = '', templateIds }, etag) {
      const saved = await libraryWrite(() =>
        builderApi.updateTemplateCollection(
          id,
          { name, description, templateIds },
          etag,
        ),
      );

      await this.fetchTemplates();

      return saved;
    },

    /**
     * Adds or removes templates of a library collection, sent whole. On a 412
     * it reads the collection again and makes the change once more. Returns
     * false when it already was as requested.
     */
    async changeCollectionMembers(id, { add = [], remove = [] }) {
      const send = async (retried) => {
        const collection = this.templates.collections.find(
          (entry) => entry.id === id && entry.source === 'library',
        );

        if (!collection) {
          throw new LibraryError(
            'This collection is no longer in the library.',
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
     * Deletes library templates and collections in one request, each at its
     * listed version. Collections and templates do not delete each other. If
     * one changed since the read, nothing is deleted (LibraryError).
     */
    async deleteLibraryItems({ templates = [], collections = [] }) {
      // Each id with the tag the library has.
      const tagged = (ids, items) =>
        ids.flatMap((id) => {
          const item = items.find(
            (entry) => entry.id === id && entry.source === 'library',
          );

          return item?.etag ? [{ id, etag: item.etag }] : [];
        });
      const selection = {
        templates: tagged(templates, this.templates.items),
        collections: tagged(collections, this.templates.collections),
      };

      // An id that the library no longer lists was deleted by someone else.
      if (
        selection.templates.length !== templates.length ||
        selection.collections.length !== collections.length
      ) {
        await this.fetchTemplates();

        throw new LibraryError(LIBRARY_CHANGED);
      }

      let deleted;

      try {
        deleted = await libraryWrite(() =>
          builderApi.deleteTemplates(selection),
        );
      } catch (error) {
        if (error?.response?.status !== 412) {
          throw error;
        }

        await this.fetchTemplates();

        throw new LibraryError(LIBRARY_CHANGED);
      }

      await this.fetchTemplates();

      return deleted;
    },

    setInfo(patch) {
      this.commit(setDocumentInfo(this.doc, patch), 'Renamed diagram');
    },

    /**
     * Replaces the diagram notes in one undo step (see setDiagramNotes in
     * model.js). Blank notes are dropped. Unchanged notes, or a note that
     * cannot be written (see diagramNoteProblem), take no step.
     */
    setDiagramNotes(notes) {
      const next = setDiagramNotes(this.doc, notes);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, 'Updated diagram notes');
    },

    // Sets the diagram's icon size in one undo step (see setIconSize in
    // model.js). A node with its own size keeps it.
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

    // Puts a device or switch at a layer, or none, in one undo step (see
    // setNodeLayer in model.js). An included device is refused.
    setNodeLayer(nodeId, layer) {
      if (this.refuseIncluded(nodeId)) {
        return null;
      }

      const next = setNodeLayer(this.doc, nodeId, layer);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, nodeLayerLabel(next, nodeId));
    },

    /**
     * Puts the devices and switches among nodeIds at a layer, or none, in one
     * undo step (see setNodeLayers in model.js). Included devices keep their
     * layer, and the label counts them. No change says why.
     */
    setNodesLayer(nodeIds, layer) {
      const wanted = layerIdOf(layer);

      if (this.readOnly) {
        // As commit refuses it.
        this.setError(READ_ONLY_REFUSAL);

        return null;
      }

      if (wanted === undefined) {
        return null;
      }

      // One pass over the document, whatever the number of nodes asked for.
      const asked = new Set(nodeIds);
      const nodes = (this.doc?.nodes || []).filter(
        (node) => asked.has(node.id) && LAYER_KINDS.includes(node.kind),
      );
      const kept = nodes.filter((node) => includedReason(node));
      const { doc: next, changed } = setNodeLayers(
        this.doc,
        nodes.map((node) => node.id),
        wanted,
      );

      if (!changed.length) {
        if (!nodes.length) {
          this.announce('Select a device or a switch.');
        } else if (kept.length === nodes.length) {
          this.refuse(
            kept.length === 1
              ? includedReason(kept[0])
              : `The ${count(kept.length, 'selected node')} are read only here. ${includedReason(kept[0])}`,
          );
        } else {
          // The read-only nodes cannot change, so the message is about the
          // other nodes.
          const which = kept.length
            ? 'selected node that you can change'
            : 'selected node';

          this.announce(
            wanted === null
              ? `No ${which} has a layer of its own.`
              : `Every ${which} is at ${layerName(this.doc, wanted)} already.`,
          );
        }

        return null;
      }

      const label =
        changed.length === 1
          ? nodeLayerLabel(next, changed[0])
          : wanted === null
            ? `Removed the layer of ${count(changed.length, 'node')}`
            : `Changed the layer of ${count(changed.length, 'node')} to ${layerLabel(next, wanted)}`;

      return this.commit(
        next,
        kept.length
          ? `${label}. Kept ${count(kept.length, 'read-only node')} of included topologies`
          : label,
      );
    },

    // Replaces the diagram's layers in one undo step (see setLayers in
    // model.js). An empty list gives the placeholder layers. Invalid layers
    // (see layersProblem) take no step.
    setLayers(layers, label = 'Changed the layers of the diagram') {
      const next = setLayers(this.doc, layers);

      if (next === this.doc) {
        return null;
      }

      return this.commit(next, label);
    },

    // Adds a layer with the next free ID in one undo step (see addLayer in
    // model.js). Placeholder layers stay. Returns the layer, or null.
    addLayer(label) {
      const { doc: next, layer } = addLayer(this.doc, label);

      if (!layer) {
        return null;
      }

      return this.commit(next, `Added ${layerName(next, layer.id)}`)
        ? layer
        : null;
    },

    // Relabels a layer in one undo step (see relabelLayer in model.js). An
    // invalid label (see layerLabelProblem) takes no step.
    relabelLayer(id, label) {
      const next = relabelLayer(this.doc, id, label);

      if (next === this.doc) {
        return null;
      }

      return this.commit(
        next,
        `Renamed ${layerName(this.doc, id)} to ${label}`,
      );
    },

    // Gives a layer a new ID in one undo step (see renumberLayer in model.js).
    // Its devices, switches and templates move with it.
    renumberLayer(from, to) {
      const next = renumberLayer(this.doc, from, to);

      if (next === this.doc) {
        return null;
      }

      const id = Number(to);

      return this.commit(
        next,
        `Changed the ID of ${layerName(this.doc, from)} to ${id}`,
      );
    },

    // Removes a layer in one undo step (see removeLayer in model.js). Its
    // nodes keep its ID. No layers left gives the placeholder layers.
    removeLayer(id) {
      const { doc: next, nodeIds } = removeLayer(this.doc, id);

      if (next === this.doc) {
        return null;
      }

      const name = layerName(this.doc, id);

      return this.commit(
        next,
        nodeIds.length
          ? `Removed ${name}. ${count(nodeIds.length, 'node')} ${nodeIds.length === 1 ? 'stays' : 'stay'} at ${placeholderLabel(id)}`
          : `Removed ${name}`,
      );
    },

    setViewport(viewport) {
      this.doc = setViewport(this.doc, viewport);
    },

    // Changes the diagram's grid in one undo step (see setGrid in model.js).
    // A size outside MIN_GRID_SIZE to MAX_GRID_SIZE takes no step.
    setGrid(patch) {
      const next = setGrid(this.doc, patch);

      if (next === this.doc) {
        return null;
      }

      return this.commit(
        next,
        next.grid.size === this.doc.grid?.size
          ? 'Changed the grid'
          : `Changed the grid size to ${next.grid.size}`,
      );
    },

    // Replaces the diagram's Scenario configs in one undo step (see
    // setScenarios in model.js).
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
     * Stores a Scenario config: POST, or PUT with `replace`. A PUT replaces the
     * annotations, so a replacement keeps the stored topology annotation, which
     * phenix needs to make an experiment (see replacedScenarioConfig).
     */
    async saveScenarioConfig(config, { replace = false, exact = false } = {}) {
      if (replace && exact) {
        await builderApi.updateConfig(config);
      } else if (replace) {
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

      const result = pasteClipboard(this.doc, this.clipboard, connectOptions());

      if (
        !this.commit(
          result.doc,
          `Pasted ${count(result.nodeIds.length, 'node')}`,
        )
      ) {
        return null;
      }

      this.selection = { nodes: result.nodeIds, edges: [] };

      return result.nodeIds;
    },

    // Pastes a copy of the selection beside it, as Copy and Paste would,
    // without the clipboard. A connection goes only with its two nodes.
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

      const result = pasteClipboard(this.doc, payload, connectOptions());

      if (
        !this.commit(
          result.doc,
          `Duplicated ${count(result.nodeIds.length, 'node')}`,
        )
      ) {
        return null;
      }

      this.selection = { nodes: result.nodeIds, edges: [] };

      return result.nodeIds;
    },

    undo() {
      if (this.refuseWhileResolving()) {
        return;
      }

      // A move made with a held key is undone whole.
      this.endKeyGesture();

      if (!this.history.canUndo()) {
        this.announce('Nothing to undo.');

        return;
      }

      const label = this.history.undoLabel();

      this.doc = this.history.undo();
      // Dropped here, so a redo to the layout also cannot offer it again.
      this.layoutRestore = null;
      this.historyChanged();
      this.selection = emptySelection();
      // A blocked queue keeps the move on this device only, so say so.
      this.announce(`Undid ${label}${this.unsavedNote() || '.'}`);
      this.moveHistoryCursor();
    },

    redo() {
      if (this.refuseWhileResolving()) {
        return;
      }

      this.endKeyGesture();

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

    // Fix, from the Diagram checks: makes the edit that fixes the issue (see
    // issueFix in issueFixes.js) as one Undo step. Returns its name, or ''.
    fixIssue(issue) {
      const fix = issueFix(this.doc, issue);

      if (!fix) {
        this.announce('This issue has no fix now.');

        return '';
      }

      return this.commit(fix.apply(this.doc), fix.label) ? fix.label : '';
    },

    /**
     * Go to: selects what an issue is about (see issueTarget) and asks for
     * focus on its Inspector field (see Builder.vue). With connections hidden,
     * Go to a connection selects its source node and sets hiddenEdge.
     */
    goToIssue(issue) {
      let target = issueTarget(this.doc, toIssue(issue));

      if (!target) {
        return false;
      }

      if (
        target.kind === 'edges' &&
        builderSettings.showConnections === false
      ) {
        const edge = (this.doc.edges || []).find(
          (entry) => entry.id === target.id,
        );

        target = {
          kind: 'nodes',
          id: edge.sourceNodeId,
          field: '',
          hiddenEdge: true,
        };
      }

      this.select({ [target.kind]: [target.id] });
      this.focusRequest = {
        ...target,
        token: (this.focusRequest?.token || 0) + 1,
      };

      return true;
    },

    // Takes the Go to request with `token` once, so an Inspector that mounts
    // later still acts on it and no Inspector acts on it twice.
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

    // Without edges, only the nodes: the canvas hides the connections.
    selectAll({ edges = true } = {}) {
      this.selection = {
        nodes: (this.doc.nodes || []).map((node) => node.id),
        edges: edges ? (this.doc.edges || []).map((edge) => edge.id) : [],
      };
      this.announce(edges ? 'Selected everything.' : 'Selected every node.');
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

    // The Settings dialog says nothing. Its radio buttons speak for
    // themselves, and the live region would say it after the dialog closes.
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

    // The toolbar's toggle, which uses what System shows now (see
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
    // request still in progress for them is dropped when it answers.
    endSession() {
      sessionEpoch += 1;
      disksRequest = null;
      forgetUser(this);
    },

    // Whether the session ended after `epoch` was read (see endSession). A
    // draft opened or made for the previous user that answers after that is
    // dropped, with any queue it started, and the store is left empty.
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
