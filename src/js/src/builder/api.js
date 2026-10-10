// Builder API client.
//
// The path builders are exported separately from the client, so unit tests
// can test routes without HTTP, and the autosave queue can use the same
// paths. All requests are relative to the app's /api/v1/ base (axios
// instance).
//
// The draft record is versioned. Every mutation carries the ETag that the
// client last observed as `If-Match`. The server rejects a stale write, so
// one editor cannot overwrite another without notice. Intentionally, no call
// here has a "force" variant. A draft's share list has its own tag
// ("shares-N"), which its updates carry in place of the ETag.

import axiosInstance from '@/utils/axios.js';

import { count } from './announce.js';
import { BulkError } from './bulk.js';
import { MAX_DOCUMENT_BYTES, MAX_SHARES } from './limits.js';
import { sessionEnded } from './signin.js';
import { hasControlCharacters, utf8Length } from './text.js';

export const DRAFTS_PATH = 'builder/drafts';
export const SOURCES_PATH = 'builder/sources';
export const GENERATE_PATH = 'builder/generate';
export const LEGACY_PATH = 'builder/legacy';
export const EXPORT_TOPOLOGY_PATH = 'builder/export/topology';
// Builder packages (web/builder_package.go): one is made of a document, and
// one is checked against what this server has.
export const PACKAGE_PATH = 'builder/package';
export const RESOLVE_PACKAGE_PATH = 'builder/package/resolve';
export const DOCUMENTS_PATH = 'builder/documents';
// The server's icon library, which every user shares (web/builder_icons.go).
export const ICONS_PATH = 'builder/icons';
// The template library the caller can use (web/builder_templates.go).
export const TEMPLATES_PATH = 'builder/templates';
export const SCHEMA_PATH = 'schemas/builder/v1';
// Disk images, the same listing the Disks page reads (web/disk.go GetDisks).
export const DISKS_PATH = 'disks';

/**
 * A stored config, as the Configs page reads it (web/config.go GetConfig).
 *
 * @param {string} kind config kind, such as Scenario
 * @param {string} name config name
 * @returns {string}
 */
export function configPath(kind, name) {
  return `configs/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`;
}

/**
 * An experiment, as its own page reads it (web/handlers.go GetExperiment).
 *
 * @param {string} name experiment name
 * @returns {string}
 */
export function experimentPath(name) {
  return `experiments/${encodeURIComponent(name)}`;
}

/**
 * @param {string} owner draft owner (username)
 * @param {string} id draft id
 * @returns {string}
 */
export function draftPath(owner, id) {
  return `${DRAFTS_PATH}/${encodeURIComponent(owner)}/${encodeURIComponent(id)}`;
}

/**
 * @param {string} owner
 * @param {string} id
 * @param {string} [snapshotId]
 * @returns {string}
 */
export function snapshotPath(owner, id, snapshotId) {
  const base = `${draftPath(owner, id)}/snapshots`;
  return snapshotId ? `${base}/${encodeURIComponent(snapshotId)}` : base;
}

/**
 * @param {string} owner
 * @param {string} id
 * @returns {string}
 */
export function cursorPath(owner, id) {
  return `${draftPath(owner, id)}/cursor`;
}

/**
 * @param {string} owner
 * @param {string} id
 * @returns {string}
 */
export function publishPath(owner, id) {
  return `${draftPath(owner, id)}/publish`;
}

/**
 * @param {string} owner
 * @param {string} id
 * @returns {string} where the draft's preflight checks are run
 */
export function preflightPath(owner, id) {
  return `${draftPath(owner, id)}/preflight`;
}

/**
 * @param {string} owner
 * @param {string} id
 * @returns {string} the draft's share list, which only its owner reads
 */
export function sharesPath(owner, id) {
  return `${draftPath(owner, id)}/shares`;
}

/**
 * @param {string} owner
 * @param {string} id
 * @returns {string} the users the draft's owner may share it with
 */
export function shareCandidatesPath(owner, id) {
  return `${sharesPath(owner, id)}/candidates`;
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

/**
 * @param {string} owner the user whose template library it is
 * @returns {string} where templates are added to that library
 */
export function templateItemsPath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/items`;
}

/**
 * @param {string} owner
 * @param {string} id template id
 * @returns {string} one template of that library
 */
export function templateItemPath(owner, id) {
  return `${templateItemsPath(owner)}/${encodeURIComponent(id)}`;
}

/**
 * @param {string} owner
 * @returns {string} where collections are added to that library
 */
export function templateCollectionsPath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/collections`;
}

/**
 * @param {string} owner
 * @param {string} id collection id
 * @returns {string} one collection of that library
 */
export function templateCollectionPath(owner, id) {
  return `${templateCollectionsPath(owner)}/${encodeURIComponent(id)}`;
}

/**
 * @param {string} owner
 * @returns {string} where templates and collections of that library are
 *   deleted
 */
export function templateDeletePath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/delete`;
}

/**
 * @param {string} owner
 * @returns {string} where deleted built-in templates of that library are
 *   restored
 */
export function templateRestorePath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/restore`;
}

// The users the caller may share the templates of their library with.
export const TEMPLATE_CANDIDATES_PATH = `${TEMPLATES_PATH}/candidates`;

/**
 * @param {string} owner
 * @returns {string} where people are added to and removed from templates
 *   and collections of that library
 */
export function templateSharePath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/share`;
}

/**
 * @param {string} owner
 * @returns {string} where templates and collections of that library are
 *   published server-wide, or taken back
 */
export function templatePublishPath(owner) {
  return `${TEMPLATES_PATH}/${encodeURIComponent(owner)}/publish`;
}

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
 * The name of an uploaded file as a new draft records it (`sourceFile`). ''
 * for a name that would cause the server to refuse the draft
 * (ValidateSourceFile in api/builder): a name that is not a plain file name,
 * is too long, or holds a control character. The name is only shown. Thus a
 * name that cannot be recorded is left out, and the upload continues without
 * it.
 *
 * @param {string} [name] the chosen file's name
 * @returns {string}
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
 * Reads the draft ETag from a response. It reads the body first, which
 * carries the ETag in every draft envelope (and in a publish result's
 * draft). If not there, it reads the ETag header, and accepts header bags and
 * Maps. The body comes first because a compressing proxy rewrites the header
 * (nginx changes "3" into the weak W/"3", Apache into "3-gzip"), and the
 * server refuses such a tag in If-Match.
 *
 * @param {object} response axios-like response
 * @returns {string|null}
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
 * The draft's current ETag, from the header of the refusal (412) of a
 * request sent with an older ETag, so that a delete can be sent again with
 * it. The body of the refusal does not carry it, and a damaged draft cannot
 * be read for it. A tag that a compressing proxy rewrote (see readETag) is
 * not the draft's tag. Thus no tag is given, and the caller reads the draft
 * instead.
 *
 * @param {object} error axios-like error
 * @returns {string|null}
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
 * An upload that the server would refuse for its size, refused before it is
 * sent. The server refuses such a body before it reads all of it. Some
 * browsers (Firefox) then report the reset connection and not the 413, which
 * would look like an unreachable server.
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

/**
 * Refuses an upload that the server would refuse for its size: a document or
 * the text of a file over MAX_DOCUMENT_BYTES, or a body over
 * MAX_REQUEST_BYTES.
 *
 * @param {object} payload request body
 * @param {string} [content] what the payload's `content` is, as a sentence
 *   subject
 * @throws {TooLargeError}
 */
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

/**
 * Classifies an API failure so the UI can show a specific state.
 *
 * @param {object} error axios-like error
 * @returns {'conflict'|'unauthenticated'|'forbidden'|'missing'|'offline'|'too-large'|'invalid'|'error'}
 */
export function classifyError(error) {
  if (error instanceof TooLargeError) {
    return 'too-large';
  }

  const status = error?.response?.status;

  if (status === 409 || status === 412 || status === 428) {
    return 'conflict';
  }

  // The session ended (an expired or revoked token): signing in again
  // fixes it, unlike a refusal of this user.
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

/**
 * Whether a failure ends a bulk action (runBulk's `stop` in bulk.js). After
 * the session ends or the server becomes unreachable, the remaining items
 * would fail in the same way. A failure that is already in words (BulkError),
 * for which no request was sent, never ends the action.
 *
 * @param {*} error
 * @returns {boolean}
 */
export function endsBulk(error) {
  return (
    !(error instanceof BulkError) &&
    ['unauthenticated', 'offline'].includes(classifyError(error))
  );
}

// An id in a server message. A draft is named by its owner and its id
// ("draft alice/<id> not found"). The owner goes with the id, because
// "alice/" alone would look like a name that was cut short. An id inside a
// longer path keeps the part before it.
const UUID_PATTERN =
  /(?:(?:^|\s+)[^\s/]+\/|\s*)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;

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
 * The reason that a server gave for refusing a request, in words that a user
 * can act on, as a sentence: capitalized and ending in a full stop. Server
 * errors carry a `message` and a `cause`. Most messages tell what is wrong
 * ("config core already exists; choose update explicitly") and are used
 * unchanged. A generic message, which only names the refused operation
 * ("unable to save builder draft <id>"), is replaced by the first issue of
 * the cause chain ("builder: invalid request: document: ...:
 * nodes[1].device.hostname: duplicate hostname "node-2" (also nodes[0])"),
 * without its prefixes, document paths, array indexes and ids. Ids are never
 * shown.
 *
 * @param {object} [error] axios-like error
 * @returns {string} the reason, or '' when the server gave none
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

  // Issues are separated by "; ", and schema errors by " | ". These can
  // repeat each other.
  const [reason, ...others] = new Set(
    cause
      .split(/; | \| /)
      .filter((issue) => issue.trim())
      .map(causeReason),
  );
  const more = others.length;

  return sentence(
    more > 0 ? `${reason} (and ${count(more, 'more problem')})` : reason,
  );
}

/**
 * The code of the failure that a server named for a refused request: a
 * Builder error's stable, machine-readable name (document.invalid,
 * publish.topology.exists). codes.go lists all of them.
 *
 * @param {object} [error] axios-like error
 * @returns {string} the code, or '' when the server gave none
 */
export function serverCode(error) {
  const code = error?.response?.data?.code;

  return typeof code === 'string' ? code : '';
}

/**
 * Issues as the server reports them (BuilderIssue in openapi.yml). Each has
 * its code, its severity and its message, and its location when known. A
 * plain message, which a server of an earlier release sent, is an issue with
 * no code.
 *
 * @param {Array} [list] the issues the server sent
 * @param {'error'|'warning'} [severity] the severity of an issue that says
 *   none
 * @returns {{code: string, severity: string, message: string, path?: string,
 *   nodeId?: string, edgeId?: string, networkId?: string, field?: string}[]}
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
 * A preflight report as the server sends it (BuilderPreflightReport in
 * openapi.yml). It has each check in the requested order, with its status,
 * its summary and its issues (see readIssues). An issue that states no
 * severity is a warning. It also has the names of the checks by status, in
 * the same order. A check with a status that this client does not know
 * counts as unavailable.
 *
 * @param {object} [data] the body of the response
 * @returns {{checks: {name: string, status: string, summary: string,
 *   issues: object[]}[], passed: string[], failed: string[],
 *   unavailable: string[]}}
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

/**
 * A refused publish in the form that publishRefusal() takes: the server's
 * code, its reason (see serverReason()), and the scenario that the refusal
 * names, when it is about one of the scenarios in the draft's metadata.
 *
 * @param {object} [error] axios-like error
 * @returns {{code: string, reason: string, scenario: string}}
 */
export function serverRefusal(error) {
  const scenario = error?.response?.data?.metadata?.scenario;

  return {
    code: serverCode(error),
    reason: serverReason(error),
    scenario: typeof scenario === 'string' ? scenario : '',
  };
}

/**
 * What the server said about a refused request, word for word, as a
 * sentence. Use it for responses written to be shown unchanged. For example,
 * the reason that a topology's Builder file cannot be used names its path,
 * and serverReason() would remove an id from that path.
 *
 * @param {object} [error] axios-like error
 * @returns {string} the message, or '' when the server gave none
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

/**
 * Human readable message for a failure class.
 *
 * @param {string} kind result of classifyError
 * @param {object} [error]
 * @returns {string}
 */
export function errorMessage(kind, error) {
  const detail = serverReason(error);

  switch (kind) {
    case 'conflict':
      return 'This draft changed on the server since you loaded it.';
    case 'unauthenticated':
      return 'Your session has ended. Sign in again to continue.';
    case 'forbidden':
      return detail || 'You are not allowed to change this draft.';
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
      return 'The server could not be reached. Check the connection and try again.';
    default:
      return detail || 'The server rejected the request.';
  }
}

/**
 * Why a request to the template library failed. The server writes its
 * refusals of a library change to be shown ("a library holds at most 200
 * templates"), so this function uses its reason, as a sentence. For a session
 * that ended and a server that cannot be reached, it gives the same message
 * as everywhere else. The messages of errorMessage for the other cases are
 * about a draft.
 *
 * @param {object} error axios-like error
 * @returns {string}
 */
export function libraryErrorMessage(error) {
  const kind = classifyError(error);

  if (['unauthenticated', 'offline'].includes(kind)) {
    return errorMessage(kind, error);
  }

  return (
    serverReason(error) ||
    (kind === 'forbidden'
      ? 'Your role does not allow it.'
      : 'The server rejected the request.')
  );
}

/**
 * Why the experiment that a diagram was published with could not be opened.
 * For a name that it does not have, the server gives its own error. Thus
 * any failure, except a refusal of the user or of the session or an
 * unreachable server, means that the experiment is gone.
 *
 * @param {string} name the experiment's name
 * @param {object} error axios-like error
 * @returns {string}
 */
export function experimentError(name, error) {
  const kind = classifyError(error);
  const reasons = {
    forbidden: 'Your role does not allow it.',
    offline: errorMessage('offline'),
    unauthenticated: errorMessage('unauthenticated'),
  };

  return `Could not open experiment ${name}. ${
    reasons[kind] || 'It may have been deleted or renamed.'
  }`;
}

function ifMatch(etag) {
  return etag ? { headers: { 'If-Match': etag } } : {};
}

/**
 * Normalizes a share list response. Its tag ("shares-3") belongs to the
 * share list, separate from the draft's ETag. It is read from the body first
 * for the reason that readETag gives.
 *
 * @param {object} response axios-like response
 * @returns {{shares: object[], sharesEtag: string|null, maxShares: number}}
 */
export function readShares(response) {
  const data = response?.data || {};
  const headers = response?.headers;
  const header =
    typeof headers?.get === 'function'
      ? headers.get('etag')
      : headers?.etag || headers?.ETag;
  const sharesEtag =
    typeof data.sharesEtag === 'string' && data.sharesEtag !== ''
      ? data.sharesEtag
      : header || null;

  return {
    shares: (Array.isArray(data.shares) ? data.shares : [])
      .filter((entry) => typeof entry?.user === 'string' && entry.user)
      .map((entry) => ({
        user: entry.user,
        access: entry.access === 'edit' ? 'edit' : 'view',
        stale: entry.stale === true,
        grantedAt: entry.grantedAt || '',
      })),
    sharesEtag,
    maxShares: Number.isInteger(data.maxShares) ? data.maxShares : MAX_SHARES,
  };
}

/**
 * The people a share list update refused, and why, from its 422 answer.
 *
 * @param {object} error axios-like error
 * @returns {{user: string, reason: string}[]} none for any other failure
 */
export function shareErrors(error) {
  const errors = error?.response?.data?.errors;

  if (error?.response?.status !== 422 || !Array.isArray(errors)) {
    return [];
  }

  return errors
    .filter((entry) => typeof entry?.reason === 'string' && entry.reason)
    .map((entry) => ({
      user: typeof entry.user === 'string' ? entry.user : '',
      reason: entry.reason,
    }));
}

/**
 * The users that a share can name, from a listing of them: a draft's
 * listing or the template library's listing.
 *
 * @param {object} response axios-like response
 * @returns {{username: string, name: string}[]} name may be ''
 */
export function readCandidates(response) {
  const users = response?.data?.users;

  if (!Array.isArray(users)) {
    throw new TypeError('The server sent an unexpected user listing.');
  }

  return users
    .filter((user) => typeof user?.username === 'string' && user.username)
    .map((user) => ({
      username: user.username,
      name: typeof user.name === 'string' ? user.name.trim() : '',
    }));
}

/**
 * The result of a change to who can use templates and collections: the
 * items that it did not change, and why ('not-found', 'too-many').
 *
 * @param {object} response axios-like response
 * @returns {{failed: {kind: string, id: string, reason: string}[]}}
 */
export function readLibraryResult(response) {
  const failed = response?.data?.failed;

  return {
    failed: (Array.isArray(failed) ? failed : [])
      .filter((entry) => typeof entry?.id === 'string' && entry.id)
      .map((entry) => ({
        kind: entry.kind === 'collection' ? 'collection' : 'template',
        id: entry.id,
        reason: typeof entry.reason === 'string' ? entry.reason : '',
      })),
  };
}

/**
 * Normalizes a draft envelope: metadata, the current document and the history
 * index that the server holds. Only a read of the draft carries its history.
 * A save responds with only the draft, and its history is then null
 * (unknown), not empty.
 *
 * @param {object} response axios-like response
 * @returns {{draft: object, document: object|null, history: object[]|null, cursor: number, etag: string|null}}
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
 * Builds the publish request body. Publish is an *intent*. The server loads
 * the snapshot that the draft cursor points at and runs its own checks
 * again, so document bytes are never sent here. Anything that is not part of
 * the intent is dropped, not forwarded. The experiment's scenario is sent
 * only by name, and only with an experiment. The server adds the topology to
 * every scenario that the document lists, whatever the request says.
 *
 * @param {object} intent mode, topology, scenario ({name}), experiment
 * @returns {object} request body
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
    throw new Error('A topology name and action are required to publish.');
  }

  const body = { mode, topology };

  if (mode === 'topology-experiment') {
    const experiment = target(intent.experiment);

    if (!experiment) {
      throw new Error('An experiment name and action are required.');
    }

    if (name(intent.scenario)) {
      body.scenario = { name: name(intent.scenario) };
    }

    body.experiment = experiment;
  }

  return body;
}

/**
 * Normalizes a publish response. The server reports results per stage, so a
 * partial failure (topology written, experiment refused) shows as a failure,
 * not as a plain success.
 *
 * @param {object} response axios response
 * @returns {object} result
 */
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
  // names say, and the Publish dialog lists them.
  const warningIssues = readIssues(data.warnings, 'warning');
  const errorIssues = readIssues(data.errors, 'error');

  return {
    status,
    partial: status === 'partial',
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
 * Normalizes the response of a dry run of a publication (see
 * DescribePublishChanges in api/builder/changes.go). It gives what
 * publishing would change, or null when the server would refuse it. It also
 * gives the warnings and the refusal of the response, each an issue (see
 * readIssues).
 *
 * @param {object} response axios-like response
 * @returns {{changes: object|null, warningIssues: object[],
 *   errorIssues: object[]}} changes: topology and experiment ({name,
 *   action}, experiment null without one), includes, scenarios, images
 *   (devices, and onServer true, false or null when unknown) and
 *   vlanAliases (from and to, null for none)
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

/**
 * Creates an API client bound to an HTTP implementation.
 *
 * @param {object} [http] axios-compatible client
 * @returns {object} client
 */
export function createBuilderApi(http = axiosInstance) {
  return {
    /**
     * Lists the drafts that the user can see: their own (mine), drafts
     * shared with them (shared), and other users' drafts that their role
     * lets them see (others). Drafts that the server cannot read now are
     * listed separately, marked damaged. The user can only delete them. A
     * draft that the user can delete says so (`canDelete`).
     */
    async listDrafts() {
      const response = await http.get(DRAFTS_PATH);
      const data = response.data || {};
      const visible = Array.isArray(data.shared) ? data.shared : [];

      return {
        mine: data.drafts || data.mine || [],
        shared: visible.filter((draft) => draft?.via === 'share'),
        others: visible.filter((draft) => draft?.via !== 'share'),
        published: data.published || [],
        damaged: (Array.isArray(data.damaged) ? data.damaged : []).map(
          (draft) => ({ ...draft, damaged: true }),
        ),
      };
    },

    /**
     * Creates a draft from a complete document.
     *
     * @param {{owner?: string, title?: string, sourceToken?: string,
     *   sourceFile?: string, forkOf?: string, document: object,
     *   summary?: string}} request
     *   forkOf: "<owner>/<draft id>" of a draft that the new one forks. The
     *   new draft takes its source and last publication in place of
     *   sourceToken.
     *   sourceFile: the name of the uploaded file that the document was read
     *   from.
     */
    async createDraft(request) {
      checkUploadSize(request);

      const response = await http.post(DRAFTS_PATH, request);

      return readEnvelope(response);
    },

    async getDraft(owner, id) {
      const response = await http.get(draftPath(owner, id));

      return readEnvelope(response);
    },

    async deleteDraft(owner, id, etag) {
      await http.delete(draftPath(owner, id), ifMatch(etag));

      return true;
    },

    /**
     * Reads who a draft is shared with. Only its owner may.
     *
     * @param {string} owner
     * @param {string} id
     * @returns {Promise<{shares: object[], sharesEtag: string|null,
     *   maxShares: number}>}
     */
    async getShares(owner, id) {
      return readShares(await http.get(sharesPath(owner, id)));
    },

    /**
     * Replaces the list of people that a draft is shared with, if the list
     * did not change since sharesEtag was read. The response also carries
     * the draft, whose ETag the change changed.
     *
     * @param {string} owner
     * @param {string} id
     * @param {{user: string, access: string}[]} shares the whole new list
     * @param {string} sharesEtag the share list's tag, as last read
     * @returns {Promise<object>} readShares() with the draft and its ETag
     */
    async updateShares(owner, id, shares, sharesEtag) {
      const response = await http.put(
        sharesPath(owner, id),
        { shares: shares.map(({ user, access }) => ({ user, access })) },
        ifMatch(sharesEtag),
      );

      return {
        ...readShares(response),
        draft: response.data?.draft || null,
        // The draft's, from its body: the ETag header is the share list's.
        etag: readETag({ data: response.data }),
      };
    },

    /**
     * Lists the users that the draft's owner can share it with, by username,
     * including the users that it is already shared with.
     *
     * @param {string} owner
     * @param {string} id
     * @returns {Promise<{username: string, name: string}[]>} name may be ''
     */
    async listShareCandidates(owner, id) {
      return readCandidates(await http.get(shareCandidatesPath(owner, id)));
    },

    async listSnapshots(owner, id) {
      const response = await http.get(snapshotPath(owner, id));

      return response.data?.history || response.data?.snapshots || [];
    },

    /**
     * Appends one snapshot. Every semantic edit is its own snapshot. The
     * client never coalesces commits, because the history that the user can
     * undo through must be the history that the server stores.
     *
     * @param {string} owner
     * @param {string} id
     * @param {{document: object, summary?: string, opId?: string}} payload
     *   opId: the client's id for this save, recorded with the snapshot
     * @param {string} etag observed draft ETag
     */
    async appendSnapshot(owner, id, payload, etag) {
      checkUploadSize(payload);

      const response = await http.post(
        snapshotPath(owner, id),
        payload,
        ifMatch(etag),
      );

      return readEnvelope(response);
    },

    async getSnapshot(owner, id, snapshotId) {
      const response = await http.get(snapshotPath(owner, id, snapshotId));

      return readEnvelope(response);
    },

    /**
     * Deletes one snapshot from the draft's history. The current snapshot
     * (the cursor's) cannot be deleted. The response is the draft, as for a
     * save, with its new ETag.
     *
     * @param {string} owner
     * @param {string} id
     * @param {string} snapshotId
     * @param {string} etag observed draft ETag
     */
    async deleteSnapshot(owner, id, snapshotId, etag) {
      const response = await http.delete(
        snapshotPath(owner, id, snapshotId),
        ifMatch(etag),
      );

      return readEnvelope(response);
    },

    /**
     * Moves the draft cursor (undo/redo) to a history index or snapshot id.
     *
     * @param {string} owner
     * @param {string} id
     * @param {{index?: number, snapshotId?: string}} cursor
     * @param {string} etag
     */
    async moveCursor(owner, id, cursor, etag) {
      const response = await http.patch(
        cursorPath(owner, id),
        cursor,
        ifMatch(etag),
      );

      return readEnvelope(response);
    },

    /**
     * Publishes the snapshot that the draft cursor points at. Only the intent
     * is sent. The server owns the document bytes and validates them again.
     *
     * @param {string} owner
     * @param {string} id
     * @param {object} intent mode, topology, scenario, experiment
     * @param {string} etag last observed draft ETag
     */
    async publish(owner, id, intent, etag) {
      let response;

      try {
        response = await http.post(
          publishPath(owner, id),
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
     * Asks what publishing the snapshot that the draft cursor points at with
     * this intent would change. This is a dry run of the publish route,
     * which writes nothing and thus needs no If-Match. For a publication that
     * the server would refuse, the response has no changes and the refusal
     * in errorIssues.
     *
     * @param {string} owner
     * @param {string} id
     * @param {object} intent mode, topology, scenario, experiment
     * @returns {Promise<object>} readPublishPreview()
     */
    async previewPublish(owner, id, intent) {
      const response = await http.post(publishPath(owner, id), {
        ...publishIntent(intent),
        dryRun: true,
      });

      return readPublishPreview(response);
    },

    /**
     * Runs the named preflight checks on the snapshot that the draft cursor
     * points at. The server runs them against its cluster and itself.
     * Nothing is written, and the draft's ETag does not change.
     *
     * @param {string} owner
     * @param {string} id
     * @param {{checks: string[], experiment?: string}} request checks: ids
     *   of the checks (capacity, network, disks, apps), in the order of the
     *   report. experiment: the experiment whose VLAN range and default
     *   bridge the network check uses, sent only when named.
     * @returns {Promise<object>} see readPreflightReport
     */
    async preflight(owner, id, { checks, experiment = '' }) {
      const response = await http.post(
        preflightPath(owner, id),
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
     * Asks the server to build a builder document from a stored config, or
     * from an uploaded config given as JSON or YAML text. A topology can be
     * imported with its included topologies combined into it, or as a copy.
     * The document is then linked to no config and has a new name. Each of
     * the three options is sent only when the user chooses it.
     *
     * @param {{kind?: string, name?: string, content?: string,
     *   includes?: 'keep'|'combine', copy?: boolean, newName?: string}} request
     *   kind and name of a stored config, or content. newName: the new
     *   topology's name, with copy or includes 'combine'.
     * @returns {Promise<{document: object, warnings: string[], source: object|null}>}
     *   source as GET /builder/sources describes it (`stored` false for an
     *   upload): the config that was read, never the copy.
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
     * Asks the server to convert a diagram of the legacy Builder into a
     * builder document. Nothing is stored.
     *
     * @param {{content: string, name?: string}} request content: the text
     *   of the file, which is the diagram's XML or a Topology config (JSON
     *   or YAML) that holds one in its builder-xml annotation. name: the
     *   document's name for a diagram that comes without a topology.
     * @returns {Promise<{document: object, warnings: string[], source: object|null}>}
     *   source as generate() gives it for a Topology config, and null for a
     *   diagram alone.
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
     * Asks the server for the phenix Topology config that a document
     * publishes as, which Publish would write. Nothing is written. The
     * request sends the document, so it holds edits that are not saved yet.
     *
     * @param {object} document
     * @param {string} [name] the topology's name. Without one, the server
     *   names it as the Publish dialog proposes.
     * @returns {Promise<{name: string, yaml: string, warnings: string[],
     *   publishBlockers: string[], warningIssues: object[],
     *   publishBlockerIssues: object[]}>} the config as YAML, what the
     *   projection left out or changed, and why publishing it would be
     *   refused. Each is given as text and as issues with their codes (see
     *   readIssues).
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
     * Asks the server for the package of a document: the document, the
     * sections that include names, and the list of what the diagram needs.
     * Nothing is written. The request sends the document, so it holds edits
     * that are not saved yet.
     *
     * @param {object} document
     * @param {string[]} [include] the sections that the package carries:
     *   scenarios, topologies, icons and images
     * @returns {Promise<{package: object, warnings: Array<object|string>}>}
     *   warnings: what the package names but does not carry, or leaves out
     *   of its requirements, and why. Each is an issue with its code (see
     *   toIssue in issues.js).
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

    /**
     * Asks the server which of what a package's diagram needs it has.
     * Nothing is written.
     *
     * @param {object} pkg the package, decoded
     * @returns {Promise<object>} the answer, {dependencies}, which
     *   readDependencies in package.js reads
     */
    async resolvePackage(pkg) {
      checkUploadSize({ content: JSON.stringify(pkg) }, 'This package');

      const response = await http.post(RESOLVE_PACKAGE_PATH, pkg);

      return response.data || {};
    },

    /**
     * Stores a new config, as the Configs page does (POST /configs). The
     * server checks `configs` `create` for its kind and name, and the
     * config itself.
     *
     * @param {object} config apiVersion, kind, metadata and spec
     * @returns {Promise<void>}
     */
    async createConfig(config) {
      const payload = JSON.stringify(config);

      checkUploadSize({ content: payload }, `The ${config.kind}`);

      await http.post('configs', payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    },

    /**
     * Replaces a stored config of the same kind and name, as the Configs
     * page does (PUT /configs/<kind>/<name>), which needs `configs`
     * `update` on it.
     *
     * @param {object} config apiVersion, kind, metadata and spec
     * @returns {Promise<void>}
     */
    async updateConfig(config) {
      const payload = JSON.stringify(config);

      checkUploadSize({ content: payload }, `The ${config.kind}`);

      await http.put(configPath(config.kind, config.metadata.name), payload, {
        headers: { 'Content-Type': 'application/json' },
      });
    },

    /**
     * Reads a stored config whole, metadata and annotations included (GET
     * /configs/<kind>/<name>), which needs `configs` `get` on it.
     *
     * @param {string} kind
     * @param {string} name
     * @returns {Promise<object>} apiVersion, kind, metadata, spec
     */
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

    /**
     * Reads a stored scenario's spec, to list its apps. A document that
     * lists the scenario does not hold the spec.
     *
     * @param {string} name
     * @returns {Promise<object>} the spec, upgraded to the latest version
     */
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

    /**
     * Reads whether an experiment is running, which its page asks for
     * itself when it is not told. A failure says the experiment cannot be
     * opened: it is gone, or the user may not get it.
     *
     * @param {string} name
     * @returns {Promise<{running: boolean}>}
     */
    async experimentState(name) {
      const response = await http.get(experimentPath(name));

      return { running: Boolean(response.data?.running) };
    },

    /**
     * Lists the published diagrams that the user can see:
     *   - each topology's current published document (source "store")
     *   - each topology whose diagram is read from the Builder file that it
     *     names (source "file"). Such a row has a path and a handle as its id
     *     (see fileHandle). It has no digest, time or user, because the
     *     listing reads no file.
     * A published document's row names the experiment that its publication
     * made (`experiment`), while that experiment exists and the user can get
     * it.
     */
    async listDocuments() {
      const response = await http.get(DOCUMENTS_PATH);
      const documents = response.data?.documents;

      return (Array.isArray(documents) ? documents : []).map(listedDocument);
    },

    /**
     * Reads the Builder document that a topology references, from where it
     * is kept: its published document, or the Builder file that it names,
     * which the server reads on every request.
     *
     * @param {string} name topology name
     * @returns {Promise<object>} the listing row, with `digest`, `size` and
     *   `document`, and for a file `topologyDiffers`: whether the stored
     *   topology is not what the file's document publishes
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

    /**
     * Deletes the topology a published diagram is current for, and the
     * topology's published diagrams with it.
     *
     * @param {string} id published document id
     */
    async deleteDocument(id) {
      await http.delete(documentPath(id));

      return true;
    },

    async getSchema() {
      const response = await http.get(SCHEMA_PATH);

      return response.data;
    },

    /**
     * Lists every icon of the server's icon library, each with its PNG in
     * base64, its uploader, its aliases and whether the user can rename or
     * delete it. Also lists the user's uploads against what each user can
     * upload.
     *
     * @returns {Promise<{icons: object[], maxIcons: number,
     *   maxBytes: number, usedIcons: number, usedBytes: number}>} each icon
     *   {name, id, owner, width, height, bytes, created, updated, aliases,
     *   data, canRename, canDelete}
     */
    async listIcons() {
      const data = (await http.get(ICONS_PATH)).data || {};
      const number = (value) => (Number.isFinite(value) ? value : 0);

      return {
        icons: (Array.isArray(data.icons) ? data.icons : [])
          .filter(
            (icon) =>
              typeof icon?.name === 'string' && typeof icon.data === 'string',
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
        maxBytes: number(data.maxBytes),
        usedIcons: number(data.usedIcons),
        usedBytes: number(data.usedBytes),
      };
    },

    /**
     * Adds a PNG to the icon library under a name. The server checks the
     * image and can encode it again, so the icon is the one in the response:
     * its data, not what was sent. For a name that already names an icon with
     * the same bytes, the response is that icon. A name of an icon with other
     * bytes is refused (409).
     *
     * @param {{name: string, data: string}} upload the name and the PNG in
     *   base64
     * @returns {Promise<{icon: object, created: boolean}>} created: false
     *   when the library already held it
     */
    async uploadIcon({ name, data }) {
      const response = await http.post(ICONS_PATH, { name, data });

      return { icon: response.data, created: response.status === 201 };
    },

    /**
     * Gives an icon of the icon library a new name. The old name continues
     * to name it, as an alias.
     *
     * @param {string} name the icon's name, or an alias
     * @param {string} newName
     * @returns {Promise<object>} the icon as renamed
     */
    async renameIcon(name, newName) {
      const response = await http.put(iconPath(name), { name: newName });

      return response.data;
    },

    /**
     * Deletes an icon from the icon library, with its aliases. Nodes that
     * name it show their built-in icon.
     *
     * @param {string} name the icon's name, or an alias
     */
    async deleteIcon(name) {
      await http.delete(iconPath(name));

      return true;
    },

    /**
     * Reads the template library that the user can use: their own templates
     * and collections, then those of other users that are shared with them
     * or published server-wide. Each item's source tells which: own, shared
     * or server. The result includes what the user can do and the library's
     * limits. A user who never changed their library has the built-in
     * templates. After them come the collections that the server read from
     * its template files, each with its templates (source preloaded, no
     * owner), read only for everyone.
     *
     * @returns {Promise<object>} owner, templates and collections (lists),
     *   canShare, canPublish, damaged (the stored library cannot be read,
     *   and lists nothing) and limits
     */
    async listTemplates() {
      const data = (await http.get(TEMPLATES_PATH)).data || {};
      const list = (value) =>
        (Array.isArray(value) ? value : []).filter(
          (item) => typeof item?.id === 'string' && item.id !== '',
        );
      const object = (value) =>
        value && typeof value === 'object' && !Array.isArray(value)
          ? value
          : {};
      // A server collection and its templates, as the other sources list
      // theirs: source preloaded, owned by no one.
      const preloaded = (Array.isArray(data.preloaded) ? data.preloaded : [])
        .map((entry) => ({
          collection: list([object(entry).collection])[0],
          templates: list(object(entry).templates),
        }))
        .filter((entry) => entry.collection)
        .map(({ collection, templates }) => ({
          collection: { ...collection, source: 'preloaded', owner: '' },
          templates: templates.map((template) => ({
            ...template,
            source: 'preloaded',
            owner: '',
          })),
        }));

      return {
        owner: typeof data.owner === 'string' ? data.owner : '',
        templates: [
          ...list(data.templates),
          ...preloaded.flatMap((entry) => entry.templates),
        ],
        collections: [
          ...list(data.collections),
          ...preloaded.map((entry) => entry.collection),
        ],
        canShare: data.canShare === true,
        canPublish: data.canPublish === true,
        damaged: data.damaged === true,
        limits: object(data.limits),
      };
    },

    /**
     * Adds templates to a library, and, with `collection`, a new collection
     * that holds exactly them, in the same change. The server names each
     * template. A template sent with an id is refused.
     *
     * @param {string} owner the library's owner, who the caller must be
     * @param {object} request templates: [{name, description?, device}].
     *   collection: {name, description?}, optional.
     * @returns {Promise<{created: {id: string, etag: string}[],
     *   collection: {id: string, etag: string}|null}>}
     */
    async createTemplates(owner, { templates, collection } = {}) {
      const response = await http.post(templateItemsPath(owner), {
        templates,
        ...(collection ? { collection } : {}),
      });
      const data = response.data || {};

      return {
        created: Array.isArray(data.created) ? data.created : [],
        collection: data.collection || null,
      };
    },

    /**
     * Replaces the name, the description and the device of a template of a
     * library. The server refuses the change (412) when the template is not
     * at `etag` now.
     *
     * @param {string} owner
     * @param {string} id template id
     * @param {object} content name, description? and device
     * @param {string} etag the template's tag, as listed
     * @returns {Promise<object>} the template as it is now, with its new
     *   etag
     */
    async updateTemplate(owner, id, content, etag) {
      const response = await http.put(
        templateItemPath(owner, id),
        content,
        ifMatch(etag),
      );

      return response.data;
    },

    /**
     * Adds a collection to a library: a named group of its templates.
     *
     * @param {string} owner
     * @param {{name: string, description?: string, templateIds?: string[]}}
     *   content
     * @returns {Promise<object>} the collection, with its id and etag
     */
    async createTemplateCollection(owner, content) {
      const response = await http.post(templateCollectionsPath(owner), content);

      return response.data;
    },

    /**
     * Replaces the name, the description and the templates of a collection.
     * The server refuses the change (412) when the collection is not at
     * `etag` now.
     *
     * @param {string} owner
     * @param {string} id collection id
     * @param {{name: string, description?: string, templateIds?: string[]}}
     *   content the whole collection
     * @param {string} etag the collection's tag, as listed
     * @returns {Promise<object>} the collection as it is now
     */
    async updateTemplateCollection(owner, id, content, etag) {
      const response = await http.put(
        templateCollectionPath(owner, id),
        content,
        ifMatch(etag),
      );

      return response.data;
    },

    /**
     * Deletes templates and collections of a library. Ids that the library
     * does not hold are ignored, so a repeat does no harm. A deleted template
     * leaves the collections that held it. A deleted collection leaves its
     * templates.
     *
     * @param {string} owner
     * @param {{templates?: string[], collections?: string[]}} selection
     * @returns {Promise<{templates: number, collections: number}>} how many
     *   of each were deleted
     */
    async deleteTemplates(owner, { templates = [], collections = [] } = {}) {
      const response = await http.post(templateDeletePath(owner), {
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
     * Restores deleted built-in templates of a library, with their original
     * ids. With no ids, every missing built-in template is restored. An id
     * that is not a built-in id, or an id that the library holds, is ignored.
     *
     * @param {string} owner
     * @param {{templates?: string[]}} [selection] built-in template ids
     * @returns {Promise<string[]>} the ids of the restored templates
     */
    async restoreTemplates(owner, { templates = [] } = {}) {
      const response = await http.post(
        templateRestorePath(owner),
        templates.length ? { templates } : {},
      );
      const restored = response.data?.restored;

      return Array.isArray(restored) ? restored : [];
    },

    /**
     * Lists the users the caller may share the templates and collections
     * of their library with: every user but the caller. Only a user with an
     * account of their own may.
     *
     * @returns {Promise<{username: string, name: string}[]>} name may be ''
     */
    async listTemplateShareCandidates() {
      return readCandidates(await http.get(TEMPLATE_CANDIDATES_PATH));
    },

    /**
     * Adds people to templates and collections of a library, and removes
     * people from them, each item at once. Adding a person that an item
     * already has, or removing a person that it does not have, changes
     * nothing for that item. The server refuses the whole request (422, see
     * shareErrors) when it refuses a person to add.
     *
     * @param {string} owner the library's owner, who the caller must be
     * @param {object} change templates, collections: the items, by id. add,
     *   remove: usernames.
     * @returns {Promise<{failed: object[]}>} the items left as they were,
     *   see readLibraryResult
     */
    async shareTemplates(
      owner,
      { templates = [], collections = [], add = [], remove = [] } = {},
    ) {
      return readLibraryResult(
        await http.post(templateSharePath(owner), {
          ...(templates.length ? { templates } : {}),
          ...(collections.length ? { collections } : {}),
          ...(add.length ? { add } : {}),
          ...(remove.length ? { remove } : {}),
        }),
      );
    },

    /**
     * Publishes templates and collections of a library server-wide, for
     * everyone who can use the Builder, or withdraws them. Only the owner
     * publishes. The owner, or a role that can publish, withdraws.
     *
     * @param {string} owner the library's owner
     * @param {object} change templates, collections: the items, by id.
     *   serverWide: whether they are to be server-wide.
     * @returns {Promise<{failed: object[]}>} see readLibraryResult
     */
    async publishTemplates(
      owner,
      { templates = [], collections = [], serverWide } = {},
    ) {
      return readLibraryResult(
        await http.post(templatePublishPath(owner), {
          ...(templates.length ? { templates } : {}),
          ...(collections.length ? { collections } : {}),
          serverWide: serverWide === true,
        }),
      );
    },

    /**
     * Lists the disk images that the server has: the image files in
     * minimega's files directory and the images that experiments use, of
     * every kind (VM, container, ISO), as far as the user's role can list
     * them.
     *
     * @returns {Promise<string[]>} file names, without their directory
     */
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

/**
 * An HTTP client that reports when the server refuses the session (401), so
 * the Builder can offer to sign in again (see signin.js). In all other
 * respects, each request is the client's own, and fails as it would.
 *
 * @param {object} http axios-compatible client
 * @param {(error: object) => void} [ended] called for each 401
 * @returns {object} client
 */
export function watchSession(http, ended = sessionEnded) {
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

export const builderApi = createBuilderApi(watchSession(axiosInstance));
