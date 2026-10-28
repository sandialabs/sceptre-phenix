// Template files: one collection of node templates as YAML or JSON
// (TemplateFile in types/builder/templatefile.go). Download writes exactly
// what the server reads, so a downloaded file can go in the template directory
// as is.

import YAML from 'js-yaml';

import { count, listOf } from './announce.js';
import {
  AliasError,
  DOCUMENT_KEYS,
  ICON_ENTRY_KEYS,
  TEMPLATE_DEVICE_KEYS,
  parseText,
} from './decode.js';
import { saveText } from './exporters.js';
import { foldKey } from './goCase.js';
import { MAX_DOCUMENT_ICONS, embedIcons } from './icons.js';
import { templateContent } from './templates.js';
import {
  hasControlCharacters,
  isBlank,
  trimSpace,
  utf8Length,
} from './text.js';
import {
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  templateIssues,
  validateIcons,
} from './validate.js';
import { fileFieldChecks, isPlainObject, numberedName } from './values.js';

// The format a template file names (TemplateFileSchemaURI).
export const TEMPLATE_FILE_SCHEMA_URI =
  'https://phenix.sandia.gov/schemas/builder/templates/v1';

// The largest file read (MaxTemplateFileBytes), and the most templates one
// holds (MaxTemplateFileTemplates), which is the most a collection holds.
export const MAX_TEMPLATE_FILE_BYTES = 8 * 1024 * 1024;
const MAX_TEMPLATE_FILE_TEMPLATES = 200;

// The keys of a template file, and of one of its templates. A template in a
// file has no id. The place that stores it gives it one.
const TEMPLATE_FILE_KEYS = new Set([
  '$schema',
  'name',
  'description',
  'templates',
  'icons',
]);
const TEMPLATE_FILE_TEMPLATE_KEYS = new Set(['name', 'description', 'device']);

/**
 * Why a template file cannot be read: a sentence, and the issues of a file
 * that was read but is not valid, each {path, message}.
 */
class TemplateFileError extends Error {
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

// Refuses what the server's strict decoding refuses, as decodeDocument does.
const { rejectUnknown, rejectNonText } = fileFieldChecks(TemplateFileError);

// The key used to compare two template names of a file, as
// templateFileIssues and the server compare them: without the white space
// around it and without case, as Go's foldKey gives it.
function templateNameKey(name) {
  return foldKey(name);
}

// The templates with names unique ignoring case, as a file requires. A later
// duplicate takes the first free " (2)", " (3)" suffix within
// MAX_TEMPLATE_NAME_BYTES. Returns the templates and each rename.
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
    let name = numberedName(
      trimSpace(template.name),
      n,
      MAX_TEMPLATE_NAME_BYTES,
    );

    while (taken(name)) {
      n += 1;
      name = numberedName(trimSpace(template.name), n, MAX_TEMPLATE_NAME_BYTES);
    }

    used.add(templateNameKey(name));
    renamed.push({ from: template.name, to: name });

    return { ...template, name };
  });

  return { templates: named, renamed };
}

// The template file of a collection, with copies of the custom icons it names
// (at most MAX_DOCUMENT_ICONS). Names that differ only in case get numbers
// (uniqueTemplateNames), so the file imports and loads.
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

/** The file name of a template file, safe and never hidden: the server skips dot files. */
export function templateFileName(name) {
  const base = String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[.-]+|[.-]+$/g, '');

  return `${base || 'node-templates'}.templates.yaml`;
}

/** The YAML text of a template file. Long values such as base64 icons stay on one line. */
function templateFileText(file) {
  return YAML.dump(file, { noRefs: true, sortKeys: false, lineWidth: -1 });
}

/** Saves templates as a YAML template file and returns what the page says of it. */
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

/** What the page says once templates were exported. */
function exportedMessage(
  fileName,
  templates,
  { missing = [], left = [], renamed = [] } = {},
) {
  const parts = [`Downloaded ${count(templates, 'template')} to ${fileName}.`];

  if (renamed.length) {
    parts.push(
      `The templates of a file need different names, ignoring case. Thus the file holds ${listOf(renamed.map(({ from, to }) => `"${from}" as "${to}"`))}.`,
    );
  }

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

// The keys that a diagram document has and a template file does not.
const DIAGRAM_ONLY_KEYS = [...DOCUMENT_KEYS].filter(
  (key) => !TEMPLATE_FILE_KEYS.has(key),
);

// Whether a parsed value is a template file and not a diagram, so Upload
// refuses it. A diagram that carries its own templates names the diagram
// schema, so it is not one.
export function isTemplateFileValue(value) {
  if (!isPlainObject(value)) {
    return false;
  }

  if (value.$schema !== undefined && value.$schema !== null) {
    return value.$schema === TEMPLATE_FILE_SCHEMA_URI;
  }

  return (
    Array.isArray(value.templates) &&
    !DIAGRAM_ONLY_KEYS.some((key) => Object.hasOwn(value, key))
  );
}

// Decodes a template file's value as DecodeTemplateFile does: it refuses
// unknown keys and wrong kinds, and a null is the same as no value.
function decodeTemplateFile(value) {
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

// What makes a decoded template file unusable, in the order and with the codes
// of TemplateFile.Issues in templatefile.go. A template may name an icon the
// file does not carry.
export function templateFileIssues(file) {
  const issues = [];
  const add = (code, path, message) =>
    issues.push({ code, path, message, level: 'error', severity: 'error' });
  const { name = '', description = '', templates = [] } = file;

  if (file.$schema !== TEMPLATE_FILE_SCHEMA_URI) {
    add(
      'template.file.schema-mismatch',
      '$schema',
      `template file schema must be "${TEMPLATE_FILE_SCHEMA_URI}", not "${file.$schema ?? ''}"`,
    );
  }

  if (isBlank(name)) {
    add(
      'template.collection-name.required',
      'name',
      'a collection needs a name',
    );
  } else if (utf8Length(name) > MAX_TEMPLATE_NAME_BYTES) {
    add(
      'template.collection-name.too-long',
      'name',
      `collection name must be at most ${MAX_TEMPLATE_NAME_BYTES} bytes`,
    );
  } else if (hasControlCharacters(name)) {
    add(
      'template.collection-name.control',
      'name',
      'collection name must not contain control characters',
    );
  }

  if (utf8Length(description) > MAX_TEMPLATE_DESCRIPTION_BYTES) {
    add(
      'template.collection-description.too-long',
      'description',
      `collection description must be at most ${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes`,
    );
  } else if (hasControlCharacters(description)) {
    add(
      'template.collection-description.control',
      'description',
      'collection description must not contain control characters',
    );
  }

  if (templates.length === 0) {
    add(
      'template.file.empty',
      'templates',
      'a template file holds at least one template',
    );
  } else if (templates.length > MAX_TEMPLATE_FILE_TEMPLATES) {
    add(
      'template.file.too-many',
      'templates',
      `a template file holds at most ${MAX_TEMPLATE_FILE_TEMPLATES} templates, not ${templates.length}`,
    );
  }

  const names = new Map();

  templates.forEach((template, index) => {
    const path = `templates[${index}]`;

    templateIssues(template, path).forEach((entry) =>
      add(entry.code, entry.path, entry.message),
    );

    const key =
      typeof template.name === 'string' ? templateNameKey(template.name) : '';

    if (!key) {
      return;
    }

    if (names.has(key)) {
      add(
        'template.name.duplicate',
        `${path}.name`,
        `template name "${template.name}" is also the name of templates[${names.get(key)}], ignoring case`,
      );
    } else {
      names.set(key, index);
    }
  });

  validateIcons(file.icons, 'icons').forEach((entry) =>
    add(entry.code, entry.path, entry.message),
  );

  return issues;
}

// Reads a template file's text, JSON or YAML by content, as ParseTemplateFile
// does. It refuses YAML aliases and a file that does not decode or is invalid.
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
        ? 'The Builder does not support YAML aliases (*name). Replace each alias with the value it repeats. Then import the file again.'
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
          ? 'You cannot import this template file:'
          : `You cannot import this template file (${issues.length} problems):`,
        issues,
      )
    : { ok: true, file };
}

// A new collection name unique ignoring case: the name, else with " (2)",
// " (3)" and so on, within MAX_TEMPLATE_NAME_BYTES.
export function uniqueCollectionName(name, taken) {
  const used = new Set(taken.map((entry) => String(entry).toLowerCase()));

  if (!used.has(name.toLowerCase())) {
    return name;
  }

  for (let n = 2; ; n += 1) {
    const candidate = numberedName(name, n, MAX_TEMPLATE_NAME_BYTES);

    if (!used.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}

/** Why a template file cannot be imported (library limits), or '' when it can. */
export function importProblem(library, adding) {
  const limits = library?.limits || {};
  const held = (source) =>
    (source || []).filter((item) => item?.source === 'library').length;
  const templates = limits.templates ?? MAX_TEMPLATE_FILE_TEMPLATES;
  const collections = limits.collections ?? 50;

  if (held(library?.items) + adding > templates) {
    return `The library can hold ${templates} templates. This file would put more than ${templates} in it. Delete some templates first.`;
  }

  return held(library?.collections) >= collections
    ? `The library holds ${collections} collections, which is its limit. Delete a collection first.`
    : '';
}

/** What the page says once a template file was imported. */
export function importedMessage(collection, templates) {
  return `Imported ${count(templates, 'template')} as collection ${collection}.`;
}
