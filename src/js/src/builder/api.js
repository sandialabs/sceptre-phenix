// Builder API client. Path builders are exported apart so tests and the
// autosave queue use the same routes. Every mutation sends the last seen ETag
// as If-Match, so one editor never overwrites another unseen. By design, no
// call has a "force" variant.

import { usePhenixStore } from '@/store.js';
import axiosInstance from '@/utils/axios.js';

import { count } from './announce.js';
import { BulkError } from './bulk.js';
import { MAX_DOCUMENT_BYTES } from './limits.js';
import { hasControlCharacters, utf8Length } from './text.js';
import { isPlainObject } from './values.js';

export const DRAFTS_PATH = 'builder/drafts';
export const SOURCES_PATH = 'builder/sources';
export const GENERATE_PATH = 'builder/generate';
export const LEGACY_PATH = 'builder/legacy';
export const EXPORT_TOPOLOGY_PATH = 'builder/export/topology';
// Builder packages (web/builder_package.go): one is made of a document, and
// one is checked against what this server has.
export const PACKAGE_PATH = 'builder/package';
const RESOLVE_PACKAGE_PATH = 'builder/package/resolve';
export const DOCUMENTS_PATH = 'builder/documents';
export const CLEANUP_PATH = 'builder/cleanup';
// The server's icon library (web/builder_icons.go).
const ICONS_PATH = 'builder/icons';
// The node template library (web/builder_templates.go).
export const TEMPLATES_PATH = 'builder/templates';
export const SCHEMA_PATH = 'schemas/builder/v1';
// The server's layer set library, and its default set
// (web/builder_layersets.go).
export const LAYER_SETS_PATH = 'builder/layersets';
export const DEFAULT_LAYER_SET_PATH = `${LAYER_SETS_PATH}/default`;
// Disk images, the same listing the Disks page reads (web/disk.go GetDisks).
export const DISKS_PATH = 'disks';

/** A stored config's path, as the Configs page reads it (web/config.go GetConfig). */
export function configPath(kind, name) {
  return `configs/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`;
}

/** An experiment's path, as its page reads it (web/handlers.go GetExperiment). */
export function experimentPath(name) {
  return `experiments/${encodeURIComponent(name)}`;
}

/**
 * @param {string} id draft id
 * @returns {string}
 */
export function draftPath(id) {
  return `${DRAFTS_PATH}/${encodeURIComponent(id)}`;
}

/**
 * @param {string} id
 * @param {string} [snapshotId]
 * @returns {string}
 */
export function snapshotPath(id, snapshotId) {
  const base = `${draftPath(id)}/snapshots`;
  return snapshotId ? `${base}/${encodeURIComponent(snapshotId)}` : base;
}

/**
 * @param {string} id
 * @returns {string}
 */
export function cursorPath(id) {
  return `${draftPath(id)}/cursor`;
}

/**
 * @param {string} id
 * @returns {string}
 */
export function publishPath(id) {
  return `${draftPath(id)}/publish`;
}

/**
 * @param {string} id
 * @returns {string} where the draft's preflight checks are run
 */
export function preflightPath(id) {
  return `${draftPath(id)}/preflight`;
}

/**
 * @param {string} id published document id
 * @returns {string}
 */
export function documentPath(id) {
  return `${DOCUMENTS_PATH}/${encodeURIComponent(id)}`;
}

/**
 * @param {string} name icon name, or one of its aliases
 * @returns {string} the icon of that name in the icon library
 */
export function iconPath(name) {
  return `${ICONS_PATH}/${encodeURIComponent(String(name))}`;
}

// Where templates are added to the server-wide template library.
export const TEMPLATE_ITEMS_PATH = `${TEMPLATES_PATH}/items`;

/**
 * @param {string} id
 * @returns {string} the layer set of that ID in the layer set library
 */
export function layerSetPath(id) {
  return `${LAYER_SETS_PATH}/${encodeURIComponent(String(id))}`;
}

/**
 * A layer set from the server with each field of the right type and defaults
 * (match conflict lowest, version 0), or null when it has no ID.
 */
export function readLayerSet(value) {
  if (!value || typeof value !== 'object' || typeof value.id !== 'string') {
    return null;
  }

  const text = (field) => (typeof field === 'string' ? field : '');

  return {
    id: value.id,
    name: text(value.name),
    description: text(value.description),
    iconKey: text(value.iconKey),
    icon: text(value.icon),
    matchConflict: value.matchConflict === 'highest' ? 'highest' : 'lowest',
    layerOutlines: value.layerOutlines === true,
    layers: Array.isArray(value.layers)
      ? value.layers.filter((layer) => layer && typeof layer === 'object')
      : [],
    builtin: value.builtin === true,
    version: Number.isInteger(value.version) ? value.version : 0,
    etag: text(value.etag),
    created: text(value.created),
    updated: text(value.updated),
    createdBy: text(value.createdBy),
    updatedBy: text(value.updatedBy),
  };
}

/**
 * @param {string} id template id
 * @returns {string} one template of the library
 */
export function templateItemPath(id) {
  return `${TEMPLATE_ITEMS_PATH}/${encodeURIComponent(id)}`;
}

// Where collections are added to the library.
export const TEMPLATE_COLLECTIONS_PATH = `${TEMPLATES_PATH}/collections`;

/**
 * @param {string} id collection id
 * @returns {string} one collection of the library
 */
export function templateCollectionPath(id) {
  return `${TEMPLATE_COLLECTIONS_PATH}/${encodeURIComponent(id)}`;
}

// Where templates and collections of the library are deleted.
export const TEMPLATE_DELETE_PATH = `${TEMPLATES_PATH}/delete`;

/**
 * @param {string} topology topology name
 * @returns {string} the Builder document the topology references
 */
export function topologyDocumentPath(topology) {
  return `builder/topologies/${encodeURIComponent(topology)}/document`;
}

// A topology whose Builder document is read from the file that it names has
// no published document, and thus no document id. Its row in the listing
// gets a handle in place of the id, "file/<topology>". No id can have this
// form (an id holds no slash). Thus cards, commands and the editor name the
// row as they name any other row.
const FILE_HANDLE = 'file/';

/**
 * @param {string} topology topology name
 * @returns {string} the listing id of the diagram read from its Builder file
 */
export function fileHandle(topology) {
  return `${FILE_HANDLE}${topology}`;
}

/**
 * @param {string} id a listed published diagram's id
 * @returns {string} the topology whose Builder file is the source of the
 *   diagram, or '' when the id is a published document's id
 */
export function fileTopology(id) {
  return typeof id === 'string' && id.startsWith(FILE_HANDLE)
    ? id.slice(FILE_HANDLE.length)
    : '';
}

// The server's default layer set, from a listing or from the default's own
// route: its id (null for none) and the tag that a change of it names.
function readDefaultLayerSet({ id, etag }) {
  return {
    defaultId: typeof id === 'string' && id !== '' ? id : null,
    defaultEtag: typeof etag === 'string' && etag !== '' ? etag : null,
  };
}

// A listed published diagram, a file's with its handle as its id.
function listedDocument(row) {
  return row?.source === 'file' && typeof row.target === 'string'
    ? { ...row, id: fileHandle(row.target) }
    : row;
}

// The longest name of an uploaded file a draft records, in UTF-8 bytes
// (MaxSourceFileLength in api/builder).
export const MAX_SOURCE_FILE_BYTES = 255;

/**
 * The uploaded file name a new draft records, or '' for one the server would
 * refuse (ValidateSourceFile in api/builder). The name is only shown, so the
 * upload goes on without it.
 */
export function sourceFileName(name) {
  const text = typeof name === 'string' ? name : '';
  const refused =
    text === '.' ||
    text === '..' ||
    /[/\\]/.test(text) ||
    hasControlCharacters(text) ||
    utf8Length(text) > MAX_SOURCE_FILE_BYTES;

  return refused ? '' : text;
}

/**
 * Reads the draft ETag from the response body, else from the ETag header.
 * The body comes first because a compressing proxy rewrites the header
 * (W/"3", "3-gzip"), and the server refuses such a tag in If-Match.
 */
export function readETag(response) {
  const data = response?.data;
  const body = [data?.etag, data?.draft?.etag].find(
    (value) => typeof value === 'string' && value !== '',
  );

  if (body) {
    return body;
  }

  const headers = response?.headers;

  if (!headers) {
    return null;
  }

  const value =
    typeof headers.get === 'function'
      ? headers.get('etag')
      : headers.etag || headers.ETag;

  return value || null;
}

/**
 * The draft's current ETag from a 412 header, so a delete can retry with it.
 * null for a tag a proxy rewrote (see readETag): the caller reads the draft.
 */
export function preconditionETag(error) {
  const response = error?.response;

  if (response?.status !== 412) {
    return null;
  }

  const headers = response.headers;
  const value =
    typeof headers?.get === 'function'
      ? headers.get('etag')
      : headers?.etag || headers?.ETag;
  const tag = typeof value === 'string' ? value.trim() : '';

  return /^"[^"]+"$/.test(tag) && !tag.endsWith('-gzip"') ? tag : null;
}

// The largest request body the server reads: a document (MAX_DOCUMENT_BYTES)
// and 1 MiB for the rest of the request (builderMaxRequestBytes in web).
export const MAX_REQUEST_BYTES = MAX_DOCUMENT_BYTES + 1024 * 1024;

function mebibytes(bytes) {
  return `${Number((bytes / (1024 * 1024)).toFixed(1))} MiB`;
}

/**
 * An upload refused before it is sent for its size. The server resets such a
 * body early, and some browsers (Firefox) then report an unreachable server.
 */
export class TooLargeError extends Error {
  /**
   * @param {string} what the thing too large, as a sentence subject
   * @param {number} bytes its size
   * @param {number} limit the size the server accepts
   */
  constructor(what, bytes, limit) {
    super(`${what} is larger than the ${mebibytes(limit)} the server accepts.`);
    this.name = 'TooLargeError';
    this.bytes = bytes;
    this.limit = limit;
  }
}

// Whether text is over limit bytes as UTF-8. Encoding is skipped when the
// length alone decides (one to three bytes per UTF-16 code unit).
function exceeds(text, limit) {
  if (text.length > limit) {
    return true;
  }

  return text.length * 3 > limit && utf8Length(text) > limit;
}

/** Refuses an upload over MAX_DOCUMENT_BYTES of content or MAX_REQUEST_BYTES of body. */
function checkUploadSize(payload, content = 'The config file') {
  const body = JSON.stringify(payload);

  // The document and the content are part of the body.
  if (!exceeds(body, MAX_DOCUMENT_BYTES)) {
    return;
  }

  const diagram = payload.document !== undefined;
  const part = diagram ? JSON.stringify(payload.document) : payload.content;

  if (typeof part === 'string' && exceeds(part, MAX_DOCUMENT_BYTES)) {
    throw new TooLargeError(
      diagram ? 'This diagram' : content,
      utf8Length(part),
      MAX_DOCUMENT_BYTES,
    );
  }

  if (exceeds(body, MAX_REQUEST_BYTES)) {
    throw new TooLargeError(
      'This request',
      utf8Length(body),
      MAX_REQUEST_BYTES,
    );
  }
}

/** Classifies an API failure so the UI can show a specific state. */
export function classifyError(error) {
  if (error instanceof TooLargeError) {
    return 'too-large';
  }

  const status = error?.response?.status;

  if (status === 409 || status === 412 || status === 428) {
    return 'conflict';
  }

  // The session ended (an expired or revoked token), unlike a refusal of
  // this user. The Builder logs out (see watchSession).
  if (status === 401) {
    return 'unauthenticated';
  }

  if (status === 403) {
    return 'forbidden';
  }

  if (status === 404) {
    return 'missing';
  }

  if (status === 413) {
    return 'too-large';
  }

  // The request itself was refused (a document the server will not store),
  // so sending it again cannot succeed.
  if (status === 400 || status === 422) {
    return 'invalid';
  }

  if (!error?.response) {
    return 'offline';
  }

  return 'error';
}

/** Whether a request got no answer (timeout or network). The server may still have done it. */
export function unanswered(error) {
  return !error?.response && ['ETIMEDOUT', 'ERR_NETWORK'].includes(error?.code);
}

/**
 * Whether a failure ends a bulk action (runBulk's stop in bulk.js): the
 * session ended or the server is unreachable. A BulkError never does.
 */
export function endsBulk(error) {
  return (
    !(error instanceof BulkError) &&
    ['unauthenticated', 'offline'].includes(classifyError(error))
  );
}

// An id in a server message, such as "draft <id> not found".
const UUID_PATTERN =
  /\s*\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;

// Leading "prefix: " segments of a server cause chain that carry no reason:
// package and wrapper prefixes, and document paths such as nodes[1].device.
const CAUSE_PREFIX =
  /^(?:builder|invalid request|document|invalid builder document|is not a valid builder document|validating topology projection|config validation failed|[\w-]+(?:\[\d+\]|\.[\w-]+)+)$/i;

/**
 * @param {string} text
 * @returns {string} the text capitalized and ending in a full stop
 */
export function sentence(text) {
  const trimmed = text.trim();

  if (!trimmed) {
    return '';
  }

  const capital = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);

  return /[.!?]$/.test(capital) ? capital : `${capital}.`;
}

// A server message that only names the operation ("unable to save builder
// draft <id>", "builder document cannot be published as topology <name>").
// The reason is then in the cause.
const GENERIC_MESSAGE =
  /^(?:unable to |builder document cannot be (?:published as topology \S+|projected)$)/i;

/**
 * The server's reason for a refusal as a sentence a user can act on, or ''.
 * A generic "unable to ..." message gives way to the first issue of the
 * cause, without prefixes, paths, indexes and ids. Ids are never shown.
 */
export function serverReason(error) {
  const data = error?.response?.data;

  if (!data || typeof data !== 'object') {
    return '';
  }

  const detail = data.error || data.message;
  const message =
    typeof detail === 'string' ? detail.replace(UUID_PATTERN, '').trim() : '';
  const cause = typeof data.cause === 'string' ? data.cause.trim() : '';

  if (!cause || (message && !GENERIC_MESSAGE.test(message))) {
    return sentence(message);
  }

  // Issues are separated by ". " before a lowercase issue or a path, and
  // schema errors by " | ". These can repeat each other.
  const [reason, ...others] = new Set(
    cause
      .split(/\. (?=[a-z[])| \| /)
      .filter((issue) => issue.trim())
      .map(causeReason),
  );
  const more = others.length;

  return sentence(
    more > 0 ? `${reason} (and ${count(more, 'more problem')})` : reason,
  );
}

/** The Builder error code a refusal names, such as document.invalid (see codes.go), or ''. */
export function serverCode(error) {
  const code = error?.response?.data?.code;

  return typeof code === 'string' ? code : '';
}

/**
 * Issues as the server reports them (BuilderIssue in openapi.yml). A plain
 * string from an earlier server is an issue with no code.
 */
export function readIssues(list, severity = 'error') {
  return (Array.isArray(list) ? list : []).flatMap((entry) => {
    if (typeof entry === 'string') {
      return entry ? [{ code: '', severity, message: entry }] : [];
    }

    if (
      !entry ||
      typeof entry !== 'object' ||
      typeof entry.message !== 'string'
    ) {
      return [];
    }

    return [
      {
        ...entry,
        code: typeof entry.code === 'string' ? entry.code : '',
        severity: ['error', 'warning'].includes(entry.severity)
          ? entry.severity
          : severity,
      },
    ];
  });
}

// The statuses of a preflight check (BuilderPreflightResult in openapi.yml).
const PREFLIGHT_STATUSES = ['passed', 'failed', 'unavailable'];

/**
 * A preflight report (BuilderPreflightReport in openapi.yml). An issue with
 * no severity is a warning, and an unknown check status counts as unavailable.
 */
export function readPreflightReport(data) {
  if (!Array.isArray(data?.checks)) {
    throw new TypeError('The server sent an unexpected preflight report.');
  }

  const checks = data.checks.flatMap((entry) =>
    entry && typeof entry.name === 'string'
      ? [
          {
            name: entry.name,
            status: PREFLIGHT_STATUSES.includes(entry.status)
              ? entry.status
              : 'unavailable',
            summary: typeof entry.summary === 'string' ? entry.summary : '',
            issues: readIssues(entry.issues, 'warning'),
          },
        ]
      : [],
  );
  const named = (status) =>
    checks
      .filter((check) => check.status === status)
      .map((check) => check.name);

  return {
    checks,
    passed: named('passed'),
    failed: named('failed'),
    unavailable: named('unavailable'),
  };
}

/** A refused publish as publishRefusal() takes it: code, reason and the scenario it names. */
export function serverRefusal(error) {
  const scenario = error?.response?.data?.metadata?.scenario;

  return {
    code: serverCode(error),
    reason: serverReason(error),
    scenario: typeof scenario === 'string' ? scenario : '',
  };
}

/**
 * What the server said about a refusal, word for word, as a sentence. For
 * messages meant to show unchanged, such as one that names a file path.
 */
export function serverSentence(error) {
  const data = error?.response?.data;
  const detail = data && typeof data === 'object' ? data.message : '';

  return typeof detail === 'string' ? sentence(detail) : '';
}

// One issue of a cause chain, without its prefixes, the other elements it
// names ("(also nodes[0])") and ids.
function causeReason(issue) {
  const segments = issue.split(': ');

  while (segments.length > 1 && CAUSE_PREFIX.test(segments[0].trim())) {
    segments.shift();
  }

  return segments
    .join(': ')
    .replace(/\s*\(also [^)]*\)/g, '')
    .replace(UUID_PATTERN, '');
}

/** Human readable message for a failure class. */
export function errorMessage(kind, error) {
  const detail = serverReason(error);

  switch (kind) {
    case 'conflict':
      return 'This draft changed on the server since you loaded it.';
    case 'unauthenticated':
      return 'Your session has ended. Log in again.';
    case 'forbidden':
      return detail || 'Your role does not include the Builder.';
    case 'missing':
      return detail || 'This draft no longer exists on the server.';
    case 'too-large':
      return (
        detail ||
        (error instanceof TooLargeError
          ? error.message
          : 'This diagram is too large to save.')
      );
    case 'invalid':
      return detail || 'The server refused the diagram as invalid.';
    case 'offline':
      return error?.code === 'ETIMEDOUT'
        ? 'The server did not answer in time. Check the connection and try again.'
        : 'The Builder could not reach the server. Check the connection and try again.';
    default:
      return detail || 'The server rejected the request.';
  }
}

/**
 * Why a template library request failed: the server's reason, which it
 * writes to be shown, or the common session and offline messages.
 */
export function libraryErrorMessage(error) {
  const kind = classifyError(error);

  if (['unauthenticated', 'offline'].includes(kind)) {
    return errorMessage(kind, error);
  }

  return (
    serverReason(error) ||
    (kind === 'forbidden'
      ? 'Your role does not include the Builder.'
      : 'The server rejected the request.')
  );
}

/**
 * Why a published diagram's experiment could not be opened. Any failure but
 * a refusal or an unreachable server means the experiment is gone.
 */
export function experimentError(name, error) {
  const kind = classifyError(error);
  const reasons = {
    forbidden: 'Your role does not allow it.',
    offline: errorMessage('offline'),
    unauthenticated: errorMessage('unauthenticated'),
  };

  return `Could not open experiment ${name}. ${
    reasons[kind] || 'Someone may have deleted or renamed it.'
  }`;
}

function ifMatch(etag) {
  return etag ? { headers: { 'If-Match': etag } } : {};
}

/**
 * Normalizes a draft envelope. Only a read carries the history. After a save
 * it is null (unknown), not empty.
 */
export function readEnvelope(response) {
  const data = response?.data || {};
  const draft = data.draft || data.metadata || data;
  const history = [draft.history, data.history].find(Array.isArray);

  return {
    draft,
    document: data.document || null,
    history: history || null,
    cursor: Number.isInteger(draft.cursor) ? draft.cursor : (data.cursor ?? 0),
    etag: readETag(response),
  };
}

const PUBLISH_MODES = ['topology', 'topology-experiment'];
const CONFIG_ACTIONS = ['create', 'update'];

/**
 * Builds the publish request body. Publish is an intent: the server publishes
 * the cursor's snapshot and checks it again, so no document bytes are sent.
 * Fields outside the intent are dropped.
 */
export function publishIntent(intent = {}) {
  const mode = PUBLISH_MODES.includes(intent.mode) ? intent.mode : 'topology';
  const name = (value) =>
    typeof value?.name === 'string' ? value.name.trim() : '';
  const target = (value) => {
    const action = CONFIG_ACTIONS.includes(value?.action) ? value.action : null;

    return name(value) && action ? { name: name(value), action } : null;
  };

  const topology = target(intent.topology);

  if (!topology) {
    throw new Error('Publish needs a topology name and an action.');
  }

  const body = { mode, topology };

  if (mode === 'topology-experiment') {
    const experiment = target(intent.experiment);

    if (!experiment) {
      throw new Error('Publish needs an experiment name and an action.');
    }

    if (name(intent.scenario)) {
      body.scenario = { name: name(intent.scenario) };
    }

    body.experiment = experiment;
  }

  return body;
}

// The code of the server's advice, on a partial publication, to publish again
// with the same choices.
const PUBLISH_RETRY_CODE = 'publish.retry.available';

/** Normalizes a publish response. A partial failure of its stages shows as a failure. */
export function readPublishResult(response) {
  const data = response?.data || {};
  const stages = (Array.isArray(data.stages) ? data.stages : []).map(
    (stage) => ({
      name: stage?.name || stage?.stage || 'stage',
      status: stage?.status || (stage?.error ? 'failed' : 'ok'),
      message: stage?.message || stage?.error || '',
      config: stage?.config || stage?.ref || null,
    }),
  );

  const failed = stages.filter((stage) =>
    ['failed', 'error'].includes(stage.status),
  );
  const succeeded = stages.filter((stage) =>
    ['ok', 'created', 'updated', 'used', 'skipped'].includes(stage.status),
  );

  const status =
    data.status ||
    (failed.length === 0
      ? 'succeeded'
      : succeeded.length > 0
        ? 'partial'
        : 'failed');
  // Each with its code (see readIssues). Warnings and errors are what their
  // names say, and the Publish dialog lists them. The advice to publish again
  // is not listed: the result's summary says it (see describePublishResult).
  const listed = (issue) => issue.code !== PUBLISH_RETRY_CODE;
  const warnings = readIssues(data.warnings, 'warning');
  const warningIssues = warnings.filter(listed);
  const errorIssues = readIssues(data.errors, 'error');

  return {
    status,
    partial: status === 'partial',
    // The same publish, sent again, finishes what this one started.
    retry: warnings.some((issue) => !listed(issue)),
    ok: failed.length === 0,
    stages,
    failed,
    warnings: warningIssues.map((entry) => entry.message),
    errors: errorIssues.map((entry) => entry.message),
    warningIssues,
    errorIssues,
    topology: data.topology || null,
    scenario: data.scenario || null,
    experiment: data.experiment || null,
    // The draft record, with the publication this one recorded.
    draft: data.draft || null,
  };
}

// The entries of a list of what publishing changes that name something.
function namedEntries(list) {
  return (Array.isArray(list) ? list : []).filter(
    (entry) => entry && typeof entry.name === 'string',
  );
}

// A config of what publishing changes, or null for none.
function configChange(value) {
  return value && typeof value.name === 'string'
    ? { name: value.name, action: String(value.action || '') }
    : null;
}

// A VLAN alias of what publishing changes, or null for none.
function aliasValue(value) {
  return Number.isInteger(value) ? value : null;
}

/**
 * Normalizes a publish dry run (DescribePublishChanges in
 * api/builder/changes.go): changes, or null when the server would refuse,
 * with warnings and the refusal as issues.
 */
export function readPublishPreview(response) {
  const data = response?.data || {};
  const changes = data.changes;
  const topology = configChange(changes?.topology);

  return {
    changes: topology
      ? {
          topology,
          experiment: configChange(changes.experiment),
          includes: namedEntries(changes.includes),
          scenarios: namedEntries(changes.scenarios),
          images: namedEntries(changes.images).map((image) => ({
            ...image,
            devices: (Array.isArray(image.devices) ? image.devices : []).filter(
              (device) => typeof device === 'string',
            ),
            onServer:
              typeof image.onServer === 'boolean' ? image.onServer : null,
          })),
          vlanAliases: namedEntries(changes.vlanAliases).map((alias) => ({
            ...alias,
            from: aliasValue(alias.from),
            to: aliasValue(alias.to),
          })),
        }
      : null,
    warningIssues: readIssues(data.warnings, 'warning'),
    errorIssues: readIssues(data.errors, 'error'),
  };
}

/** Creates an API client bound to an HTTP implementation. */
export function createBuilderApi(http = axiosInstance) {
  return {
    /**
     * Lists every draft on the server (all) and the published diagrams.
     * Drafts that the server cannot read now are listed separately, marked
     * damaged. They can only be deleted.
     */
    async listDrafts() {
      const response = await http.get(DRAFTS_PATH);
      const data = response.data || {};

      return {
        all: Array.isArray(data.drafts) ? data.drafts : [],
        published: Array.isArray(data.published) ? data.published : [],
        damaged: (Array.isArray(data.damaged) ? data.damaged : []).map(
          (draft) => ({ ...draft, damaged: true }),
        ),
      };
    },

    /**
     * Creates a draft from a complete document. forkOf names a draft to fork,
     * whose source and last publication the new draft takes.
     */
    async createDraft(request) {
      checkUploadSize(request);

      const response = await http.post(DRAFTS_PATH, request);

      return readEnvelope(response);
    },

    async getDraft(id) {
      const response = await http.get(draftPath(id));

      return readEnvelope(response);
    },

    async deleteDraft(id, etag) {
      await http.delete(draftPath(id), ifMatch(etag));

      return true;
    },

    /**
     * Deletes published, unchanged drafts and unused stored data. With dryRun,
     * the server only says what it would remove. A Warning header marks a
     * partial failure (incomplete).
     */
    async cleanUpStorage({ dryRun = false } = {}) {
      const response = await http.post(
        dryRun ? `${CLEANUP_PATH}?dryRun=true` : CLEANUP_PATH,
      );
      const data = response?.data || {};
      const headers = response?.headers;
      const warning =
        typeof headers?.get === 'function'
          ? headers.get('warning')
          : headers?.warning || headers?.Warning;
      const amount = (value) =>
        Number.isInteger(value) && value > 0 ? value : 0;

      return {
        drafts: Array.isArray(data.drafts)
          ? data.drafts.filter((id) => typeof id === 'string')
          : [],
        documents: amount(data.documents),
        content: amount(data.content),
        incomplete: Boolean(warning),
      };
    },

    async listSnapshots(id) {
      const response = await http.get(snapshotPath(id));

      return response.data?.snapshots || [];
    },

    /**
     * Appends one snapshot. The client never coalesces edits, so the history
     * the user undoes through is the history the server stores.
     */
    async appendSnapshot(id, payload, etag) {
      checkUploadSize(payload);

      const response = await http.post(
        snapshotPath(id),
        payload,
        ifMatch(etag),
      );

      return readEnvelope(response);
    },

    async getSnapshot(id, snapshotId) {
      const response = await http.get(snapshotPath(id, snapshotId));

      return readEnvelope(response);
    },

    /** Deletes one snapshot that is not the cursor's. The response is the draft with a new ETag. */
    async deleteSnapshot(id, snapshotId, etag) {
      const response = await http.delete(
        snapshotPath(id, snapshotId),
        ifMatch(etag),
      );

      return readEnvelope(response);
    },

    /** Moves the draft cursor (undo/redo) to a history index or snapshot id. */
    async moveCursor(id, cursor, etag) {
      const response = await http.patch(cursorPath(id), cursor, ifMatch(etag));

      return readEnvelope(response);
    },

    /** Publishes the cursor's snapshot. Only the intent is sent, and the server checks it again. */
    async publish(id, intent, etag) {
      let response;

      try {
        response = await http.post(
          publishPath(id),
          publishIntent(intent),
          ifMatch(etag),
        );
      } catch (error) {
        if (error?.response?.data?.status !== 'partial') {
          throw error;
        }

        response = error.response;
      }

      return { result: readPublishResult(response), etag: readETag(response) };
    },

    /**
     * A dry run of publish for the cursor's snapshot. It writes nothing, so it
     * sends no If-Match. A refusal comes back in errorIssues.
     */
    async previewPublish(id, intent) {
      const response = await http.post(publishPath(id), {
        ...publishIntent(intent),
        dryRun: true,
      });

      return readPublishPreview(response);
    },

    /**
     * Runs the named preflight checks on the cursor's snapshot against the
     * cluster. Nothing is written, and the ETag does not change.
     */
    async preflight(id, { checks, experiment = '' }) {
      const response = await http.post(
        preflightPath(id),
        experiment ? { checks, experiment } : { checks },
      );

      return readPreflightReport(response.data);
    },

    async getSources() {
      const response = await http.get(SOURCES_PATH);
      const data = response.data || {};

      return {
        images: data.images || [],
        topologies: data.topologies || [],
        scenarios: data.scenarios || [],
        experiments: data.experiments || [],
      };
    },

    /**
     * Builds a document from a stored or uploaded config. A topology can come
     * with its includes combined, or as a copy under newName. Each option is
     * sent only when chosen. source is the config read, never the copy.
     */
    async generate(request) {
      const payload =
        typeof request?.content === 'string'
          ? { content: request.content }
          : { source: `${request.kind}/${request.name}` };

      if (request?.includes) {
        payload.includes = request.includes;
      }

      if (request?.copy) {
        payload.copy = true;
      }

      if (request?.newName) {
        payload.name = request.newName;
      }

      checkUploadSize(payload);

      const response = await http.post(GENERATE_PATH, payload);
      const data = response.data || {};

      return {
        document: data.document || data,
        warnings: data.warnings || [],
        source: data.source || null,
      };
    },

    /**
     * Converts a legacy Builder diagram (its XML, or a Topology config with a
     * builder-xml annotation) into a document. Nothing is stored.
     */
    async convertLegacy({ content, name = '' } = {}) {
      const payload = name ? { content, name } : { content };

      checkUploadSize(payload, 'The legacy diagram');

      const response = await http.post(LEGACY_PATH, payload);
      const data = response.data || {};

      return {
        document: data.document,
        warnings: Array.isArray(data.warnings) ? data.warnings : [],
        source: data.source || null,
      };
    },

    /**
     * The Topology config a document publishes as, with its warnings and
     * publish blockers. Nothing is written. It sends the document, unsaved
     * edits included.
     */
    async exportTopology(document, name = '') {
      const payload = name ? { document, name } : { document };

      checkUploadSize(payload);

      const response = await http.post(EXPORT_TOPOLOGY_PATH, payload);
      const data = response.data || {};

      if (typeof data.yaml !== 'string') {
        throw new TypeError('The server sent an unexpected topology.');
      }

      const warningIssues = readIssues(data.warnings, 'warning');
      const publishBlockerIssues = readIssues(data.publishBlockers, 'error');

      return {
        name: data.name || name,
        yaml: data.yaml,
        warnings: warningIssues.map((entry) => entry.message),
        publishBlockers: publishBlockerIssues.map((entry) => entry.message),
        warningIssues,
        publishBlockerIssues,
      };
    },

    /**
     * The package of a document with the include sections, and warnings for what
     * it names but does not carry. Nothing is written.
     */
    async buildPackage(document, include = []) {
      const payload = { document, include };

      checkUploadSize(payload);

      const response = await http.post(PACKAGE_PATH, payload);
      const data = response.data || {};

      if (!data.package || typeof data.package !== 'object') {
        throw new TypeError('The server sent an unexpected package.');
      }

      return {
        package: data.package,
        warnings: Array.isArray(data.warnings) ? data.warnings : [],
      };
    },

    /** Asks which of a package's needs the server has, for readDependencies in package.js. */
    async resolvePackage(pkg) {
      checkUploadSize({ content: JSON.stringify(pkg) }, 'This package');

      const response = await http.post(RESOLVE_PACKAGE_PATH, pkg);

      return response.data || {};
    },

    /** Stores a new config, as the Configs page does (POST /configs). */
    async createConfig(config) {
      const payload = JSON.stringify(config);

      checkUploadSize({ content: payload }, `The ${config.kind}`);

      await http.post('configs', payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    },

    /** Replaces a stored config, as the Configs page does (PUT /configs/<kind>/<name>). */
    async updateConfig(config) {
      const payload = JSON.stringify(config);

      checkUploadSize({ content: payload }, `The ${config.kind}`);

      await http.put(configPath(config.kind, config.metadata.name), payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    },

    /** Reads a stored config whole (GET /configs/<kind>/<name>). */
    async getConfig(kind, name) {
      // The route refuses axios's default Accept, a list of types.
      const response = await http.get(configPath(kind, name), {
        headers: { Accept: 'application/json' },
      });
      const config = response.data;
      const metadata = config?.metadata;

      if (
        !config ||
        typeof config !== 'object' ||
        !metadata ||
        typeof metadata !== 'object' ||
        Array.isArray(metadata)
      ) {
        throw new TypeError(`The server sent an unexpected ${kind} config.`);
      }

      return config;
    },

    /** Reads a stored scenario's spec, to list its apps. A document does not hold it. */
    async getScenario(name) {
      // The route refuses axios's default Accept, a list of types.
      const response = await http.get(configPath('Scenario', name), {
        headers: { Accept: 'application/json' },
      });
      const spec = response.data?.spec;

      if (!spec || typeof spec !== 'object' || Array.isArray(spec)) {
        throw new TypeError('The server sent an unexpected scenario.');
      }

      return spec;
    },

    /** Reads whether an experiment is running. A failure means it is gone or not allowed. */
    async experimentState(name) {
      const response = await http.get(experimentPath(name));

      return { running: Boolean(response.data?.running) };
    },

    /**
     * Lists the published diagrams the user can see: stored ones, and those
     * read from a topology's Builder file (see fileHandle), which have no digest,
     * time or user. A row names its experiment while that exists.
     */
    async listDocuments() {
      const response = await http.get(DOCUMENTS_PATH);
      const documents = response.data?.documents;

      return (Array.isArray(documents) ? documents : []).map(listedDocument);
    },

    /**
     * Reads a topology's Builder document: its published one, or the file it
     * names, read on each request. topologyDiffers marks a stale stored topology.
     */
    async getTopologyDocument(name) {
      const response = await http.get(topologyDocumentPath(name));
      const data = response.data || {};

      return {
        ...listedDocument(data),
        topologyDiffers: data.topologyDiffers === true,
      };
    },

    async getDocument(id) {
      const response = await http.get(documentPath(id));
      const data = response.data || {};

      return data.document || data;
    },

    /** Deletes a published diagram's topology and its published diagrams. 412 when stale. */
    async deleteDocument(id, etag) {
      await http.delete(documentPath(id), ifMatch(etag));

      return true;
    },

    async getSchema() {
      const response = await http.get(SCHEMA_PATH);

      return response.data;
    },

    /**
     * Lists the icon library without images, with its limit and tag. A list
     * still at etag gives {notModified: true} (304).
     */
    async listIcons(etag = '') {
      const response = await http.get(ICONS_PATH, {
        ...(etag ? { headers: { 'If-None-Match': etag } } : {}),
        validateStatus: (status) =>
          (status >= 200 && status < 300) || status === 304,
      });

      if (response?.status === 304) {
        return { notModified: true };
      }

      const data = response?.data || {};
      const number = (value) => (Number.isFinite(value) ? value : 0);

      return {
        icons: (Array.isArray(data.icons) ? data.icons : [])
          .filter(
            (icon) =>
              typeof icon?.name === 'string' && typeof icon.id === 'string',
          )
          .map((icon) => ({
            ...icon,
            aliases: Array.isArray(icon.aliases)
              ? icon.aliases.filter((alias) => typeof alias === 'string')
              : [],
            canRename: icon.canRename === true,
            canDelete: icon.canDelete === true,
          })),
        maxIcons: number(data.maxIcons),
        etag: typeof data.etag === 'string' ? data.etag : '',
      };
    },

    /** Reads one icon of the icon library, with its PNG in base64. */
    async getIcon(name) {
      return (await http.get(iconPath(name))).data;
    },

    /**
     * Adds a PNG to the icon library. Use the icon in the response, as the server
     * can encode it again. The same bytes under the name give that icon, other
     * bytes a 409.
     */
    async uploadIcon({ name, data }) {
      const response = await http.post(ICONS_PATH, { name, data });

      return { icon: response.data, created: response.status === 201 };
    },

    /** Renames a library icon. The old name stays as an alias. */
    async renameIcon(name, newName) {
      const response = await http.put(iconPath(name), { name: newName });

      return response.data;
    },

    /** Deletes a library icon and its aliases. Nodes that name it show their built-in icon. */
    async deleteIcon(name, etag) {
      await http.delete(iconPath(name), ifMatch(etag));

      return true;
    },

    /**
     * Reads the server-wide template library, then the preloaded collections.
     * Built-in templates are listed apart. Built-in and preloaded items are read
     * only, always at version 1.
     */
    async listTemplates() {
      const data = (await http.get(TEMPLATES_PATH)).data || {};
      const list = (value) =>
        (Array.isArray(value) ? value : []).filter(
          (item) => typeof item?.id === 'string' && item.id !== '',
        );
      const object = (value) => (isPlainObject(value) ? value : {});
      const as = (source) => (item) => ({ ...item, source });
      // A server collection and its templates.
      const preloaded = (Array.isArray(data.preloaded) ? data.preloaded : [])
        .map((entry) => ({
          collection: list([object(entry).collection])[0],
          templates: list(object(entry).templates),
        }))
        .filter((entry) => entry.collection)
        .map(({ collection, templates }) => ({
          collection: as('preloaded')(collection),
          templates: templates.map(as('preloaded')),
        }));

      return {
        templates: [
          ...list(data.templates).map(as('library')),
          ...preloaded.flatMap((entry) => entry.templates),
        ],
        collections: [
          ...list(data.collections).map(as('library')),
          ...preloaded.map((entry) => entry.collection),
        ],
        builtins: list(data.builtins).map(as('builtin')),
        damaged: data.damaged === true,
        limits: object(data.limits),
      };
    },

    /** Adds templates, and optionally a new collection of exactly them, in one change. */
    async createTemplates({ templates, collection } = {}) {
      const response = await http.post(TEMPLATE_ITEMS_PATH, {
        templates,
        ...(collection ? { collection } : {}),
      });
      const data = response.data || {};

      return {
        created: Array.isArray(data.created) ? data.created : [],
        collection: data.collection || null,
      };
    },

    /** Replaces a library template. 412 when it is not at etag, 409 when it is read only. */
    async updateTemplate(id, content, etag) {
      const response = await http.put(
        templateItemPath(id),
        content,
        ifMatch(etag),
      );

      return response.data;
    },

    /** Adds a collection to the library: a named group of its templates. */
    async createTemplateCollection(content) {
      const response = await http.post(TEMPLATE_COLLECTIONS_PATH, content);

      return response.data;
    },

    /** Replaces a collection's name, description and templates. 412 when it is not at etag. */
    async updateTemplateCollection(id, content, etag) {
      const response = await http.put(
        templateCollectionPath(id),
        content,
        ifMatch(etag),
      );

      return response.data;
    },

    /**
     * Deletes templates and collections. One stale tag refuses all (412,
     * template.item.stale). Unknown ids are ignored, so a repeat does no harm.
     */
    async deleteTemplates({ templates = [], collections = [] } = {}) {
      const response = await http.post(TEMPLATE_DELETE_PATH, {
        ...(templates.length ? { templates } : {}),
        ...(collections.length ? { collections } : {}),
      });
      const deleted = response.data?.deleted || {};

      return {
        templates: Number(deleted.templates) || 0,
        collections: Number(deleted.collections) || 0,
      };
    },

    /**
     * Reads the shared layer set library, built-in sets first, with the server's
     * default for new diagrams and its tag, and the count of damaged sets.
     */
    async listLayerSets() {
      const data = (await http.get(LAYER_SETS_PATH)).data || {};

      return {
        layerSets: (Array.isArray(data.layerSets) ? data.layerSets : [])
          .map(readLayerSet)
          .filter(Boolean),
        ...readDefaultLayerSet({ id: data.defaultId, etag: data.defaultEtag }),
        damaged: Number.isInteger(data.damaged) ? data.damaged : 0,
      };
    },

    /** Adds a layer set under an ID the server gives it. */
    async createLayerSet(content) {
      const response = await http.post(LAYER_SETS_PATH, content);

      return readLayerSet({ ...response.data, etag: readETag(response) });
    },

    /** Replaces a layer set, which takes the next version. 412 when stale, 403 when built in. */
    async updateLayerSet(id, content, etag) {
      const response = await http.put(layerSetPath(id), content, ifMatch(etag));

      return readLayerSet({ ...response.data, etag: readETag(response) });
    },

    /** Deletes a layer set. Diagrams keep their copies, and a default naming it reads as none. */
    async deleteLayerSet(id, etag) {
      await http.delete(layerSetPath(id), ifMatch(etag));

      return true;
    },

    /** Sets or, with null, clears the default layer set of new diagrams. 412 when stale. */
    async setDefaultLayerSet(id, etag) {
      const response = await http.put(
        DEFAULT_LAYER_SET_PATH,
        { id: id || null },
        ifMatch(etag),
      );

      return readDefaultLayerSet(response.data || {});
    },

    /** Lists the disk image file names the server has, of every kind, as the role allows. */
    async listDisks() {
      const response = await http.get(DISKS_PATH);
      const disks = response.data?.disks;

      if (!Array.isArray(disks)) {
        throw new TypeError('The server sent an unexpected disk listing.');
      }

      return [
        ...new Set(
          disks
            .map((disk) => disk?.name)
            .filter((name) => typeof name === 'string' && name !== ''),
        ),
      ];
    },
  };
}

// Logs out as other pages do. The first logout sets auth to false, so the
// refusals of other requests do nothing.
function logOut() {
  const phenix = usePhenixStore();

  if (phenix.auth) {
    phenix.logout();
  }
}

/** An HTTP client that logs out when the server refuses the session (401). */
export function watchSession(http, ended = logOut) {
  const method =
    (name) =>
    (...args) =>
      http[name](...args).catch((error) => {
        if (error?.response?.status === 401) {
          ended(error);
        }

        throw error;
      });

  return {
    get: method('get'),
    post: method('post'),
    put: method('put'),
    patch: method('patch'),
    delete: method('delete'),
  };
}

// How long the Builder waits for the server to answer a request, in
// milliseconds, by kind of request (see requestTimeout).
export const REQUEST_TIMEOUTS = Object.freeze({
  read: 30_000,
  write: 60_000,
  long: 600_000,
});

// The kind of each Builder route, by its path without the query. The routes
// listed are server operations that can take minutes. Every other route is
// a read for GET and a write for the other methods: snapshot appends, cursor
// moves, deletes and library changes.
const ROUTE_TIMEOUTS = [
  // Publish, and its dry run that previews the changes.
  [/^builder\/drafts\/[^/]+\/publish$/, 'long'],
  [/^builder\/drafts\/[^/]+\/preflight$/, 'long'],
  // Import from a config or a legacy diagram, and the topology export.
  [/^builder\/(generate|legacy|export\/topology)$/, 'long'],
  [/^builder\/package(\/resolve)?$/, 'long'],
  [/^builder\/cleanup$/, 'long'],
];

/** How long the Builder waits for an answer, in ms (see REQUEST_TIMEOUTS, ROUTE_TIMEOUTS). */
export function requestTimeout(method, path) {
  const route = String(path).split('?')[0];
  const listed = ROUTE_TIMEOUTS.find(([pattern]) => pattern.test(route));

  if (listed) {
    return REQUEST_TIMEOUTS[listed[1]];
  }

  return method === 'get' ? REQUEST_TIMEOUTS.read : REQUEST_TIMEOUTS.write;
}

/**
 * An HTTP client whose requests abort with ETIMEDOUT after requestTimeout(),
 * so a request that never settles cannot stop the one-at-a-time save queue.
 */
export function withTimeouts(http) {
  const method =
    (name, withBody) =>
    (url, ...rest) => {
      const limit = requestTimeout(name, url);
      const controller = new AbortController();
      const args = withBody ? [url, rest[0]] : [url];
      const config = withBody ? rest[1] : rest[0];

      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          controller.abort();
          reject(
            Object.assign(
              new Error(
                `The server did not answer within ${limit / 1000} seconds.`,
              ),
              { code: 'ETIMEDOUT' },
            ),
          );
        }, limit);

        http[name](...args, { ...config, signal: controller.signal })
          .then(resolve, reject)
          .finally(() => clearTimeout(timer));
      });
    };

  return {
    get: method('get', false),
    post: method('post', true),
    put: method('put', true),
    patch: method('patch', true),
    delete: method('delete', false),
  };
}

export const builderApi = createBuilderApi(
  watchSession(withTimeouts(axiosInstance)),
);
