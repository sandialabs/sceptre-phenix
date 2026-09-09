// Builder document schema access.
//
// The inspector is entirely schema driven: whatever the server returns from
// GET /api/v1/schemas/builder/v1 wins. The bundle checked in beside this module
// is generated from the same Go source that serves that endpoint and is used
// only as a fallback; when the server copy cannot be fetched the editor keeps
// working but the failure is reported to the user rather than swallowed.

import bundledSchema from './schema/builder-v1.schema.json';

import { SCHEMA_URI } from './model.js';

export const BUILDER_SCHEMA_ID = SCHEMA_URI;
export const PHENIX_DEF_PREFIX = 'phenix.v1.';
export const MINIMEGA_NODE_DEF = `${PHENIX_DEF_PREFIX}minimega_node`;
export const EXTERNAL_NODE_DEF = `${PHENIX_DEF_PREFIX}external_node`;

export const builderSchemaV1 = bundledSchema;

/**
 * True when a payload is a builder schema bundle this editor can drive forms
 * from.
 *
 * @param {object} payload
 * @returns {boolean}
 */
export function isSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  return Boolean(
    candidate &&
      typeof candidate === 'object' &&
      candidate.$defs &&
      typeof candidate.$defs === 'object',
  );
}

/**
 * Accepts either a bare schema or an API envelope and returns a usable bundle.
 * A payload without `$defs` is not a builder schema bundle and is ignored.
 *
 * @param {object} payload
 * @returns {object} schema bundle
 */
export function normalizeSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  if (!candidate || typeof candidate !== 'object' || !candidate.$defs) {
    return builderSchemaV1;
  }

  return candidate;
}

/**
 * Resolves a local `#/$defs/...` reference against a bundle.
 *
 * @param {object} bundle
 * @param {string} pointer
 * @returns {object|undefined}
 */
export function resolveRef(bundle, pointer) {
  if (typeof pointer !== 'string' || !pointer.startsWith('#/$defs/')) {
    return undefined;
  }

  return bundle?.$defs?.[pointer.slice('#/$defs/'.length)];
}

/**
 * Resolves a schema that may be a `$ref`, one level deep.
 *
 * @param {object} bundle
 * @param {object} schema
 * @returns {object}
 */
export function deref(bundle, schema) {
  if (!schema || typeof schema !== 'object') {
    return {};
  }

  if (schema.$ref) {
    return resolveRef(bundle, schema.$ref) || {};
  }

  if (Array.isArray(schema.allOf) && schema.allOf.length === 1) {
    return { ...deref(bundle, schema.allOf[0]), ...omit(schema, ['allOf']) };
  }

  return schema;
}

function omit(value, keys) {
  return Object.fromEntries(
    Object.entries(value).filter(([key]) => !keys.includes(key)),
  );
}

/**
 * The `$defs` name of the phenix spec variant a device spec belongs to.
 *
 * External devices are described by `external_node`, everything else by
 * `minimega_node`; picking the concrete variant (rather than handing JSON Forms
 * the `oneOf`) keeps the complete applicable phenix schema on screen.
 *
 * @param {object} spec phenix node spec
 * @returns {string} def name
 */
export function specDefName(spec) {
  if (spec?.external === true || spec?.type === 'HIL') {
    return EXTERNAL_NODE_DEF;
  }

  return MINIMEGA_NODE_DEF;
}

/**
 * Complete phenix node spec schema for a device node.
 *
 * @param {object} bundle
 * @param {object} spec
 * @returns {object} JSON Schema with `$defs` retained for `$ref` resolution
 */
export function specSchema(bundle, spec) {
  const source = normalizeSchemaBundle(bundle);
  const name = specDefName(spec);
  const definition = source.$defs?.[name] || {};

  return {
    $schema: source.$schema,
    title: 'phenix node spec',
    ...definition,
    $defs: source.$defs,
  };
}

/**
 * Inspector schema for an element.
 *
 * The returned schema always describes the *editable working copy* of the
 * element, not its wire form: identifiers, geometry and interface handles are
 * owned by the canvas and are therefore not offered as form fields.
 *
 * @param {object} bundle schema bundle
 * @param {'device'|'switch'|'note'|'group'|'edge'|'network'|'document'} kind
 * @param {object} [context] element being edited (used to pick a spec variant)
 * @returns {object} JSON Schema
 */
export function schemaForKind(bundle, kind, context = {}) {
  const source = normalizeSchemaBundle(bundle);
  const defs = source.$defs || {};
  const base = {
    $schema: source.$schema,
    type: 'object',
    additionalProperties: false,
    $defs: defs,
  };

  switch (kind) {
    case 'device':
      return {
        ...base,
        title: 'Device',
        required: ['hostname', 'spec'],
        properties: {
          hostname: {
            ...(defs.device?.properties?.hostname || { type: 'string' }),
            title: 'Hostname',
            description: 'Unique host name; also written to the node spec.',
          },
          iconKey: {
            ...(defs.iconKey || { type: 'string' }),
            title: 'Icon',
            description: 'Canvas icon. Presentation only.',
          },
          spec: {
            ...(defs[specDefName(context.spec)] || {}),
            title: 'phenix node spec',
          },
        },
      };
    case 'switch':
    case 'network':
      return {
        ...base,
        title: 'Network',
        required: ['name'],
        properties: {
          name: {
            ...(defs.network?.properties?.name || { type: 'string' }),
            title: 'Name',
            description:
              'VLAN name used by every interface attached to this switch.',
          },
          alias: {
            ...(defs.network?.properties?.alias || { type: 'integer' }),
            title: 'VLAN alias',
            description:
              'Optional integer VLAN alias published to an experiment (1-4094).',
          },
          description: {
            ...(defs.network?.properties?.description || { type: 'string' }),
            title: 'Description',
          },
          color: {
            ...(defs.network?.properties?.color || { type: 'string' }),
            title: 'Color',
            description: 'Edge color. Never the only cue for network identity.',
          },
        },
      };
    case 'note':
      return {
        ...base,
        title: 'Note',
        required: ['text'],
        properties: {
          text: {
            ...(defs.note?.properties?.text || { type: 'string' }),
            title: 'Text',
          },
          color: {
            ...(defs.note?.properties?.color || { type: 'string' }),
            title: 'Color',
          },
        },
      };
    case 'group':
      return {
        ...base,
        title: 'Group',
        properties: {
          title: {
            ...(defs.group?.properties?.title || { type: 'string' }),
            title: 'Title',
          },
          color: {
            ...(defs.group?.properties?.color || { type: 'string' }),
            title: 'Color',
          },
          collapsed: {
            ...(defs.group?.properties?.collapsed || { type: 'boolean' }),
            title: 'Collapsed',
          },
        },
      };
    case 'edge':
      return {
        ...base,
        title: 'Connection',
        properties: {
          label: {
            ...(defs.edge?.properties?.label || { type: 'string' }),
            title: 'Label',
          },
        },
      };
    case 'document':
      return {
        ...base,
        title: 'Diagram',
        properties: {
          name: {
            ...(source.properties?.name || { type: 'string' }),
            title: 'Name',
          },
          description: {
            ...(source.properties?.description || { type: 'string' }),
            title: 'Description',
          },
        },
      };
    default:
      return { ...base, title: 'Element', properties: {} };
  }
}
