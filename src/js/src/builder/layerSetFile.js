// Layer set files: one layer set as YAML or JSON (LayerSetFileSchema in
// types/builder/layerset_schema.go). A file holds the set's content without its
// ID, version, times and users, and copies of the custom icons it names.

import YAML from 'js-yaml';

import { count, describeNames, listOf } from './announce.js';
import {
  AliasError,
  ICON_ENTRY_KEYS,
  LAYER_KEYS,
  parseText,
} from './decode.js';
import { MAX_DOCUMENT_ICONS, resolveIcon } from './icons.js';
import { MATCH_CONFLICT_LOWEST, MATCH_CONFLICTS } from './layerMatchers.js';
import { MAX_LAYER_SET_NAME_BYTES, normalizedLayer } from './model.js';
import { utf8Length } from './text.js';
import { layerSetIssues, validateIcons } from './validate.js';
import { fileFieldChecks, isPlainObject, numberedName } from './values.js';

// The format a layer set file names (LayerSetFileSchemaURI).
export const LAYER_SET_FILE_SCHEMA_URI =
  'https://phenix.sandia.gov/schemas/builder/layersets/v1';

// The largest layer set file the Builder reads.
export const MAX_LAYER_SET_FILE_BYTES = 4 * 1024 * 1024;

// The keys of a layer set file.
const LAYER_SET_FILE_KEYS = new Set([
  '$schema',
  'name',
  'description',
  'iconKey',
  'icon',
  'matchConflict',
  'layerOutlines',
  'layers',
  'icons',
]);

// The text fields of a layer set file.
const TEXT_KEYS = ['$schema', 'name', 'description', 'iconKey', 'icon'];

/**
 * Why a layer set file cannot be read: a sentence, and the issues of a file
 * that was read but is not valid, each {code, path, message}.
 */
class LayerSetFileError extends Error {
  /**
   * @param {string} message
   * @param {object[]} [issues]
   */
  constructor(message, issues = []) {
    super(message);
    this.name = 'LayerSetFileError';
    this.issues = issues;
  }
}

// Refuses what the server's strict decoding refuses.
const { rejectUnknown, rejectNonText } = fileFieldChecks(LayerSetFileError);

/**
 * The content of a layer set that the library stores and a file holds, without
 * its ID, version, times and users. Layers come highest ID first, and empty
 * values are left out.
 */
export function layerSetContent(set) {
  const layers = (Array.isArray(set?.layers) ? set.layers : [])
    .filter((layer) => isPlainObject(layer))
    .map(normalizedLayer)
    .sort((a, b) => b.id - a.id);

  return {
    name: typeof set?.name === 'string' ? set.name : '',
    ...(set?.description ? { description: set.description } : {}),
    ...(set?.iconKey ? { iconKey: set.iconKey } : {}),
    ...(set?.icon ? { icon: set.icon } : {}),
    matchConflict: MATCH_CONFLICTS.includes(set?.matchConflict)
      ? set.matchConflict
      : MATCH_CONFLICT_LOWEST,
    ...(set?.layerOutlines === true ? { layerOutlines: true } : {}),
    layers,
  };
}

/** The custom icons a layer set names, each once: its own, then its layers'. */
export function layerSetIconNames(set) {
  const names = new Set();

  for (const entry of [
    set,
    ...(Array.isArray(set?.layers) ? set.layers : []),
  ]) {
    if (typeof entry?.icon === 'string' && entry.icon !== '') {
      names.add(entry.icon);
    }
  }

  return [...names];
}

/**
 * The layer set file of a set, with copies of the custom icons it names, at
 * most MAX_DOCUMENT_ICONS. `missing` names what nothing resolves, and `left`
 * names what the limit leaves out.
 */
export function layerSetFileOf(set, library = null) {
  const carried = {};
  const missing = [];
  const left = [];

  for (const name of layerSetIconNames(set)) {
    const icon = resolveIcon(name, null, library);

    if (!icon || typeof icon.data !== 'string') {
      missing.push(name);
    } else if (Object.keys(carried).length >= MAX_DOCUMENT_ICONS) {
      left.push(name);
    } else {
      carried[name] = { data: icon.data };
    }
  }

  return {
    file: {
      $schema: LAYER_SET_FILE_SCHEMA_URI,
      ...layerSetContent(set),
      ...(Object.keys(carried).length ? { icons: carried } : {}),
    },
    missing,
    left,
  };
}

/**
 * The file name of a layer set, "<name>.layers.yaml", safe for a file system
 * and never hidden.
 */
export function layerSetFileName(name) {
  const base = String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[.-]+|[.-]+$/g, '');

  return `${base || 'layer-set'}.layers.yaml`;
}

/**
 * The YAML text of a layer set file. Long values, such as icon data, stay on
 * one line.
 */
export function layerSetFileText(file) {
  return YAML.dump(file, { noRefs: true, sortKeys: false, lineWidth: -1 });
}

/**
 * Saves a layer set as a YAML file, and returns what was saved and what the
 * page says.
 */
export function downloadLayerSetFile(set, { library = null, saveAs } = {}) {
  const { file, missing, left } = layerSetFileOf(set, library);
  const fileName = layerSetFileName(set?.name);

  // As saveText in exporters.js saves text. That module reads the page's
  // stores, which this one stays clear of.
  saveAs(
    new Blob([layerSetFileText(file)], {
      type: 'text/yaml;charset=utf-8',
    }),
    fileName,
  );

  return {
    fileName,
    file,
    // Whether the file lacks some of the custom icons the set names.
    incomplete: missing.length > 0 || left.length > 0,
    message: downloadedMessage(fileName, file.layers.length, {
      missing,
      left,
    }),
  };
}

/**
 * What the page says once Download selected saved `n` layer sets. `incomplete`
 * names the sets whose files lack some of their custom icons.
 */
export function layerSetsDownloadedMessage(n, incomplete = []) {
  const head = `Downloaded ${count(n, 'layer set')}.`;

  return incomplete.length
    ? `${head} Some custom icons are not in the ${incomplete.length === 1 ? 'file' : 'files'} of ${describeNames(incomplete)}.`
    : head;
}

/** What the page says once a layer set was downloaded. */
export function downloadedMessage(
  fileName,
  layers,
  { missing = [], left = [] } = {},
) {
  const parts = [`Downloaded ${count(layers, 'layer')} to ${fileName}.`];

  if (missing.length) {
    parts.push(
      `It does not carry ${missing.length === 1 ? 'the custom icon' : 'the custom icons'} ${listOf(missing)}, which the server's icon library does not have.`,
    );
  }

  if (left.length) {
    parts.push(
      `It carries at most ${MAX_DOCUMENT_ICONS} custom icons. Thus it does not carry ${listOf(left)}.`,
    );
  }

  return parts.join(' ');
}

// One layer of a file, as the server decodes it. Null fields are no value.
function decodeLayer(layer, index) {
  const path = `layers[${index}]`;

  rejectUnknown(layer, LAYER_KEYS, path);
  ['label', 'iconKey', 'icon', 'color'].forEach((key) =>
    rejectNonText(layer[key], `${path}.${key}`),
  );

  const decoded = { id: layer.id, label: layer.label ?? '' };

  ['iconKey', 'icon', 'color'].forEach((key) => {
    if (layer[key]) {
      decoded[key] = layer[key];
    }
  });

  if (layer.matchers !== undefined && layer.matchers !== null) {
    if (!isPlainObject(layer.matchers)) {
      throw new LayerSetFileError(
        `${path}.matchers: expected an object of patterns by field.`,
      );
    }

    Object.entries(layer.matchers).forEach(([field, pattern]) =>
      rejectNonText(pattern, `${path}.matchers.${field}`),
    );

    if (Object.keys(layer.matchers).length) {
      decoded.matchers = Object.fromEntries(
        Object.entries(layer.matchers).map(([field, pattern]) => [
          field,
          pattern ?? '',
        ]),
      );
    }
  }

  return decoded;
}

/**
 * Whether a parsed value names the layer set file schema. Upload refuses it as
 * a diagram.
 */
export function isLayerSetFileValue(value) {
  return isPlainObject(value) && value.$schema === LAYER_SET_FILE_SCHEMA_URI;
}

/**
 * Decodes a layer set file's value as DecodeLayerSetFile does. It refuses
 * unknown keys and wrong value kinds, and treats null as no value.
 */
export function decodeLayerSetFile(value) {
  rejectUnknown(value, LAYER_SET_FILE_KEYS, 'file');
  TEXT_KEYS.forEach((key) => rejectNonText(value[key], key));
  rejectNonText(value.matchConflict, 'matchConflict');

  if (
    value.layerOutlines !== undefined &&
    value.layerOutlines !== null &&
    typeof value.layerOutlines !== 'boolean'
  ) {
    throw new LayerSetFileError('layerOutlines: expected true or false.');
  }

  if (
    value.layers !== undefined &&
    value.layers !== null &&
    !Array.isArray(value.layers)
  ) {
    throw new LayerSetFileError('layers: expected a list.');
  }

  const layers = (value.layers || []).map(decodeLayer);

  if (value.icons !== undefined && value.icons !== null) {
    if (!isPlainObject(value.icons)) {
      throw new LayerSetFileError('icons: expected an object.');
    }

    Object.values(value.icons).forEach((entry) =>
      rejectUnknown(entry, ICON_ENTRY_KEYS, 'icons'),
    );
  }

  return JSON.parse(
    JSON.stringify({
      $schema: value.$schema ?? '',
      name: value.name ?? '',
      ...(value.description ? { description: value.description } : {}),
      ...(value.iconKey ? { iconKey: value.iconKey } : {}),
      ...(value.icon ? { icon: value.icon } : {}),
      ...(value.matchConflict ? { matchConflict: value.matchConflict } : {}),
      ...(value.layerOutlines ? { layerOutlines: true } : {}),
      layers,
      ...(isPlainObject(value.icons) && Object.keys(value.icons).length
        ? { icons: value.icons }
        : {}),
    }),
  );
}

/**
 * What makes a decoded layer set file unusable (LayerSet.Validate in
 * layersetfile.go), in the server's order and with its codes.
 */
function layerSetFileIssues(file) {
  const issues = [];

  if (file.$schema !== LAYER_SET_FILE_SCHEMA_URI) {
    issues.push({
      code: 'layerset.file.schema-mismatch',
      path: '$schema',
      message: `layer set file schema must be "${LAYER_SET_FILE_SCHEMA_URI}", not "${file.$schema ?? ''}"`,
      level: 'error',
      severity: 'error',
    });
  }

  issues.push(...layerSetIssues(file));

  validateIcons(file.icons, 'icons').forEach((entry) =>
    issues.push({
      code: entry.code,
      path: entry.path,
      message: entry.message,
      level: 'error',
      severity: 'error',
    }),
  );

  return issues;
}

/**
 * Reads a layer set file's text, JSON or YAML by its content. It refuses YAML
 * aliases and a file that does not decode or is not valid. Issues name their
 * place, such as "layers[2].label".
 */
export function parseLayerSetFile(text) {
  const input = String(text ?? '');
  const refuse = (error, issues = []) => ({ ok: false, error, issues });

  if (!input.trim()) {
    return refuse('The file is empty.');
  }

  if (utf8Length(input) > MAX_LAYER_SET_FILE_BYTES) {
    return refuse(
      `The file is larger than the ${MAX_LAYER_SET_FILE_BYTES / 1024 / 1024} MiB limit.`,
    );
  }

  let value;

  try {
    value = parseText(input);
  } catch (error) {
    return refuse(
      error instanceof AliasError
        ? 'The Builder does not support YAML aliases (*name). Replace each alias with the value it repeats. Then upload the file again.'
        : `The file is neither JSON nor YAML: ${error.message}`,
    );
  }

  let file;

  try {
    file = decodeLayerSetFile(value);
  } catch (error) {
    return refuse(`This is not a layer set file. ${error.message}`);
  }

  const issues = layerSetFileIssues(file);

  return issues.length
    ? refuse(
        issues.length === 1
          ? 'You cannot upload this layer set file:'
          : `You cannot upload this layer set file (${issues.length} problems):`,
        issues,
      )
    : { ok: true, file };
}

/**
 * A name for a new layer set that no library set has, ignoring case: the name,
 * else with " (2)", " (3)" and so on, within MAX_LAYER_SET_NAME_BYTES. The
 * library allows duplicates, so the number tells copies apart.
 */
export function uniqueLayerSetName(name, taken) {
  const used = new Set(taken.map((entry) => String(entry).toLowerCase()));
  const base = String(name || '').trim() || 'Layer set';

  if (!used.has(base.toLowerCase())) {
    return base;
  }

  for (let n = 2; ; n += 1) {
    const candidate = numberedName(base, n, MAX_LAYER_SET_NAME_BYTES);

    if (!used.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}
