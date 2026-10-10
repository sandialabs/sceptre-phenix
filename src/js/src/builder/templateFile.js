// Template files: one collection of node templates as a file holds it, YAML
// or JSON (TemplateFile in types/builder/templatefile.go). The Node Templates
// tab exports templates as one, and imports one into the user's library as a
// new collection; phenix reads every one in its template directory at start
// as a read-only collection of the server's (source 'preloaded').
//
// A file is {$schema, name, description?, templates: [{name, description?,
// device}], icons?}: the collection's name and description, its templates
// without ids, and copies of the custom icons they name, by icon name, as a
// downloaded diagram carries them. Export writes exactly what the server
// reads, so an exported file can be put in the template directory as it is.

import YAML from 'js-yaml';

import { count, listOf } from './announce.js';
import {
  AliasError,
  ICON_ENTRY_KEYS,
  TEMPLATE_DEVICE_KEYS,
  parseText,
} from './decode.js';
import { saveText } from './exporters.js';
import { MAX_DOCUMENT_ICONS, embedIcons } from './icons.js';
import { templateContent } from './templates.js';
import { hasControlCharacters, isBlank, utf8Length } from './text.js';
import {
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  templateIssues,
  validateIcons,
} from './validate.js';

// The format a template file names (TemplateFileSchemaURI).
export const TEMPLATE_FILE_SCHEMA_URI =
  'https://phenix.sandia.gov/schemas/builder/templates/v1';

// The largest file read (MaxTemplateFileBytes), and the most templates one
// holds (MaxTemplateFileTemplates), which is the most a collection holds.
export const MAX_TEMPLATE_FILE_BYTES = 8 * 1024 * 1024;
export const MAX_TEMPLATE_FILE_TEMPLATES = 200;

// The keys of a template file, and of one of its templates, which has no id:
// where it is kept gives it one.
export const TEMPLATE_FILE_KEYS = new Set([
  '$schema',
  'name',
  'description',
  'templates',
  'icons',
]);
export const TEMPLATE_FILE_TEMPLATE_KEYS = new Set([
  'name',
  'description',
  'device',
]);

/**
 * Why a template file cannot be read: a sentence, and the issues of a file
 * that was read but is not valid, each {path, message}.
 */
export class TemplateFileError extends Error {
  /**
   * @param {string} message
   * @param {{path: string, message: string}[]} [issues]
   */
  constructor(message, issues = []) {
    super(message);
    this.name = 'TemplateFileError';
    this.issues = issues;
  }
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

// The key two template names of a file are compared by, as
// templateFileIssues and the server compare them: without the white space
// around it and ignoring case.
function templateNameKey(name) {
  return String(name ?? '')
    .trim()
    .toLowerCase();
}

// name followed by " (n)", shortened to stay within MAX_TEMPLATE_NAME_BYTES.
function numberedName(name, n) {
  const suffix = ` (${n})`;
  const characters = [...name];

  while (
    characters.length &&
    utf8Length(characters.join('') + suffix) > MAX_TEMPLATE_NAME_BYTES
  ) {
    characters.pop();
  }

  return `${characters.join('').trimEnd()}${suffix}`;
}

/**
 * The templates with names a template file may hold. A library may hold two
 * templates whose names differ only in case, of different sources or
 * collections; a file may not. A template whose name one before it has,
 * ignoring case, takes the first of " (2)", " (3)" and so on that no
 * template of the file has, shortened to stay within
 * MAX_TEMPLATE_NAME_BYTES. The others keep theirs.
 *
 * @param {object[]} templates each {name, ...}
 * @returns {{templates: object[], renamed: {from: string, to: string}[]}}
 *   the templates, in their order, and each name that changed
 */
export function uniqueTemplateNames(templates) {
  const given = new Set(
    templates.map((template) => templateNameKey(template.name)),
  );
  const used = new Set();
  const renamed = [];

  const named = templates.map((template) => {
    const key = templateNameKey(template.name);

    if (!key || !used.has(key)) {
      used.add(key);

      return template;
    }

    // Whether a name is one a template before this one took, or one a
    // template of the file has as its own.
    const taken = (candidate) =>
      used.has(templateNameKey(candidate)) ||
      given.has(templateNameKey(candidate));
    let n = 2;
    let name = numberedName(template.name.trim(), n);

    while (taken(name)) {
      n += 1;
      name = numberedName(template.name.trim(), n);
    }

    used.add(templateNameKey(name));
    renamed.push({ from: template.name, to: name });

    return { ...template, name };
  });

  return { templates: named, renamed };
}

/**
 * The template file of templates, as a collection: its name and
 * description, each template's name, description and device, and a copy of
 * every custom icon they name, from the icon library (see embedIcons), at
 * most MAX_DOCUMENT_ICONS. Templates of any source export alike; names that
 * differ only in case are numbered apart (see uniqueTemplateNames), so the
 * file imports and loads.
 *
 * @param {object} collection
 * @param {string} collection.name the name the file gives the collection
 * @param {string} [collection.description]
 * @param {object[]} collection.templates as the library lists them
 * @param {{lookup: Function}|null} [library] the icon library
 * @returns {{file: object, missing: string[], left: string[], renamed:
 *   {from: string, to: string}[]}} the file; the icon names nothing
 *   resolves, which it does not carry; those left out past the most it
 *   carries; and the templates it holds under another name
 */
export function templateFileOf(
  { name, description = '', templates },
  library = null,
) {
  const { templates: named, renamed } = uniqueTemplateNames(
    templates.map(templateContent),
  );
  const { doc, missing, left } = embedIcons({ templates: named }, library);

  return {
    file: {
      $schema: TEMPLATE_FILE_SCHEMA_URI,
      name,
      ...(description ? { description } : {}),
      templates: doc.templates,
      ...(doc.icons ? { icons: doc.icons } : {}),
    },
    missing,
    left,
    renamed,
  };
}

/**
 * The name a template file is saved under: the collection's or template's
 * name, made safe for a file system, and never hidden (the server skips a
 * file whose name starts with a dot).
 *
 * @param {string} name
 * @returns {string} "<name>.templates.yaml"
 */
export function templateFileName(name) {
  const base = String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[.-]+|[.-]+$/g, '');

  return `${base || 'node-templates'}.templates.yaml`;
}

/**
 * The YAML text of a template file, as Export saves it. Long values, such
 * as an icon's base64, stay on one line rather than folded, so the file
 * reads plainly where an administrator puts it.
 *
 * @param {object} file see templateFileOf
 * @returns {string}
 */
export function templateFileText(file) {
  return YAML.dump(file, { noRefs: true, sortKeys: false, lineWidth: -1 });
}

/**
 * Saves templates as a YAML template file (see templateFileOf).
 *
 * @param {object} collection {name, description?, templates}
 * @param {object} options
 * @param {{lookup: Function}|null} options.library the icon library
 * @param {Function} options.saveAs the saver (file-saver's saveAs)
 * @param {Function} [options.BlobCtor]
 * @returns {{fileName: string, file: object, message: string}} what was
 *   saved, and what the page says of it
 */
export function exportTemplateFile(
  collection,
  { library = null, saveAs, BlobCtor } = {},
) {
  const { file, missing, left, renamed } = templateFileOf(collection, library);
  const fileName = templateFileName(collection.name);

  saveText({
    text: templateFileText(file),
    mime: 'text/yaml',
    fileName,
    saveAs,
    BlobCtor,
  });

  return {
    fileName,
    file,
    message: exportedMessage(fileName, file.templates.length, {
      missing,
      left,
      renamed,
    }),
  };
}

/**
 * What the page says once templates were exported.
 *
 * @param {string} fileName
 * @param {number} templates how many the file holds
 * @param {{missing?: string[], left?: string[], renamed?: {from: string,
 *   to: string}[]}} [notes] see templateFileOf
 * @returns {string}
 */
export function exportedMessage(
  fileName,
  templates,
  { missing = [], left = [], renamed = [] } = {},
) {
  const parts = [`Exported ${count(templates, 'template')} to ${fileName}.`];

  if (renamed.length) {
    parts.push(
      `The templates of a file need names that differ even ignoring case, so it holds ${listOf(renamed.map(({ from, to }) => `"${from}" as "${to}"`))}.`,
    );
  }

  if (missing.length) {
    parts.push(
      `It does not carry ${missing.length === 1 ? 'the custom icon' : 'the custom icons'} ${listOf(missing)}, which the server's icon library does not have.`,
    );
  }

  if (left.length) {
    parts.push(
      `It carries at most ${MAX_DOCUMENT_ICONS} custom icons, so ${listOf(left)} ${left.length === 1 ? 'was' : 'were'} left out.`,
    );
  }

  return parts.join(' ');
}

// Refuses a key the server's strict decoding refuses, as decodeDocument does.
function rejectUnknown(value, allowed, path) {
  if (!isPlainObject(value)) {
    throw new TemplateFileError(`${path}: expected an object.`);
  }

  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new TemplateFileError(`${path}: unknown field "${key}".`);
    }
  }
}

// Refuses a value of text that is not text, as the server's decoding does.
function rejectNonText(value, path) {
  if (value !== undefined && value !== null && typeof value !== 'string') {
    throw new TemplateFileError(`${path}: expected text.`);
  }
}

/**
 * Decodes a template file's value as the server does (DecodeTemplateFile):
 * unknown keys and values of the wrong kind are refused. Nulls are none.
 *
 * @param {*} value
 * @returns {object} the file, with what is none left out
 * @throws {TemplateFileError}
 */
export function decodeTemplateFile(value) {
  rejectUnknown(value, TEMPLATE_FILE_KEYS, 'file');
  ['$schema', 'name', 'description'].forEach((key) =>
    rejectNonText(value[key], key),
  );

  if (
    value.templates !== undefined &&
    value.templates !== null &&
    !Array.isArray(value.templates)
  ) {
    throw new TemplateFileError('templates: expected a list.');
  }

  const templates = (value.templates || []).map((template, index) => {
    const path = `templates[${index}]`;

    rejectUnknown(template, TEMPLATE_FILE_TEMPLATE_KEYS, path);
    rejectNonText(template.name, `${path}.name`);
    rejectNonText(template.description, `${path}.description`);

    if (template.device !== undefined && template.device !== null) {
      rejectUnknown(template.device, TEMPLATE_DEVICE_KEYS, `${path}.device`);
    }

    return {
      name: template.name ?? '',
      ...(template.description ? { description: template.description } : {}),
      device: template.device ?? {},
    };
  });

  if (value.icons !== undefined && value.icons !== null) {
    if (!isPlainObject(value.icons)) {
      throw new TemplateFileError('icons: expected an object.');
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
      templates,
      ...(isPlainObject(value.icons) && Object.keys(value.icons).length
        ? { icons: value.icons }
        : {}),
    }),
  );
}

/**
 * What makes a decoded template file unusable (TemplateFile.Issues in
 * templatefile.go), in its order: another schema; a collection name or
 * description a collection may not have; no template, or more than
 * MAX_TEMPLATE_FILE_TEMPLATES; a template a library refuses (see
 * templateIssues); two templates whose names differ only in case; and
 * custom icons a document may not carry (see validateIcons). A template may
 * name an icon the file does not carry.
 *
 * @param {object} file as decodeTemplateFile returns it
 * @returns {{path: string, message: string}[]}
 */
export function templateFileIssues(file) {
  const issues = [];
  const add = (path, message) => issues.push({ path, message });
  const { name = '', description = '', templates = [] } = file;

  if (file.$schema !== TEMPLATE_FILE_SCHEMA_URI) {
    add(
      '$schema',
      `template file schema must be "${TEMPLATE_FILE_SCHEMA_URI}", not "${file.$schema ?? ''}"`,
    );
  }

  if (isBlank(name)) {
    add('name', 'collection name is required');
  } else if (utf8Length(name) > MAX_TEMPLATE_NAME_BYTES) {
    add(
      'name',
      `collection name must be at most ${MAX_TEMPLATE_NAME_BYTES} bytes`,
    );
  } else if (hasControlCharacters(name)) {
    add('name', 'collection name must not contain control characters');
  }

  if (utf8Length(description) > MAX_TEMPLATE_DESCRIPTION_BYTES) {
    add(
      'description',
      `collection description must be at most ${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes`,
    );
  } else if (hasControlCharacters(description)) {
    add(
      'description',
      'collection description must not contain control characters',
    );
  }

  if (templates.length === 0) {
    add('templates', 'a template file holds at least one template');
  } else if (templates.length > MAX_TEMPLATE_FILE_TEMPLATES) {
    add(
      'templates',
      `a template file holds at most ${MAX_TEMPLATE_FILE_TEMPLATES} templates, not ${templates.length}`,
    );
  }

  const names = new Map();

  templates.forEach((template, index) => {
    const path = `templates[${index}]`;

    templateIssues(template, path).forEach((entry) =>
      add(entry.path, entry.message),
    );

    const key =
      typeof template.name === 'string'
        ? template.name.trim().toLowerCase()
        : '';

    if (!key) {
      return;
    }

    if (names.has(key)) {
      add(
        `${path}.name`,
        `template name "${template.name}" is also the name of templates[${names.get(key)}], ignoring case`,
      );
    } else {
      names.set(key, index);
    }
  });

  validateIcons(file.icons, 'icons').forEach((entry) =>
    add(entry.path, entry.message),
  );

  return issues;
}

/**
 * Reads the text of a template file, JSON or YAML by its content, as the
 * server does (ParseTemplateFile): YAML aliases are refused, and so is a
 * file that does not decode or is not valid.
 *
 * @param {string} text
 * @returns {{ok: true, file: object} | {ok: false, error: string, issues:
 *   {path: string, message: string}[]}} issues name each problem by where it
 *   is in the file, such as "templates[2].name"
 */
export function parseTemplateFile(text) {
  const input = String(text ?? '');
  const refuse = (error, issues = []) => ({ ok: false, error, issues });

  if (!input.trim()) {
    return refuse('The file is empty.');
  }

  if (utf8Length(input) > MAX_TEMPLATE_FILE_BYTES) {
    return refuse(
      `The file is larger than the ${MAX_TEMPLATE_FILE_BYTES / 1024 / 1024} MiB limit.`,
    );
  }

  let value;

  try {
    value = parseText(input);
  } catch (error) {
    return refuse(
      error instanceof AliasError
        ? 'YAML aliases (*name) are not supported. Write out each value an alias repeats and import the file again.'
        : `The file is neither JSON nor YAML: ${error.message}`,
    );
  }

  let file;

  try {
    file = decodeTemplateFile(value);
  } catch (error) {
    return refuse(`This is not a template file. ${error.message}`);
  }

  const issues = templateFileIssues(file);

  return issues.length
    ? refuse(
        issues.length === 1
          ? 'This template file cannot be imported:'
          : `This template file cannot be imported (${issues.length} problems):`,
        issues,
      )
    : { ok: true, file };
}

/**
 * A name for a new collection that none of the user's collections has,
 * ignoring case: the name itself, else it with " (2)", " (3)" and so on,
 * shortened to stay within MAX_TEMPLATE_NAME_BYTES.
 *
 * @param {string} name
 * @param {string[]} taken the names of the user's collections
 * @returns {string}
 */
export function uniqueCollectionName(name, taken) {
  const used = new Set(taken.map((entry) => String(entry).toLowerCase()));

  if (!used.has(name.toLowerCase())) {
    return name;
  }

  for (let n = 2; ; n += 1) {
    const candidate = numberedName(name, n);

    if (!used.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}

/**
 * Why a template file cannot be imported into the user's library, or ''
 * when it can: a library holds so many templates and collections at most.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {number} adding how many templates the file holds
 * @returns {string}
 */
export function importProblem(library, adding) {
  const limits = library?.limits || {};
  const own = (source) =>
    (source || []).filter((item) => item?.source === 'own').length;
  const templates = limits.templates ?? MAX_TEMPLATE_FILE_TEMPLATES;
  const collections = limits.collections ?? 50;

  if (own(library?.items) + adding > templates) {
    return `Your library can hold ${templates} templates, and this file would take it past that. Delete some first.`;
  }

  return own(library?.collections) >= collections
    ? `Your library holds ${collections} collections, the most it can. Delete one first.`
    : '';
}

/**
 * What the page says once a template file was imported.
 *
 * @param {string} collection the new collection's name
 * @param {number} templates how many templates it holds
 * @returns {string}
 */
export function importedMessage(collection, templates) {
  return `Imported ${count(templates, 'template')} as collection ${collection}.`;
}
