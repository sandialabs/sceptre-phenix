// Strict document decoding, which mirrors phenix/types/builder decode.go.
//
// The decoder rejects unknown fields, a wrong schema URI or revision, and
// structurally invalid documents. The builder never drops data that it does
// not understand, because then it would publish a topology that the user did
// not make.

import YAML from 'js-yaml';

import { MAX_DOCUMENT_BYTES } from './limits.js';
import {
  METADATA_KEYS as METADATA_KEY_ORDER,
  NODE_KINDS,
  SCHEMA_REVISION,
  SCHEMA_URI,
} from './model.js';
import { utf8Length } from './text.js';
import { validateDocument } from './validate.js';

// Re-exported for the dialogs that read files.
export { MAX_DOCUMENT_BYTES };

// What a file is called where it is chosen: Upload and the Scenario dialog
// take an upload, and Import reads a config file.
const FILE_NAMES = {
  file: 'uploaded file',
  scenario: 'uploaded scenario',
  config: 'config file',
};

/**
 * Why a file over MAX_DOCUMENT_BYTES is refused.
 *
 * @param {string} noun what the file holds: "file", "config", "scenario"
 * @returns {string}
 */
export function tooLargeText(noun) {
  return `The ${FILE_NAMES[noun]} is larger than the ${MAX_DOCUMENT_BYTES / 1024 / 1024} MiB limit.`;
}

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

// The keys of a document: the root properties of the schema bundle
// (schema/builder-v1.schema.json), which a test compares them with.
export const DOCUMENT_KEYS = new Set([
  '$schema',
  'revision',
  'metadata',
  'nodes',
  'networks',
  'edges',
  'viewport',
  'grid',
  'scenarios',
  'source',
  'layout',
  'iconSize',
  'templates',
  'icons',
]);

export const METADATA_KEYS = new Set(METADATA_KEY_ORDER);

// The keys of each object of a document: the properties of its definition
// in the schema bundle, which a test compares them with.
export const NODE_KEYS = new Set([
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
  'shape',
  'icon',
  'line',
]);

// A node holds its payload under the name of its kind.
const PAYLOAD_KEYS = NODE_KINDS;

export const DEVICE_KEYS = new Set([
  'hostname',
  'iconKey',
  'icon',
  'iconSize',
  'outlineColor',
  'fillColor',
  'purdueLevel',
  'spec',
  'interfaces',
  'includedFrom',
]);
export const HANDLE_KEYS = new Set(['id', 'name', 'index']);
export const SWITCH_KEYS = new Set([
  'networkId',
  'outlineColor',
  'fillColor',
  'iconSize',
  'purdueLevel',
  'notes',
]);
export const NOTE_KEYS = new Set(['text', 'color']);
export const GROUP_KEYS = new Set([
  'title',
  'description',
  'color',
  'borderStyle',
  'iconKey',
  'icon',
  'iconSize',
  'collapsed',
]);
export const SHAPE_KEYS = new Set([
  'shape',
  'label',
  'fillColor',
  'outlineColor',
  'borderStyle',
]);
export const ICON_NODE_KEYS = new Set(['iconKey', 'icon', 'label']);
export const LINE_KEYS = new Set([
  'points',
  'label',
  'color',
  'lineStyle',
  'startArrow',
  'endArrow',
]);
export const NETWORK_KEYS = new Set([
  'id',
  'name',
  'alias',
  'description',
  'color',
  'lineStyle',
]);
export const EDGE_KEYS = new Set([
  'id',
  'sourceNodeId',
  'sourceHandleId',
  'targetNodeId',
  'targetHandleId',
  'networkId',
  'label',
  'color',
  'lineStyle',
  'route',
]);
const POINT_KEYS = new Set(['x', 'y']);
export const VIEWPORT_KEYS = new Set(['x', 'y', 'zoom']);
export const GRID_KEYS = new Set(['enabled', 'size', 'snap']);
export const SOURCE_KEYS = new Set([
  'kind',
  'name',
  'apiVersion',
  'topology',
  'importedAt',
  'digest',
  'updatedAt',
  'includeTopologies',
  'unresolvedIncludes',
  'annotations',
  'warnings',
]);
export const TEMPLATE_KEYS = new Set(['id', 'name', 'description', 'device']);

// What a device has of its own, which a template does not fill in: its
// name, its connection points, and where it was included from.
const OWN_DEVICE_KEYS = ['hostname', 'interfaces', 'includedFrom'];

// A template fills in a device, so its device has every other key that a
// device has. Thus a key added to devices gets to templates without a second
// edit. TemplateDevice in template.go follows Device in the same way.
export const TEMPLATE_DEVICE_KEYS = new Set(
  [...DEVICE_KEYS].filter((key) => !OWN_DEVICE_KEYS.includes(key)),
);
// A copy of a custom icon is its data alone: its key is its name (Icon in
// customicons.go).
export const ICON_ENTRY_KEYS = new Set(['data']);

// A refusal that the server's decoder also lists as an issue (decodeIssues
// in decode.go). message tells what is refused. The issue names its rule with
// the server's code, path and words.
function refusal(message, code, path, issueMessage) {
  return new DocumentError(message, {
    issues: [
      {
        code,
        path,
        message: issueMessage,
        level: 'error',
        severity: 'error',
      },
    ],
  });
}

// What the server says of a list of notes that is not one, of the diagram
// or of a switch.
const NOTES_NOT_LIST = 'notes must be a list of text';

function rejectUnknown(value, allowed, path) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new DocumentError(`${path}: expected an object`);
  }

  Object.keys(value).forEach((key) => {
    if (!allowed.has(key)) {
      throw refusal(
        `${path}: unknown field "${key}"`,
        'document.field.unknown',
        path === 'document' ? key : `${path}.${key}`,
        `unknown field "${key}"`,
      );
    }
  });
}

function checkNode(node, index) {
  const path = `nodes[${index}]`;

  rejectUnknown(node, NODE_KEYS, path);

  const payloads = PAYLOAD_KEYS.filter(
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

    // As the server's decoding refuses any other value, null being none.
    const { notes } = node.switch;

    if (notes !== undefined && notes !== null && !Array.isArray(notes)) {
      throw refusal(
        `document: "${path}.switch.notes" must be an array`,
        'switch.notes.not-list',
        `${path}.switch.notes`,
        NOTES_NOT_LIST,
      );
    }
  }

  if (node.note !== undefined) {
    rejectUnknown(node.note, NOTE_KEYS, `${path}.note`);
  }

  if (node.group !== undefined) {
    rejectUnknown(node.group, GROUP_KEYS, `${path}.group`);
  }

  if (node.shape !== undefined) {
    rejectUnknown(node.shape, SHAPE_KEYS, `${path}.shape`);
  }

  if (node.icon !== undefined) {
    rejectUnknown(node.icon, ICON_NODE_KEYS, `${path}.icon`);
  }

  if (node.line !== undefined) {
    rejectUnknown(node.line, LINE_KEYS, `${path}.line`);

    if (Array.isArray(node.line.points)) {
      node.line.points.forEach((point, i) => {
        rejectUnknown(point, POINT_KEYS, `${path}.line.points[${i}]`);
      });
    }
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

  // Left out or null, the metadata is empty, as Go decodes it, and
  // validation then asks for its id.
  if (value.metadata !== undefined && value.metadata !== null) {
    if (typeof value.metadata !== 'object' || Array.isArray(value.metadata)) {
      throw refusal(
        'document: "metadata" must be an object',
        'metadata.not-object',
        'metadata',
        'metadata must be an object',
      );
    }

    rejectUnknown(value.metadata, METADATA_KEYS, 'metadata');

    const { notes } = value.metadata;

    if (notes !== undefined && notes !== null && !Array.isArray(notes)) {
      throw refusal(
        'document: "metadata.notes" must be an array',
        'metadata.notes.not-list',
        'metadata.notes',
        NOTES_NOT_LIST,
      );
    }
  }

  ['nodes', 'networks', 'edges'].forEach((key) => {
    if (!Array.isArray(value[key])) {
      throw refusal(
        `document: "${key}" must be an array`,
        'document.list.missing',
        key,
        `${key} must be an array`,
      );
    }
  });

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

  // The server decodes scenarios as a list of text, a null entry as empty
  // text, which validation then refuses as a missing name.
  if (
    value.scenarios !== undefined &&
    value.scenarios !== null &&
    (!Array.isArray(value.scenarios) ||
      value.scenarios.some((name) => name !== null && typeof name !== 'string'))
  ) {
    throw new DocumentError(
      'document: "scenarios" must be an array of scenario names',
    );
  }

  if (value.source !== undefined && value.source !== null) {
    rejectUnknown(value.source, SOURCE_KEYS, 'source');
  }

  if (value.templates !== undefined && value.templates !== null) {
    if (!Array.isArray(value.templates)) {
      throw refusal(
        'document: "templates" must be an array',
        'template.list.not-list',
        'templates',
        'templates must be a list of templates',
      );
    }

    value.templates.forEach((template, index) => {
      rejectUnknown(template, TEMPLATE_KEYS, `templates[${index}]`);

      // A template without a device is refused by validation, as the
      // server refuses it.
      if (template.device !== undefined && template.device !== null) {
        rejectUnknown(
          template.device,
          TEMPLATE_DEVICE_KEYS,
          `templates[${index}].device`,
        );
      }
    });
  }

  if (value.icons !== undefined && value.icons !== null) {
    if (typeof value.icons !== 'object' || Array.isArray(value.icons)) {
      throw refusal(
        'document: "icons" must be an object',
        'icon.list.not-object',
        'icons',
        'custom icons must be an object of icons by name',
      );
    }

    // An icon is named by its key, as the server names it.
    Object.entries(value.icons).forEach(([name, entry]) => {
      rejectUnknown(entry, ICON_ENTRY_KEYS, `icons.${name}`);
    });
  }

  const doc = JSON.parse(JSON.stringify(value));

  doc.viewport = doc.viewport || { x: 0, y: 0, zoom: 1 };
  doc.grid = doc.grid || { enabled: true, size: 16, snap: true };
  doc.metadata = doc.metadata || {};

  // Null is none, as Go decodes it.
  ['layout', 'iconSize', 'templates', 'icons'].forEach((key) => {
    if (doc[key] === null) {
      delete doc[key];
    }
  });

  METADATA_KEY_ORDER.forEach((key) => {
    if (doc.metadata[key] === null) {
      delete doc.metadata[key];
    }
  });

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

/**
 * Thrown by parseText for YAML that holds an alias (*name).
 */
export class AliasError extends Error {}

// Stops a YAML parse at its first alias (*name). js-yaml itself resolves an
// alias to the value of its anchor. Every copy of the document then expands
// that value once per alias. Thus a few nested aliases in a file of a few
// hundred bytes expand to gigabytes. js-yaml reports each node as it closes,
// and only an alias closes with a value but no kind and no tag.
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
 * The value of the text in a file: JSON, or else YAML read with js-yaml's
 * JSON schema, as the server reads it (JSONFromText in yaml.go). The content
 * decides the format, never the file's name.
 *
 * @param {string} text
 * @returns {*}
 * @throws {AliasError} for YAML that holds an alias
 * @throws {Error} for text that is neither, with js-yaml's reason
 */
export function parseText(text) {
  try {
    return JSON.parse(text);
  } catch {
    return YAML.load(text, {
      schema: YAML.JSON_SCHEMA,
      listener: refuseAliases,
    });
  }
}

/**
 * Parses uploaded text (JSON or YAML) into a validated builder document.
 * Anything that is not a builder document of this schema is rejected with a
 * message that names the reason. It is never coerced.
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

  const maxBytes = options.maxBytes ?? MAX_DOCUMENT_BYTES;
  if (utf8Length(input) > maxBytes) {
    return {
      ok: false,
      code: 'too-large',
      error: tooLargeText('file'),
    };
  }

  let parsed;

  try {
    parsed = parseText(input);
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
