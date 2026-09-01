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
 * Reads the ETag from a response, tolerating header bags and Maps.
 *
 * @param {object} response axios-like response
 * @returns {string|null}
 */
export function readETag(response) {
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
 * Classifies an API failure so the UI can show a specific state.
 *
 * @param {object} error axios-like error
 * @returns {'conflict'|'forbidden'|'missing'|'offline'|'too-large'|'error'}
 */
export function classifyError(error) {
  const status = error?.response?.status;

  if (status === 409 || status === 412 || status === 428) {
    return 'conflict';
  }

  if (status === 403 || status === 401) {
    return 'forbidden';
  }

  if (status === 404) {
    return 'missing';
  }

  if (status === 413) {
    return 'too-large';
  }

  if (!error?.response) {
    return 'offline';
  }

  return 'error';
}

/**
 * Human readable message for a failure class.
 *
 * @param {string} kind result of classifyError
 * @param {object} [error]
 * @returns {string}
 */
export function errorMessage(kind, error) {
  const detail = error?.response?.data?.error || error?.response?.data?.message;

  switch (kind) {
    case 'conflict':
      return 'This draft changed on the server since you loaded it.';
    case 'forbidden':
      return detail || 'You are not allowed to change this draft.';
    case 'missing':
      return detail || 'This draft no longer exists on the server.';
    case 'too-large':
      return detail || 'This diagram is too large to save.';
    case 'offline':
      return 'The server could not be reached. Changes are kept on this device.';
    default:
      return detail || 'The server rejected the request.';
  }
}

function ifMatch(etag) {
  return etag ? { headers: { 'If-Match': etag } } : {};
}

/**
 * Normalizes a draft envelope: metadata, the current document and the history
 * index the server holds.
 *
 * @param {object} response axios-like response
 * @returns {{draft: object, document: object|null, history: object[], cursor: number, etag: string|null}}
 */
export function readEnvelope(response) {
  const data = response?.data || {};
  const draft = data.draft || data.metadata || data;

  return {
    draft,
    document: data.document || null,
    history: draft.history || data.history || [],
    cursor: Number.isInteger(draft.cursor) ? draft.cursor : (data.cursor ?? 0),
    etag: readETag(response),
  };
}

export const PUBLISH_MODES = ['topology', 'topology-experiment'];
export const CONFIG_ACTIONS = ['create', 'update'];
export const SCENARIO_ACTIONS = ['use', 'create', 'update'];

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
     * @param {{document: object, summary?: string}} payload
     * @param {string} etag observed draft ETag
     */
    async appendSnapshot(owner, id, payload, etag) {
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
  };
}

export const builderApi = createBuilderApi();
