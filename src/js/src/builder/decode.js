// Strict document decoding, mirroring phenix/types/builder decode.go.
//
// Unknown fields, a wrong schema URI or revision, and structurally invalid
// documents are rejected outright: the builder never silently drops data it
// does not understand, because doing so would publish a topology the user never
// authored.

import YAML from 'js-yaml';

import { SCHEMA_REVISION, SCHEMA_URI } from './model.js';
import { validateDocument } from './validate.js';

export const MAX_IMPORT_BYTES = 5 * 1024 * 1024;

export class DocumentError extends Error {
  /**
   * @param {string} message
   * @param {object} [options] code, issues
   */
  constructor(message, options = {}) {
    super(message);
    this.name = 'DocumentError';
    this.code = options.code || 'invalid';
    this.issues = options.issues || [];
  }
}

const DOCUMENT_KEYS = new Set([
  '$schema',
  'revision',
  'id',
  'name',
  'description',
  'nodes',
  'networks',
  'edges',
  'viewport',
  'grid',
  'scenario',
  'source',
  'layout',
]);

const NODE_KEYS = new Set([
  'id',
  'kind',
  'label',
  'position',
  'size',
  'parentId',
  'device',
  'switch',
  'note',
  'group',
]);

const DEVICE_KEYS = new Set([
  'hostname',
  'iconKey',
  'spec',
  'interfaces',
  'includedFrom',
]);
const HANDLE_KEYS = new Set(['id', 'name', 'index']);
const SWITCH_KEYS = new Set(['networkId']);
const NOTE_KEYS = new Set(['text', 'color']);
const GROUP_KEYS = new Set(['title', 'color', 'collapsed']);
const NETWORK_KEYS = new Set(['id', 'name', 'alias', 'description', 'color']);
const EDGE_KEYS = new Set([
  'id',
  'sourceNodeId',
  'sourceHandleId',
  'targetNodeId',
  'targetHandleId',
  'networkId',
  'label',
  'color',
  'route',
]);
const POINT_KEYS = new Set(['x', 'y']);
const VIEWPORT_KEYS = new Set(['x', 'y', 'zoom']);
const GRID_KEYS = new Set(['enabled', 'size', 'snap']);
const SCENARIO_KEYS = new Set([
  'kind',
  'name',
  'content',
  'apiVersion',
  'digest',
]);
const SOURCE_KEYS = new Set([
  'kind',
  'name',
  'apiVersion',
  'topology',
  'importedAt',
  'digest',
  'updatedAt',
  'includeTopologies',
  'warnings',
]);

function rejectUnknown(value, allowed, path) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new DocumentError(`${path}: expected an object`);
  }

  Object.keys(value).forEach((key) => {
    if (!allowed.has(key)) {
      throw new DocumentError(`${path}: unknown field "${key}"`);
    }
  });
}

function checkNode(node, index) {
  const path = `nodes[${index}]`;

  rejectUnknown(node, NODE_KEYS, path);

  const payloads = ['device', 'switch', 'note', 'group'].filter(
    (key) => node[key] !== undefined && node[key] !== null,
  );

  if (payloads.length !== 1) {
    throw new DocumentError(
      `${path}: a node must carry exactly one payload, found ${payloads.length ? payloads.join(', ') : 'none'}`,
    );
  }

  if (node.kind !== payloads[0]) {
    throw new DocumentError(
      `${path}: kind "${node.kind ?? ''}" does not match the "${payloads[0]}" payload`,
    );
  }

  if (node.position !== undefined) {
    rejectUnknown(node.position, new Set(['x', 'y']), `${path}.position`);
  }

  if (node.size !== undefined && node.size !== null) {
    rejectUnknown(node.size, new Set(['width', 'height']), `${path}.size`);
  }

  if (node.device !== undefined) {
    rejectUnknown(node.device, DEVICE_KEYS, `${path}.device`);

    (node.device.interfaces || []).forEach((handle, i) => {
      rejectUnknown(handle, HANDLE_KEYS, `${path}.device.interfaces[${i}]`);
    });
  }

  if (node.switch !== undefined) {
    rejectUnknown(node.switch, SWITCH_KEYS, `${path}.switch`);
  }

  if (node.note !== undefined) {
    rejectUnknown(node.note, NOTE_KEYS, `${path}.note`);
  }

  if (node.group !== undefined) {
    rejectUnknown(node.group, GROUP_KEYS, `${path}.group`);
  }
}

/**
 * Strictly decodes a builder document from an already parsed value.
 *
 * @param {object} value
 * @returns {object} the document (a deep copy)
 * @throws {DocumentError}
 */
export function decodeDocument(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new DocumentError('A builder document must be a JSON object.');
  }

  if (value.$schema !== SCHEMA_URI) {
    throw new DocumentError(
      `Unsupported builder document schema "${value.$schema ?? ''}" (expected "${SCHEMA_URI}").`,
      { code: 'unsupported-schema' },
    );
  }

  if (value.revision !== SCHEMA_REVISION) {
    throw new DocumentError(
      `Unsupported builder document revision ${value.revision ?? ''} (expected ${SCHEMA_REVISION}).`,
      { code: 'unsupported-revision' },
    );
  }

  rejectUnknown(value, DOCUMENT_KEYS, 'document');

  if (!Array.isArray(value.nodes)) {
    throw new DocumentError('document: "nodes" must be an array');
  }

  if (!Array.isArray(value.networks)) {
    throw new DocumentError('document: "networks" must be an array');
  }

  if (!Array.isArray(value.edges)) {
    throw new DocumentError('document: "edges" must be an array');
  }

  value.nodes.forEach(checkNode);

  value.networks.forEach((network, index) => {
    rejectUnknown(network, NETWORK_KEYS, `networks[${index}]`);
  });

  value.edges.forEach((edge, index) => {
    rejectUnknown(edge, EDGE_KEYS, `edges[${index}]`);

    if (Array.isArray(edge.route)) {
      edge.route.forEach((point, i) => {
        rejectUnknown(point, POINT_KEYS, `edges[${index}].route[${i}]`);
      });
    }
  });

  if (value.viewport !== undefined) {
    rejectUnknown(value.viewport, VIEWPORT_KEYS, 'viewport');
  }

  if (value.grid !== undefined) {
    rejectUnknown(value.grid, GRID_KEYS, 'grid');
  }

  if (value.scenario !== undefined && value.scenario !== null) {
    rejectUnknown(value.scenario, SCENARIO_KEYS, 'scenario');
  }

  if (value.source !== undefined && value.source !== null) {
    rejectUnknown(value.source, SOURCE_KEYS, 'source');
  }

  const doc = JSON.parse(JSON.stringify(value));

  doc.viewport = doc.viewport || { x: 0, y: 0, zoom: 1 };
  doc.grid = doc.grid || { enabled: true, size: 16, snap: true };

  // Null is none, as Go decodes it.
  if (doc.layout === null) {
    delete doc.layout;
  }

  doc.edges.forEach((edge) => {
    if (edge && edge.route === null) {
      delete edge.route;
    }
  });

  return doc;
}

/**
 * Decodes and fully validates a document.
 *
 * @param {object} value
 * @returns {object} document
 * @throws {DocumentError}
 */
export function parseDocument(value) {
  const doc = decodeDocument(value);
  const issues = validateDocument(doc).filter(
    (entry) => entry.level === 'error',
  );

  if (issues.length > 0) {
    throw new DocumentError(
      `Invalid builder document: ${issues
        .slice(0, 3)
        .map((entry) => `${entry.path}: ${entry.message}`)
        .join('; ')}`,
      { code: 'invalid', issues },
    );
  }

  return doc;
}

/**
 * A config kind with its article: "a Topology", "an Experiment".
 *
 * @param {string} kind
 * @returns {string}
 */
export function withArticle(kind) {
  return `${/^[aeio]/i.test(kind) ? 'an' : 'a'} ${kind}`;
}

class AliasError extends Error {}

// Stops a YAML parse at its first alias (*name). js-yaml resolves an alias to
// its anchor's value itself, which every copy of the document then expands
// once per alias, so a few nested aliases in a file of a few hundred bytes
// expand to gigabytes. js-yaml reports each node as it closes, and only an
// alias closes with a value but no kind and no tag.
function refuseAliases(event, state) {
  if (
    event === 'close' &&
    state.kind === null &&
    state.tag === null &&
    state.result !== null
  ) {
    throw new AliasError();
  }
}

/**
 * Parses import text (JSON or YAML) into a validated builder document. Anything
 * that is not a builder document of this schema is rejected with a message that
 * names the reason, never silently coerced.
 *
 * @param {string} text
 * @param {{as?: string, expectedKind?: string, maxBytes?: number}} [options]
 * @returns {{ok: true, document: object} | {ok: false, error: string, code: string}}
 */
export function parseImport(text, options = {}) {
  const input = String(text || '');

  if (!input.trim()) {
    return {
      ok: false,
      code: 'empty',
      error: 'Nothing to upload: the document is empty.',
    };
  }

  const maxBytes = options.maxBytes ?? MAX_IMPORT_BYTES;
  if (new TextEncoder().encode(input).byteLength > maxBytes) {
    return {
      ok: false,
      code: 'too-large',
      error: 'The uploaded file is larger than the 5 MiB limit.',
    };
  }

  let parsed;

  try {
    parsed = JSON.parse(input);
  } catch {
    try {
      parsed = YAML.load(input, {
        schema: YAML.JSON_SCHEMA,
        listener: refuseAliases,
      });
    } catch (error) {
      if (error instanceof AliasError) {
        return {
          ok: false,
          code: 'aliases',
          error:
            'YAML aliases (*name) are not supported. Write out each value an alias repeats and upload the document again.',
        };
      }

      return {
        ok: false,
        code: 'parse',
        error: `Could not parse the document: ${error.message}`,
      };
    }
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return {
      ok: false,
      code: 'parse',
      error: 'The document must be a JSON or YAML object.',
    };
  }

  if (options.expectedKind && parsed.kind !== options.expectedKind) {
    return {
      ok: false,
      code: 'unsupported-kind',
      error: `Expected ${withArticle(options.expectedKind)} config, not ${parsed.kind || 'an untyped object'}.`,
    };
  }

  if (options.as === 'raw') {
    return { ok: true, value: parsed };
  }

  if (parsed.kind === 'Topology' || parsed.kind === 'Experiment') {
    return {
      ok: false,
      code: 'unsupported-kind',
      error:
        `${withArticle(parsed.kind).replace(/^a/, 'A')} config is not a Builder document. ` +
        'Use Import on the drafts page so the server can convert it.',
    };
  }

  try {
    return { ok: true, document: parseDocument(parsed) };
  } catch (error) {
    return {
      ok: false,
      code: error.code || 'invalid',
      error: error.message,
      issues: error.issues,
    };
  }
}
