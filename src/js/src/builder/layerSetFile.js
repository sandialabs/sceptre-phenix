// Layer set files: one layer set as a file holds it, YAML or JSON
// (LayerSetFileSchema in types/builder/layerset_schema.go). The Layers tab of the
// Drafts page downloads a set as a layer set file, and uploads one as a new
// set of the server's library.
//
// A file is {$schema, name, description?, iconKey?, icon?, matchConflict?,
// layerOutlines?, layers: [{id, label, iconKey?, icon?, color?,
// matchers?}], icons?}: the set's content, without its ID, version, times
// and users, and copies of the custom icons it names, by icon name, as a
// downloaded diagram carries them.

import YAML from 'js-yaml';

import { count, listOf } from './announce.js';
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

// The format a layer set file names (LayerSetFileSchemaURI).
export const LAYER_SET_FILE_SCHEMA_URI =
  'https://phenix.sandia.gov/schemas/builder/layersets/v1';

// The largest layer set file the Builder reads.
export const MAX_LAYER_SET_FILE_BYTES = 4 * 1024 * 1024;

// The keys of a layer set file.
export const LAYER_SET_FILE_KEYS = new Set([
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
export class LayerSetFileError extends Error {
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

function isPlainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/**
 * The content of a layer set that the library stores and a file holds: its
 * name, description, icon, options and layers (each as normalizedLayer
 * gives it, the highest ID first), without its ID, version, times and
 * users. Empty values are left out.
 *
 * @param {object} set a set of the library, or a file
 * @returns {{name: string, description?: string, iconKey?: string, icon?:
 *   string, matchConflict: string, layerOutlines?: boolean, layers:
 *   object[]}}
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

/**
 * The custom icons a layer set names: its own, then those of its layers,
 * each once, in that order.
 *
 * @param {object} set
 * @returns {string[]}
 */
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
 * The layer set file of a set: its content (see layerSetContent), and a
 * copy of every custom icon it names, from the icon library, at most
 * MAX_DOCUMENT_ICONS.
 *
 * @param {object} set a set of the library
 * @param {{lookup: Function}|null} [library] the icon library
 * @returns {{file: object, missing: string[], left: string[]}} file: the
 *   file. missing: the icon names that nothing resolves, which the file
 *   does not carry. left: the icons left out past the most it carries
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
 * The name a layer set file is saved under: the set's name, made safe for a
 * file system, and never hidden.
 *
 * @param {string} name
 * @returns {string} "<name>.layers.yaml"
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
 * The YAML text of a layer set file. Long values, such as an icon's
 * base64, stay on one line.
 *
 * @param {object} file see layerSetFileOf
 * @returns {string}
 */
export function layerSetFileText(file) {
  return YAML.dump(file, { noRefs: true, sortKeys: false, lineWidth: -1 });
}

/**
 * Saves a layer set as a YAML layer set file (see layerSetFileOf).
 *
 * @param {object} set a set of the library
 * @param {object} options
 * @param {{lookup: Function}|null} [options.library] the icon library
 * @param {Function} options.saveAs the saver (file-saver's saveAs)
 * @returns {{fileName: string, file: object, message: string}} what was
 *   saved, and what the page says of it
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
    message: downloadedMessage(fileName, file.layers.length, {
      missing,
      left,
    }),
  };
}

/**
 * What the page says once a layer set was downloaded.
 *
 * @param {string} fileName
 * @param {number} layers how many layers the file holds
 * @param {{missing?: string[], left?: string[]}} [notes] see layerSetFileOf
 * @returns {string}
 */
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
      `It carries at most ${MAX_DOCUMENT_ICONS} custom icons, so it does not carry ${listOf(left)}.`,
    );
  }

  return parts.join(' ');
}

// Refuses a key the server's strict decoding refuses.
function rejectUnknown(value, allowed, path) {
  if (!isPlainObject(value)) {
    throw new LayerSetFileError(`${path}: expected an object.`);
  }

  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new LayerSetFileError(`${path}: unknown field "${key}".`);
    }
  }
}

// Refuses a value of text that is not text. Null is no value.
function rejectNonText(value, path) {
  if (value !== undefined && value !== null && typeof value !== 'string') {
    throw new LayerSetFileError(`${path}: expected text.`);
  }
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
 * Whether a parsed value is a layer set file: it names the layer set file
 * schema. Upload refuses it as a diagram.
 *
 * @param {*} value
 * @returns {boolean}
 */
export function isLayerSetFileValue(value) {
  return isPlainObject(value) && value.$schema === LAYER_SET_FILE_SCHEMA_URI;
}

/**
 * Decodes a layer set file's value as the server does
 * (DecodeLayerSetFile). It refuses unknown keys and values of the wrong
 * kind. A null is the same as no value.
 *
 * @param {*} value
 * @returns {object} the file, without the keys that have no value
 * @throws {LayerSetFileError}
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
 * layersetfile.go), in this order, each with the code the server gives it:
 * another schema, a set the library refuses (see layerSetIssues), and
 * custom icons a document may not carry (see validateIcons).
 *
 * @param {object} file as decodeLayerSetFile returns it
 * @returns {object[]} issues
 */
export function layerSetFileIssues(file) {
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
 * Reads the text of a layer set file (LayerSetFileSchemaURI), JSON or YAML by
 * its content. It refuses YAML aliases, and a file that does not decode or is
 * not valid.
 *
 * @param {string} text
 * @returns {{ok: true, file: object} | {ok: false, error: string, issues:
 *   object[]}} issues name each problem by its code and where it is in the
 *   file, such as "layers[2].label"
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
        ? 'YAML aliases (*name) are not supported. Replace each alias with the value it repeats, and upload the file again.'
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
          ? 'This layer set file cannot be uploaded:'
          : `This layer set file cannot be uploaded (${issues.length} problems):`,
        issues,
      )
    : { ok: true, file };
}

// name followed by " (n)", shortened to stay within
// MAX_LAYER_SET_NAME_BYTES.
function numberedName(name, n) {
  const suffix = ` (${n})`;
  const characters = [...name];

  while (
    characters.length &&
    utf8Length(characters.join('') + suffix) > MAX_LAYER_SET_NAME_BYTES
  ) {
    characters.pop();
  }

  return `${characters.join('').trimEnd()}${suffix}`;
}

/**
 * A name for a new layer set that no set of the library has, ignoring
 * case: the name itself, else it with " (2)", " (3)" and so on, shortened
 * to stay within MAX_LAYER_SET_NAME_BYTES. The library allows two sets of
 * the same name. A new copy takes a number so that the list tells them
 * apart.
 *
 * @param {string} name
 * @param {string[]} taken the names of the library's sets
 * @returns {string}
 */
export function uniqueLayerSetName(name, taken) {
  const used = new Set(taken.map((entry) => String(entry).toLowerCase()));
  const base = String(name || '').trim() || 'Layer set';

  if (!used.has(base.toLowerCase())) {
    return base;
  }

  for (let n = 2; ; n += 1) {
    const candidate = numberedName(base, n);

    if (!used.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}
