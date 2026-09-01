// JSON Forms adapter.
//
// The inspector never hard codes fields: the UI schema is generated from the
// (server or bundled) JSON Schema, so fields the server adds appear
// automatically. The adapter also owns the mapping between an element and the
// *working copy* the inspector edits, which is what makes Apply/Cancel possible
// without touching the document on every keystroke.

import { Generate } from '@jsonforms/core';
import { sample } from 'openapi-sampler';

import {
  deviceHandles,
  findNetwork,
  networkOfSwitch,
  setDocumentInfo,
  updateEdge,
  updateNetwork,
  updateNode,
} from '../model.js';
import { normalizeSchemaBundle, schemaForKind } from '../schema.js';

const MULTILINE_KEYS = ['description', 'text', 'comment'];

function decorate(element) {
  if (element.type === 'Control' && typeof element.scope === 'string') {
    const key = element.scope.split('/').pop();

    if (MULTILINE_KEYS.includes(key)) {
      return { ...element, options: { ...element.options, multi: true } };
    }

    return element;
  }

  if (Array.isArray(element.elements)) {
    return { ...element, elements: element.elements.map(decorate) };
  }

  return element;
}

/**
 * Generates a JSON Forms UI schema for an element kind.
 *
 * @param {object} bundle schema bundle
 * @param {string} kind device|switch|network|note|group|edge|document
 * @param {object} [context] element context (picks the phenix spec variant)
 * @returns {object} UI schema
 */
export function uiSchemaForKind(bundle, kind, context = {}) {
  const schema = schemaForKind(normalizeSchemaBundle(bundle), kind, context);

  return decorate(Generate.uiSchema(schema, undefined, undefined, schema));
}

/**
 * Default data for a kind, sampled from the schema so required fields exist.
 *
 * @param {object} bundle
 * @param {string} kind
 * @param {object} [context]
 * @returns {object}
 */
export function sampleForKind(bundle, kind, context = {}) {
  try {
    return sample(schemaForKind(bundle, kind, context), { skipReadOnly: true });
  } catch {
    return {};
  }
}

function clone(value) {
  return JSON.parse(JSON.stringify(value ?? {}));
}

/**
 * Describes what the inspector is editing.
 *
 * A selected switch edits its network, because a switch has no properties of
 * its own beyond the network it publishes.
 *
 * @param {object} doc
 * @param {{type: 'node'|'edge'|'document', id?: string}} selection
 * @returns {{kind: string, title: string, target: object|null, data: object}|null}
 */
export function inspectorTarget(doc, selection) {
  if (!doc || !selection) {
    return null;
  }

  if (selection.type === 'document') {
    return {
      kind: 'document',
      title: 'Diagram',
      target: null,
      data: { name: doc.name || '', description: doc.description || '' },
    };
  }

  if (selection.type === 'edge') {
    const edge = (doc.edges || []).find((entry) => entry.id === selection.id);

    if (!edge) {
      return null;
    }

    return {
      kind: 'edge',
      title: 'Connection',
      target: edge,
      data: { label: edge.label || '' },
    };
  }

  const node = (doc.nodes || []).find((entry) => entry.id === selection.id);

  if (!node) {
    return null;
  }

  switch (node.kind) {
    case 'device':
      return {
        kind: 'device',
        title: `Device ${node.device.hostname}`,
        target: node,
        data: {
          hostname: node.device.hostname,
          iconKey: node.device.iconKey || '',
          spec: clone(node.device.spec),
        },
        interfaces: deviceHandles(node),
      };
    case 'switch': {
      const network = networkOfSwitch(doc, node);

      return {
        kind: 'switch',
        title: `Network ${network ? network.name : ''}`.trim(),
        target: node,
        network,
        data: {
          name: network?.name || '',
          ...(Number.isInteger(network?.alias) ? { alias: network.alias } : {}),
          description: network?.description || '',
          color: network?.color || '',
        },
      };
    }
    case 'note':
      return {
        kind: 'note',
        title: 'Note',
        target: node,
        data: { text: node.note?.text || '', color: node.note?.color || '' },
      };
    case 'group':
      return {
        kind: 'group',
        title: 'Group',
        target: node,
        data: {
          title: node.group?.title || '',
          color: node.group?.color || '',
          collapsed: Boolean(node.group?.collapsed),
        },
      };
    default:
      return null;
  }
}

/**
 * Applies an inspector working copy back onto the document.
 *
 * @param {object} doc
 * @param {{type: string, id?: string}} selection
 * @param {object} data working copy
 * @returns {object} document
 */
export function applyFormData(doc, selection, data) {
  const target = inspectorTarget(doc, selection);

  if (!target) {
    return doc;
  }

  switch (target.kind) {
    case 'document':
      return setDocumentInfo(doc, {
        name: data.name,
        description: data.description,
      });
    case 'edge':
      return updateEdge(doc, target.target.id, { label: data.label ?? '' });
    case 'device':
      return updateNode(doc, target.target.id, {
        device: {
          hostname: data.hostname,
          iconKey: data.iconKey || '',
          spec: clone(data.spec),
        },
      });
    case 'switch': {
      const network =
        target.network || findNetwork(doc, target.target.switch?.networkId);

      if (!network) {
        return doc;
      }

      return updateNetwork(doc, network.id, {
        name: data.name,
        alias: data.alias === undefined ? null : data.alias,
        description: data.description ?? '',
        color: data.color ?? '',
      });
    }
    case 'note':
      return updateNode(doc, target.target.id, {
        note: { text: data.text ?? '', color: data.color ?? '' },
      });
    case 'group':
      return updateNode(doc, target.target.id, {
        group: {
          title: data.title ?? '',
          color: data.color ?? '',
          collapsed: Boolean(data.collapsed),
        },
      });
    default:
      return doc;
  }
}

/**
 * Converts JSON Forms validation errors into inspector messages.
 *
 * @param {object[]} errors ajv errors from JSON Forms
 * @returns {{path: string, message: string}[]}
 */
export function formErrors(errors) {
  return (errors || []).map((error) => ({
    path: error.instancePath || error.dataPath || '',
    message: `${error.instancePath || 'value'} ${error.message}`.trim(),
  }));
}
