// Builder Beta API client.
//
// Path builders are exported separately from the client so routes can be unit
// tested without HTTP, and so the same paths can be reused by the autosave
// queue. All requests are relative to the app's /api/v1/ base (axios instance).
//
// The draft record is versioned: every mutation carries the ETag the client
// last observed as `If-Match`, and the server rejects a stale write instead of
// letting one editor silently overwrite another. There is deliberately no
// "force" variant of any call here.

import axiosInstance from '@/utils/axios.js';

export const DRAFTS_PATH = 'builder/drafts';
export const SOURCES_PATH = 'builder/sources';
export const GENERATE_PATH = 'builder/generate';
export const DOCUMENTS_PATH = 'builder/documents';
export const SCHEMA_PATH = 'schemas/builder/v1';
// Disk images, the same listing the Disks page reads (web/disk.go GetDisks).
export const DISKS_PATH = 'disks';

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
 * @param {string} id published document id
 * @returns {string}
 */
export function documentPath(id) {
  return `${DOCUMENTS_PATH}/${encodeURIComponent(id)}`;
}

/**
 * Reads the draft ETag from a response: from its body, which carries it in
 * every draft envelope (and a publish result's draft), or else from the ETag
 * header, tolerating header bags and Maps. The body comes first because a
 * compressing proxy rewrites the header (nginx turns "3" into the weak
 * W/"3", Apache into "3-gzip"), and the server refuses such a tag in If-Match.
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

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and the largest request body it reads: a document and 1 MiB for the rest
// of the request (builderBetaMaxRequestBytes in web).
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;
export const MAX_REQUEST_BYTES = MAX_DOCUMENT_BYTES + 1024 * 1024;

function mebibytes(bytes) {
  return `${Number((bytes / (1024 * 1024)).toFixed(1))} MiB`;
}

/**
 * An upload the server would refuse for its size, refused before it is
 * sent. The server refuses such a body before reading all of it, and some
 * browsers (Firefox) then report the reset connection rather than the 413,
 * which would read as the server being unreachable.
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

function utf8Length(text) {
  return new TextEncoder().encode(text).byteLength;
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
 * Refuses an upload the server would refuse for its size: a document or
 * uploaded config over MAX_DOCUMENT_BYTES, or a body over MAX_REQUEST_BYTES.
 *
 * @param {object} payload request body
 * @throws {TooLargeError}
 */
function checkUploadSize(payload) {
  const body = JSON.stringify(payload);

  // The document and the content are part of the body.
  if (!exceeds(body, MAX_DOCUMENT_BYTES)) {
    return;
  }

  const diagram = payload.document !== undefined;
  const part = diagram ? JSON.stringify(payload.document) : payload.content;

  if (typeof part === 'string' && exceeds(part, MAX_DOCUMENT_BYTES)) {
    throw new TooLargeError(
      diagram ? 'This diagram' : 'The uploaded config',
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
// draft <id>", "builder document cannot be published as topology <name>");
// the reason is then in the cause.
const GENERIC_MESSAGE =
  /^(?:unable to |builder document cannot be (?:published as topology \S+|projected)$)/i;

/**
 * The reason a server gave for refusing a request, in words a user can act
 * on, as a sentence: capitalized and ending in a full stop. Server errors
 * carry a `message` and a `cause`. Most messages say what is wrong ("config
 * core already exists; choose update explicitly") and are used as they are.
 * A generic one, which only names the operation refused ("unable to save
 * builder draft <id>"), is replaced by the first issue of the cause chain
 * ("builder: invalid request: document: ...: nodes[1].device.hostname:
 * duplicate hostname "node-2" (also nodes[0])"), without its prefixes,
 * document paths, array indexes and ids. Ids are never shown.
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

  // Issues are separated by "; ", and schema errors by " | ", which can
  // repeat one another.
  const [reason, ...others] = new Set(
    cause
      .split(/; | \| /)
      .filter((issue) => issue.trim())
      .map(causeReason),
  );
  const more = others.length;

  return sentence(
    more > 0
      ? `${reason} (and ${more} more problem${more === 1 ? '' : 's'})`
      : reason,
  );
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

function ifMatch(etag) {
  return etag ? { headers: { 'If-Match': etag } } : {};
}

/**
 * Normalizes a draft envelope: metadata, the current document and the history
 * index the server holds. Only a read of the draft carries its history; a
 * save answers with the draft alone, and its history is then null (unknown),
 * not empty.
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
const SCENARIO_ACTIONS = ['use', 'create', 'update'];

/**
 * Builds the publish request body. Publish is an *intent*: the server loads the
 * snapshot the draft cursor points at and re-runs its own checks, so document
 * bytes are never sent here. Anything that is not part of the intent is
 * dropped rather than forwarded.
 *
 * @param {object} intent mode, topology, scenario, experiment
 * @returns {object} request body
 */
export function publishIntent(intent = {}) {
  const mode = PUBLISH_MODES.includes(intent.mode) ? intent.mode : 'topology';
  const target = (value, actions, includeDigest = false) => {
    const name = typeof value?.name === 'string' ? value.name.trim() : '';
    const action = actions.includes(value?.action) ? value.action : null;

    if (!name || !action) {
      return null;
    }

    const result = { name, action };
    if (includeDigest && typeof value.expectedDigest === 'string') {
      result.expectedDigest = value.expectedDigest;
    }

    return result;
  };

  const topology = target(intent.topology, CONFIG_ACTIONS);

  if (!topology) {
    throw new Error('A topology name and action are required to publish.');
  }

  const body = { mode, topology };
  const scenario = target(intent.scenario, SCENARIO_ACTIONS, true);

  if (scenario) {
    body.scenario = scenario;
  }

  if (mode === 'topology-experiment') {
    const experiment = target(intent.experiment, CONFIG_ACTIONS);

    if (!experiment) {
      throw new Error('An experiment name and action are required.');
    }

    body.experiment = experiment;
  }

  return body;
}

/**
 * Normalizes a publish response. The server reports per stage results, so a
 * partial failure (topology written, experiment refused) is surfaced instead of
 * being reported as a plain success.
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

  return {
    status,
    partial: status === 'partial',
    ok: failed.length === 0,
    stages,
    failed,
    warnings: Array.isArray(data.warnings) ? data.warnings : [],
    errors: Array.isArray(data.errors) ? data.errors : [],
    topology: data.topology || null,
    scenario: data.scenario || null,
    experiment: data.experiment || null,
    // The draft record, with the publication this one recorded.
    draft: data.draft || null,
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
    async listDrafts() {
      const response = await http.get(DRAFTS_PATH);
      const data = response.data || {};

      return {
        mine: data.drafts || data.mine || [],
        shared: data.shared || [],
        published: data.published || [],
      };
    },

    /**
     * Creates a draft from a complete document.
     *
     * @param {{owner?: string, title?: string, sourceToken?: string,
     *   document: object, summary?: string}} request
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

    async listSnapshots(owner, id) {
      const response = await http.get(snapshotPath(owner, id));

      return response.data?.history || response.data?.snapshots || [];
    },

    /**
     * Appends one snapshot. Every semantic edit is its own snapshot: the client
     * never coalesces commits, because the history the user can undo through
     * must be the history the server stores.
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
     * Publishes the snapshot the draft cursor points at. Only the intent is
     * sent: the server owns the document bytes and re-validates them.
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
     * Asks the server to build a builder document from an existing config.
     *
     * @param {{kind: string, name: string}} request
     * @returns {Promise<{document: object, warnings: string[]}>}
     */
    async generate(request) {
      const payload =
        typeof request?.content === 'string'
          ? { content: request.content }
          : { source: `${request.kind}/${request.name}` };

      checkUploadSize(payload);

      const response = await http.post(GENERATE_PATH, payload);
      const data = response.data || {};

      return {
        document: data.document || data,
        warnings: data.warnings || [],
        source: data.source || null,
      };
    },

    async listDocuments() {
      const response = await http.get(DOCUMENTS_PATH);

      return response.data?.documents || [];
    },

    async getDocument(id) {
      const response = await http.get(documentPath(id));
      const data = response.data || {};

      return data.document || data;
    },

    async getSchema() {
      const response = await http.get(SCHEMA_PATH);

      return response.data;
    },

    /**
     * Lists the disk images the server has: the image files in minimega's
     * files directory and the images experiments use, of every kind (VM,
     * container, ISO), as far as the user's role may list them.
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

export const builderApi = createBuilderApi();
