// Document validation, mirroring phenix/types/builder validate.go.
//
// The rules here are the same rules the server enforces. They are reported
// as issues that the Inspector and outline can show before a save. Issues
// use the server's `path` form (nodes[0].device.hostname), so a server
// rejection and a local rejection read the same. An issue about a node, a
// connection or a network also carries its id (nodeId, edgeId, networkId),
// which still finds it after an edit moved the indexes in the path.
//
// testdata/validation-corpus.json in types/builder holds documents that both
// sides must agree on. Two errors are the editor's own, stricter than the
// server: an interface connection point with no interface of that name in
// the node's spec, and a spec hostname that is not the device's. The server
// publishes such a device with the device's hostname and without that
// connection, which the editor would not show. One check is only the
// server's: that the PNG of a custom icon decodes (see icons.js).

import { count } from './announce.js';
import { isIconKey } from './catalog.js';
import { canonicalJSON, isDigest } from './digest.js';
import {
  ICON_NAME,
  MAX_DOCUMENT_ICONS,
  MAX_ICON_BYTES,
  MAX_ICON_NAME_BYTES,
  decodeIconData,
  iconPNGProblem,
} from './icons.js';
import { MAX_USER_BYTES } from './limits.js';
import {
  BORDER_STYLES,
  HEX_COLOR,
  ICON_SIZES,
  LINE_STYLES,
  MAX_DIAGRAM_NOTE_BYTES,
  MAX_DIAGRAM_NOTES,
  MAX_LINE_POINTS,
  MIN_LINE_POINTS,
  NODE_KINDS,
  PURDUE_LEVELS,
  SCHEMA_REVISION,
  SCHEMA_URI,
  SHAPE_FIGURES,
  deviceHandles,
  edgeEndpoints,
  metadataOf,
  sizeOf,
  specInterfaces,
} from './model.js';
import {
  hasControlCharacters,
  hasControlCharactersInLines,
  utf8Length,
} from './text.js';

export { MAX_DIAGRAM_NOTE_BYTES, MAX_DIAGRAM_NOTES, MAX_USER_BYTES };

export const MAX_VLAN_ALIAS = 4094;

// The longest document name the draft service records as a title, in UTF-8
// bytes (MaxNameBytes in validate.go).
export const MAX_NAME_BYTES = 512;

// Bounds on the source config annotations a document carries only to show
// them (MaxAnnotations and MaxAnnotationBytes in validate.go): how many, and
// their keys and values together in UTF-8 bytes. A key is bounded like the
// document name.
export const MAX_ANNOTATIONS = 100;
export const MAX_ANNOTATION_BYTES = 256 * 1024;

// Bounds on device templates (MaxTemplates, MaxTemplateNameBytes,
// MaxTemplateDescriptionBytes and MaxTemplateDeviceBytes in template.go):
// how many a document may carry, the UTF-8 bytes of a name and of a
// description, and the bytes of a template's device as the server encodes
// it as JSON.
export const MAX_TEMPLATES = 50;
export const MAX_TEMPLATE_NAME_BYTES = 128;
export const MAX_TEMPLATE_DESCRIPTION_BYTES = 1024;
export const MAX_TEMPLATE_DEVICE_BYTES = 16 * 1024;

// The apiVersion of the Scenario configs the Scenario dialog uploads: the
// latest version that phenix stores scenarios at. The server also stores a
// scenario of another version, but the dialog reads only the apps of this
// version.
export const SCENARIO_API_VERSION = 'phenix.sandia.gov/v2';

// The most Scenario configs a document may list, and the longest name of
// one in UTF-8 bytes (MaxScenarios and MaxScenarioNameBytes in
// document.go).
export const MAX_SCENARIOS = 20;
export const MAX_SCENARIO_NAME_BYTES = 256;

// A name phenix gives a config (configNamePattern in schema.go, NameRegex
// in api/config).
const CONFIG_NAME = /^[A-Za-z0-9_@.-]+$/;

// The characters a name must not contain: those validate.go refuses with
// strings.ContainsAny(name, " \t\n").
const WHITESPACE = /[ \t\n]/;

// minimega's wildcard VM target, and the hostname phenix image bakes into the
// images it builds (types/version/v1/hostname.go).
const MINIMEGA_WILDCARD_VM = 'all';
const PHENIX_HOSTNAME = 'phenix';

// A canonical RFC 4122 UUID with a known version and the RFC 4122 variant, in
// either case (IsUUID in types/builder/uuid.go).
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

// The white space strings.TrimSpace trims (unicode.IsSpace). String's own
// trim differs by two characters: it keeps U+0085 and trims U+FEFF.
const GO_SPACE =
  '\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000';
const GO_SPACE_AROUND = new RegExp(`^[${GO_SPACE}]+|[${GO_SPACE}]+$`, 'g');

// Text without the white space around it, as strings.TrimSpace gives it,
// so that what is blank to the server is blank here.
function trimSpace(text) {
  return text.replace(GO_SPACE_AROUND, '');
}

// foldKey in types/builder/spec.go.
function fold(value) {
  return trimSpace(String(value ?? '')).toLowerCase();
}

function finite(value) {
  return typeof value === 'number' && Number.isFinite(value);
}

// Adds the issue of the rule `code` (a code of the registry that Go
// generates, schema/codes.json) at path. `severity` repeats `level`, which
// existing callers read, with the name that the server's issues use.
function issue(issues, code, path, message, level = 'error', extra = {}) {
  issues.push({ code, path, message, level, severity: level, ...extra });
}

// The longest value a message quotes, in UTF-8 bytes.
const QUOTED_BYTES = 64;

// What Go writes as it is between quotes: letters, marks, numbers,
// punctuation, symbols and the space (strconv.IsPrint). Go and the browser
// each know a version of Unicode. So a character that is new to one of them
// can still be written differently.
const PRINTABLE = /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]$/u;

// The characters Go's %q writes as a backslash and a letter.
const GO_ESCAPES = new Map([
  ['\x07', '\\a'],
  ['\b', '\\b'],
  ['\f', '\\f'],
  ['\n', '\\n'],
  ['\r', '\\r'],
  ['\t', '\\t'],
  ['\v', '\\v'],
]);

// Half of a surrogate pair without its other half. The server never sees
// one: JSON gives it U+FFFD in its place.
function isLoneSurrogate(ch) {
  return ch.length === 1 && ch >= '\ud800' && ch <= '\udfff';
}

// Text between double quotes as Go's %q writes it (strconv.Quote), so that a
// message that quotes a value reads the same here and from the server. A
// character that does not print is written as an escape.
function goQuoted(text) {
  let out = '';

  for (const ch of text) {
    const code = ch.codePointAt(0);
    const hex = (digits) => code.toString(16).padStart(digits, '0');

    if (ch === '"' || ch === '\\') {
      out += `\\${ch}`;
    } else if (isLoneSurrogate(ch)) {
      out += '\ufffd';
    } else if (PRINTABLE.test(ch)) {
      out += ch;
    } else if (GO_ESCAPES.has(ch)) {
      out += GO_ESCAPES.get(ch);
    } else if (code < 0x20 || code === 0x7f) {
      out += `\\x${hex(2)}`;
    } else if (code < 0x10000) {
      out += `\\u${hex(4)}`;
    } else {
      out += `\\U${hex(8)}`;
    }
  }

  return `"${out}"`;
}

// Text cut to QUOTED_BYTES bytes of whole characters (truncate in
// decode.go).
function truncated(text) {
  if (utf8Length(text) <= QUOTED_BYTES) {
    return text;
  }

  let kept = '';
  let bytes = 0;

  for (const ch of text) {
    bytes += utf8Length(ch);

    if (bytes > QUOTED_BYTES) {
      break;
    }

    kept += ch;
  }

  return `${kept}...`;
}

// A value as a message quotes it, cut when long.
function quoted(value) {
  return goQuoted(truncated(String(value)));
}

// Whether a text field is not set: absent, empty, or null, which Go decodes
// as no value.
function unset(value) {
  return value === undefined || value === null || value === '';
}

function isObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

// The icon key of a device, a group or a template's device (validateIconKey
// in validate.go): none, or one of the registry. code is the code of the
// owner.
function validateIconKey(key, path, issues, code) {
  if (!unset(key) && !isIconKey(key)) {
    issue(issues, code, path, `unknown icon key "${key}"`);
  }
}

// Why a color is not a valid outline or fill color (colorProblem in
// validate.go), or ''. A valid color is none, or #rrggbb.
function colorProblem(color) {
  return unset(color) || (typeof color === 'string' && HEX_COLOR.test(color))
    ? ''
    : `color ${quoted(color)} must be a hex color such as #2f6fbf`;
}

// The outline and fill colors of a device, a switch, a shape or a template's
// device, whose code is code.
function validateColors(payload, path, issues, code) {
  ['outlineColor', 'fillColor'].forEach((key) => {
    const problem = colorProblem(payload?.[key]);

    if (problem) {
      issue(issues, code, `${path}.${key}`, problem);
    }
  });
}

// Why an icon size is not one that the document, a device, a switch, a
// group or a template's device may name (iconSizeProblem in validate.go),
// or ''. A valid size is none, or one of ICON_SIZES.
function iconSizeProblem(size) {
  return unset(size) || ICON_SIZES.includes(size)
    ? ''
    : `unknown icon size ${quoted(size)} (expected one of ${ICON_SIZES.join(', ')})`;
}

function validateIconSize(size, path, issues, code) {
  const problem = iconSizeProblem(size);

  if (problem) {
    issue(issues, code, path, problem);
  }
}

// Why a Purdue level is not one that a device, a switch or a template's
// device may name (purdueLevelProblem in validate.go), or ''. A valid level
// is none, or one of PURDUE_LEVELS.
function purdueLevelProblem(level) {
  return unset(level) || PURDUE_LEVELS.includes(level)
    ? ''
    : `unknown Purdue layer ${quoted(level)} (expected one of ${PURDUE_LEVELS.join(', ')})`;
}

function validatePurdueLevel(level, path, issues, code) {
  const problem = purdueLevelProblem(level);

  if (problem) {
    issue(issues, code, path, problem);
  }
}

// The line style of a network, a connection or a line (validateLineStyle in
// validate.go): none, or one of LINE_STYLES.
function validateLineStyle(style, path, issues, code) {
  if (!unset(style) && !LINE_STYLES.includes(style)) {
    issue(
      issues,
      code,
      path,
      `unknown line style ${quoted(style)} (expected one of ${LINE_STYLES.join(', ')})`,
    );
  }
}

/**
 * Why text is not an icon name, in the server's words (IconNameProblem in
 * customicons.go), or ''. An icon name is 1 to MAX_ICON_NAME_BYTES letters,
 * digits, "_", "@", "." and "-", and is neither "." nor "..".
 *
 * @param {*} name
 * @returns {string}
 */
export function iconNameProblem(name) {
  const text = typeof name === 'string' ? name : String(name ?? '');

  if (typeof name !== 'string' || !ICON_NAME.test(name)) {
    return `icon name ${quoted(text)} must be 1 to ${MAX_ICON_NAME_BYTES} letters, digits, "_", "@", "." or "-"`;
  }

  if (name === '.' || name === '..') {
    return `icon name ${quoted(name)} must not be "." or ".."`;
  }

  return '';
}

// The custom icon a device, a group, an icon node or a template names
// (validateIconRef in validate.go): none, or an icon name. A name that the
// document does not carry is allowed: the server's icon library resolves it.
function validateIconRef(name, path, issues, code) {
  if (unset(name)) {
    return;
  }

  const problem = iconNameProblem(name);

  if (problem) {
    issue(issues, code, path, problem);
  }
}

// Data that is already known to be a PNG the Builder accepts. Validation
// runs on every edit, so data that was checked is not decoded again.
const checkedIcons = new Set();
const MAX_CHECKED_ICONS = 256;

/**
 * Checks the custom icons a document carries (ValidateIcons in
 * customicons.go): at most MAX_DOCUMENT_ICONS, each key an icon name (see
 * iconNameProblem), each data strict base64 of a PNG the Builder accepts
 * (see iconPNGProblem). An icon that nothing uses is valid: the editor
 * drops it on its next edit.
 *
 * @param {object|null|undefined} icons by name, {data}
 * @param {string} [path] the path the issues are reported at
 * @returns {{code: string, path: string, message: string, level: 'error',
 *   severity: 'error'}[]} in the order of the keys
 */
export function validateIcons(icons, path = 'icons') {
  const issues = [];

  // null is none, as the server decodes it.
  if (icons === undefined || icons === null) {
    return issues;
  }

  // The server refuses any other value when it decodes the document.
  if (!isObject(icons)) {
    issue(
      issues,
      'icon.list.not-object',
      path,
      'custom icons must be an object of icons by name',
    );

    return issues;
  }

  const keys = Object.keys(icons).sort();

  if (keys.length > MAX_DOCUMENT_ICONS) {
    issue(
      issues,
      'icon.list.too-many',
      path,
      `at most ${MAX_DOCUMENT_ICONS} custom icons are allowed, not ${keys.length}`,
    );
  }

  keys.forEach((key) => {
    const { data } = isObject(icons[key]) ? icons[key] : {};
    const problem = iconNameProblem(key);

    if (problem) {
      issue(issues, 'icon.name.invalid', path, problem);
    }

    if (typeof data === 'string' && checkedIcons.has(data)) {
      return;
    }

    const bytes = decodeIconData(data);

    if (!bytes) {
      issue(
        issues,
        'icon.data.invalid',
        path,
        `icon ${quoted(key)} data must be base64 of at most ${MAX_ICON_BYTES} bytes`,
      );

      return;
    }

    const pngProblem = iconPNGProblem(bytes);

    if (pngProblem) {
      issue(
        issues,
        'icon.png.invalid',
        path,
        `icon ${quoted(key)} is not an accepted PNG: ${pngProblem}`,
      );

      return;
    }

    if (checkedIcons.size >= MAX_CHECKED_ICONS) {
      checkedIcons.clear();
    }

    checkedIcons.add(data);
  });

  return issues;
}

// The length of a template's device as the server encodes it: without the
// icon and color fields that are not set, which the server omits.
function templateDeviceBytes(device) {
  const encoded = Object.fromEntries(
    Object.entries(device).filter(
      ([key, value]) => key === 'spec' || !unset(value),
    ),
  );

  return utf8Length(canonicalJSON(encoded));
}

/**
 * What makes a template unusable (Template.Issues in template.go):
 * - a name that is blank, longer than MAX_TEMPLATE_NAME_BYTES or holds
 *   control characters
 * - a description longer than MAX_TEMPLATE_DESCRIPTION_BYTES or that holds
 *   control characters
 * - a device without a spec, or whose spec has no general.hostname, a blank
 *   one or one with whitespace
 * - an unknown icon key
 * - a custom icon that is not an icon name
 * - an unknown icon size
 * - a color that is not #rrggbb
 * - a device longer than MAX_TEMPLATE_DEVICE_BYTES as JSON
 * The spec is not checked against the phenix schema here, as a device's
 * spec is not. Neither is the id, whose form depends on where the template
 * is kept. validateDocument checks it for a document's templates.
 *
 * @param {object} template {id, name, description?, device}
 * @param {string} path the path of the template, which the issues' paths
 *   start with: templates[0]
 * @returns {{code: string, path: string, message: string, level: 'error',
 *   severity: 'error'}[]}
 */
export function templateIssues(template, path) {
  const issues = [];
  const { name, description, device } = isObject(template) ? template : {};

  if (typeof name !== 'string' || !trimSpace(name)) {
    issue(
      issues,
      'template.name.required',
      `${path}.name`,
      'template name is required',
    );
  } else if (utf8Length(name) > MAX_TEMPLATE_NAME_BYTES) {
    issue(
      issues,
      'template.name.too-long',
      `${path}.name`,
      `template name must be at most ${MAX_TEMPLATE_NAME_BYTES} bytes`,
    );
  } else if (hasControlCharacters(name)) {
    issue(
      issues,
      'template.name.control',
      `${path}.name`,
      'template name must not contain control characters',
    );
  }

  if (!unset(description)) {
    if (
      typeof description !== 'string' ||
      utf8Length(description) > MAX_TEMPLATE_DESCRIPTION_BYTES
    ) {
      issue(
        issues,
        'template.description.too-long',
        `${path}.description`,
        `template description must be at most ${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes`,
      );
    } else if (hasControlCharacters(description)) {
      issue(
        issues,
        'template.description.control',
        `${path}.description`,
        'template description must not contain control characters',
      );
    }
  }

  if (!isObject(device) || !isObject(device.spec)) {
    issue(
      issues,
      'template.spec.required',
      `${path}.device.spec`,
      'template device spec is required',
    );
  } else {
    const general = device.spec.general;
    const hostname =
      isObject(general) && typeof general.hostname === 'string'
        ? general.hostname
        : '';

    if (!trimSpace(hostname)) {
      issue(
        issues,
        'template.hostname.required',
        `${path}.device.spec.general.hostname`,
        'template hostname is required',
      );
    } else if (WHITESPACE.test(hostname)) {
      issue(
        issues,
        'template.hostname.whitespace',
        `${path}.device.spec.general.hostname`,
        `template hostname ${quoted(hostname)} must not contain whitespace`,
      );
    }
  }

  if (!isObject(device)) {
    return issues;
  }

  validateIconKey(
    device.iconKey,
    `${path}.device.iconKey`,
    issues,
    'template.icon-key.unknown',
  );
  validateIconRef(
    device.icon,
    `${path}.device.icon`,
    issues,
    'template.icon.invalid',
  );
  validateIconSize(
    device.iconSize,
    `${path}.device.iconSize`,
    issues,
    'template.icon-size.unknown',
  );
  validateColors(device, `${path}.device`, issues, 'template.color.invalid');
  validatePurdueLevel(
    device.purdueLevel,
    `${path}.device.purdueLevel`,
    issues,
    'template.purdue-level.unknown',
  );

  const size = templateDeviceBytes(device);

  if (size > MAX_TEMPLATE_DEVICE_BYTES) {
    issue(
      issues,
      'template.device.too-large',
      `${path}.device`,
      `template device must take at most ${MAX_TEMPLATE_DEVICE_BYTES} bytes as JSON, not ${size}`,
    );
  }

  return issues;
}

// The document's templates (validateTemplates in validate.go): how many,
// the id of each (unique among them), and what templateIssues checks,
// which includes the custom icon each names.
function validateTemplates(doc, issues) {
  // null is none, as the server decodes it.
  if (doc.templates === undefined || doc.templates === null) {
    return;
  }

  // The server refuses any other value when it decodes the document.
  if (!Array.isArray(doc.templates)) {
    issue(
      issues,
      'template.list.not-list',
      'templates',
      'templates must be a list of templates',
    );

    return;
  }

  if (doc.templates.length > MAX_TEMPLATES) {
    issue(
      issues,
      'template.list.too-many',
      'templates',
      `at most ${MAX_TEMPLATES} templates are allowed, not ${doc.templates.length}`,
    );
  }

  const seenIds = new Map();

  doc.templates.forEach((template, index) => {
    const path = `templates[${index}]`;
    const id = template?.id;

    if (!trimSpace(String(id || ''))) {
      issue(
        issues,
        'template.id.required',
        `${path}.id`,
        'template ID is required',
      );
    } else if (seenIds.has(fold(id))) {
      issue(
        issues,
        'template.id.duplicate',
        `${path}.id`,
        `duplicate template ID ${goQuoted(String(id))} (also templates[${seenIds.get(fold(id))}])`,
      );
    } else {
      seenIds.set(fold(id), index);
    }

    validateUUID(issues, `${path}.id`, 'template', id, 'template.id.invalid');
    issues.push(...templateIssues(template, path));
  });
}

// Every identifier is a UUID. crypto.randomUUID makes the editor's, and the
// server derives name-based ones (validateID in validate.go). A missing id
// is reported where it is required. code is the code of the kind.
function validateUUID(issues, path, kind, id, code) {
  if (trimSpace(String(id || '')) && !UUID_PATTERN.test(String(id))) {
    issue(
      issues,
      code,
      path,
      `${kind} ID ${goQuoted(String(id))} is not a valid UUID`,
    );
  }
}

// A user the document metadata names (validateUser in validate.go): text of
// at most MAX_USER_BYTES bytes without control characters. Empty or null is
// none, as Go decodes it.
function validateUser(issues, path, user) {
  if (user === undefined || user === null) {
    return;
  }

  if (typeof user !== 'string') {
    issue(issues, 'metadata.user.not-text', path, `${path} must be a string`);
  } else if (utf8Length(user) > MAX_USER_BYTES) {
    issue(
      issues,
      'metadata.user.too-long',
      path,
      `${path} must be at most ${MAX_USER_BYTES} bytes`,
    );
  } else if (hasControlCharacters(user)) {
    issue(
      issues,
      'metadata.user.control',
      path,
      `${path} must not contain control characters`,
    );
  }
}

const TIME_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})Z$/;

/**
 * Whether text is a time in exactly the one form a document header holds
 * (IsTime in validate.go): UTC, whole seconds, a literal Z, and a date and
 * time of day that exist. The fields are checked here, not by Date, whose
 * parsing of such text differs between browsers (hour 24, February 30).
 *
 * @param {string} text
 * @returns {boolean}
 */
export function isTime(text) {
  const match = TIME_PATTERN.exec(String(text));

  if (!match) {
    return false;
  }

  const [year, month, day, hour, minute, second] = match.slice(1).map(Number);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

  return (
    month >= 1 &&
    month <= 12 &&
    day >= 1 &&
    day <= days[month - 1] &&
    hour <= 23 &&
    minute <= 59 &&
    second <= 59
  );
}

// A time of the document header, when it has one (validateTime in
// validate.go).
function validateTime(issues, path, value) {
  if (value === undefined || value === null || value === '') {
    return;
  }

  if (typeof value !== 'string') {
    issue(issues, 'metadata.time.not-text', path, `${path} must be a string`);
  } else if (!isTime(value)) {
    issue(
      issues,
      'metadata.time.invalid',
      path,
      `${path} must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ`,
    );
  }
}

// The codes of the rules of the notes of the diagram and of a switch
// (noteCodes in validate.go).
const METADATA_NOTE_CODES = {
  notList: 'metadata.notes.not-list',
  tooMany: 'metadata.notes.too-many',
  notText: 'metadata.note.not-text',
  blank: 'metadata.note.blank',
  tooLong: 'metadata.note.too-long',
  control: 'metadata.note.control',
};
const SWITCH_NOTE_CODES = {
  notList: 'switch.notes.not-list',
  tooMany: 'switch.notes.too-many',
  notText: 'switch.note.not-text',
  blank: 'switch.note.blank',
  tooLong: 'switch.note.too-long',
  control: 'switch.note.control',
};

// The notes at `at`, of the diagram or of a switch (validateNotes in
// validate.go): at most MAX_DIAGRAM_NOTES, each a text that is not blank,
// at most MAX_DIAGRAM_NOTE_BYTES long, and without control characters
// except the newline and the tab. codes are the codes of the notes' owner.
function validateNotes(
  notes,
  issues,
  at = 'metadata.notes',
  codes = METADATA_NOTE_CODES,
) {
  if (notes === undefined || notes === null) {
    return;
  }

  // The server refuses any other value when it decodes the document.
  if (!Array.isArray(notes)) {
    issue(issues, codes.notList, at, 'notes must be a list of text');

    return;
  }

  if (notes.length > MAX_DIAGRAM_NOTES) {
    issue(
      issues,
      codes.tooMany,
      at,
      `at most ${MAX_DIAGRAM_NOTES} notes are allowed, not ${notes.length}`,
    );
  }

  notes.forEach((note, index) => {
    const path = `${at}[${index}]`;

    if (typeof note !== 'string') {
      issue(issues, codes.notText, path, 'note must be text');
    } else if (!trimSpace(note)) {
      issue(issues, codes.blank, path, 'note must not be blank');
    } else if (utf8Length(note) > MAX_DIAGRAM_NOTE_BYTES) {
      issue(
        issues,
        codes.tooLong,
        path,
        `note must be at most ${MAX_DIAGRAM_NOTE_BYTES} bytes`,
      );
    } else if (hasControlCharactersInLines(note)) {
      issue(
        issues,
        codes.control,
        path,
        'note must not contain control characters other than newline and tab',
      );
    }
  });
}

// The document's metadata (validateMetadata in validate.go): its id, its
// name, the users and times it names, and its notes. Issues are at
// metadata.<field>, as the server reports them.
function validateMetadata(metadata, issues) {
  if (!trimSpace(String(metadata.id || ''))) {
    issue(
      issues,
      'metadata.id.required',
      'metadata.id',
      'document ID is required',
    );
  }

  validateUUID(
    issues,
    'metadata.id',
    'document',
    metadata.id,
    'metadata.id.invalid',
  );

  const name = String(metadata.name ?? '');

  if (utf8Length(name) > MAX_NAME_BYTES) {
    issue(
      issues,
      'metadata.name.too-long',
      'metadata.name',
      `document name must be at most ${MAX_NAME_BYTES} bytes`,
    );
  } else if (hasControlCharacters(name)) {
    issue(
      issues,
      'metadata.name.control',
      'metadata.name',
      'document name must not contain control characters',
    );
  }

  validateUser(issues, 'metadata.createdBy', metadata.createdBy);
  validateTime(issues, 'metadata.createdAt', metadata.createdAt);
  validateUser(issues, 'metadata.updatedBy', metadata.updatedBy);
  validateTime(issues, 'metadata.updatedAt', metadata.updatedAt);
  validateNotes(metadata.notes, issues);
}

function validateHeader(doc, issues) {
  if (doc.$schema !== SCHEMA_URI) {
    issue(
      issues,
      'document.schema.mismatch',
      '$schema',
      `expected "${SCHEMA_URI}", got "${doc.$schema ?? ''}"`,
    );
  }

  if (doc.revision !== SCHEMA_REVISION) {
    issue(
      issues,
      'document.revision.mismatch',
      'revision',
      `expected ${SCHEMA_REVISION}, got ${doc.revision ?? ''}`,
    );
  }

  validateMetadata(metadataOf(doc), issues);

  const viewport = doc.viewport || {};

  if (!finite(viewport.x) || !finite(viewport.y) || !finite(viewport.zoom)) {
    issue(
      issues,
      'document.viewport.not-finite',
      'viewport',
      'viewport values must be finite numbers',
    );
  } else if (viewport.zoom <= 0) {
    issue(
      issues,
      'document.zoom.not-positive',
      'viewport.zoom',
      'zoom must be a positive number',
    );
  }

  const grid = doc.grid || {};

  if (!finite(grid.size) || grid.size <= 0) {
    issue(
      issues,
      'document.grid.invalid',
      'grid.size',
      'grid size must be a positive finite number',
    );
  }

  // Any string, or none: an id the editor does not know is ignored (see
  // ownLayout in layouts/index.js).
  if (
    doc.layout !== undefined &&
    doc.layout !== null &&
    typeof doc.layout !== 'string'
  ) {
    issue(
      issues,
      'document.layout.not-text',
      'layout',
      'layout must be a string',
    );
  }

  validateIconSize(
    doc.iconSize,
    'iconSize',
    issues,
    'document.icon-size.unknown',
  );
}

// The fewest points an edge route may hold: its two ends (minRoutePoints in
// validate.go).
const MIN_ROUTE_POINTS = 2;

// Absent, or at least its two ends, every point a finite coordinate
// (validateRoute in validate.go). Null is none, as Go decodes it.
function validateRoute(route, path, issues) {
  if (route === undefined || route === null) {
    return;
  }

  if (!Array.isArray(route) || route.length < MIN_ROUTE_POINTS) {
    issue(
      issues,
      'edge.route.too-few',
      path,
      `a route must have at least ${MIN_ROUTE_POINTS} points`,
    );

    return;
  }

  if (!route.every((point) => finite(point?.x) && finite(point?.y))) {
    issue(
      issues,
      'edge.route.not-finite',
      path,
      'route points must be finite numbers',
    );
  }
}

function validateNetworks(doc, issues, networksById) {
  const seenIds = new Map();
  const seenNames = new Map();
  const seenAliases = new Map();

  (doc.networks || []).forEach((network, index) => {
    const path = `networks[${index}]`;

    if (!trimSpace(String(network.id || ''))) {
      issue(
        issues,
        'network.id.required',
        `${path}.id`,
        'network ID is required',
      );
    } else if (seenIds.has(fold(network.id))) {
      issue(
        issues,
        'network.id.duplicate',
        `${path}.id`,
        `duplicate network ID "${network.id}" (also networks[${seenIds.get(fold(network.id))}])`,
      );
    } else {
      seenIds.set(fold(network.id), index);
      networksById.set(network.id, network);
    }

    validateUUID(
      issues,
      `${path}.id`,
      'network',
      network.id,
      'network.id.invalid',
    );

    if (!trimSpace(String(network.name || ''))) {
      issue(
        issues,
        'network.name.required',
        `${path}.name`,
        'network name is required',
      );
    } else if (WHITESPACE.test(network.name)) {
      issue(
        issues,
        'network.name.whitespace',
        `${path}.name`,
        `network name "${network.name}" must not contain whitespace`,
      );
    } else if (seenNames.has(network.name)) {
      // Exactly: minimega VLAN names are case sensitive, so networks
      // differing only by case are different VLANs.
      issue(
        issues,
        'network.name.duplicate',
        `${path}.name`,
        `conflicting network name "${network.name}" (also networks[${seenNames.get(network.name)}])`,
      );
    } else {
      seenNames.set(network.name, index);
    }

    validateLineStyle(
      network.lineStyle,
      `${path}.lineStyle`,
      issues,
      'network.line-style.unknown',
    );

    if (network.alias === undefined || network.alias === null) {
      return;
    }

    if (
      !Number.isInteger(network.alias) ||
      network.alias < 1 ||
      network.alias > MAX_VLAN_ALIAS
    ) {
      issue(
        issues,
        'network.alias.out-of-range',
        `${path}.alias`,
        `VLAN alias ${network.alias} is out of range (1-${MAX_VLAN_ALIAS})`,
      );

      return;
    }

    if (seenAliases.has(network.alias)) {
      issue(
        issues,
        'network.alias.duplicate',
        `${path}.alias`,
        `conflicting VLAN alias ${network.alias} (also networks[${seenAliases.get(network.alias)}])`,
      );
    } else {
      seenAliases.set(network.alias, index);
    }
  });
}

// A node holds its payload under the name of its kind.
const PAYLOAD_KEYS = NODE_KINDS;

function validateNodePayload(node, path, issues) {
  if (!PAYLOAD_KEYS.includes(node.kind)) {
    issue(
      issues,
      'node.kind.unknown',
      `${path}.kind`,
      `unknown node kind "${node.kind ?? ''}"`,
    );

    return false;
  }

  if (!node[node.kind]) {
    issue(
      issues,
      'node.payload.missing',
      path,
      `node of kind "${node.kind}" is missing its "${node.kind}" payload`,
    );
  }

  PAYLOAD_KEYS.filter((key) => key !== node.kind).forEach((key) => {
    if (node[key]) {
      issue(
        issues,
        'node.payload.extra',
        path,
        `node of kind "${node.kind}" must not carry a "${key}" payload`,
      );
    }
  });

  return true;
}

function validateDeviceHandles(node, path, issues, handleOwner) {
  const seenNames = new Map();
  // A spec is free-form: interfaces that are no list, or an entry that is
  // no object, name nothing a handle could match.
  const specNames = new Set(
    arrayOf(node.device?.spec?.network?.interfaces).map((iface) => iface?.name),
  );

  deviceHandles(node).forEach((handle, index) => {
    const handlePath = `${path}.device.interfaces[${index}]`;

    if (!trimSpace(String(handle.id || ''))) {
      issue(
        issues,
        'interface.id.required',
        `${handlePath}.id`,
        'interface handle ID is required',
      );
    } else if (handleOwner.has(handle.id)) {
      issue(
        issues,
        'interface.id.duplicate',
        `${handlePath}.id`,
        `duplicate interface handle ID "${handle.id}" (also used by node "${handleOwner.get(handle.id).id}")`,
      );
    } else {
      handleOwner.set(handle.id, node);
    }

    validateUUID(
      issues,
      `${handlePath}.id`,
      'interface handle',
      handle.id,
      'interface.id.invalid',
    );

    if (!trimSpace(String(handle.name || ''))) {
      issue(
        issues,
        'interface.name.required',
        `${handlePath}.name`,
        'interface name is required',
      );

      return;
    }

    if (seenNames.has(fold(handle.name))) {
      issue(
        issues,
        'interface.name.duplicate',
        `${handlePath}.name`,
        `duplicate interface name "${handle.name}" (also interfaces[${seenNames.get(fold(handle.name))}])`,
      );
    } else {
      seenNames.set(fold(handle.name), index);
    }

    if (!specNames.has(handle.name)) {
      issue(
        issues,
        'interface.name.unmatched',
        `${handlePath}.name`,
        `interface "${handle.name}" has no matching entry in the node's Interfaces`,
      );
    }
  });
}

// A device included from another topology is left out of the published
// topology because of the document's includeTopologies. So a document that
// includes nothing must not carry such a device.
function validateIncludedFrom(doc, name, path, issues) {
  if (name === undefined || name === '') {
    return;
  }

  if (typeof name !== 'string' || !trimSpace(name) || WHITESPACE.test(name)) {
    issue(
      issues,
      'device.included-from.invalid',
      `${path}.device.includedFrom`,
      `included topology name "${name}" must not be blank or contain whitespace`,
    );
  } else if (!(doc.source?.includeTopologies || []).length) {
    issue(
      issues,
      'device.included-from.no-includes',
      `${path}.device.includedFrom`,
      `device is included from topology "${name}", but the document includes no topologies`,
    );
  }
}

// The payload of the device node at index (validateDevice in validate.go):
// its hostname (unique among the devices, ignoring case), a spec whose
// hostname is the device's, its icon and colors, the topology it is
// included from, and its connection points. seenHostnames holds the index
// of each folded hostname found so far. handleOwner holds the node of each
// connection point id.
function validateDevice(
  doc,
  node,
  path,
  index,
  issues,
  { seenHostnames, handleOwner },
) {
  const hostname = node.device.hostname;

  if (!trimSpace(String(hostname || ''))) {
    issue(
      issues,
      'node.hostname.required',
      `${path}.device.hostname`,
      'hostname is required',
    );
  } else if (WHITESPACE.test(hostname)) {
    issue(
      issues,
      'node.hostname.whitespace',
      `${path}.device.hostname`,
      `hostname "${hostname}" must not contain whitespace`,
    );
  } else if (seenHostnames.has(fold(hostname))) {
    issue(
      issues,
      'node.hostname.duplicate',
      `${path}.device.hostname`,
      `duplicate hostname "${hostname}" (also nodes[${seenHostnames.get(fold(hostname))}])`,
    );
  } else {
    seenHostnames.set(fold(hostname), index);
  }

  if (!node.device.spec) {
    issue(
      issues,
      'device.spec.required',
      `${path}.device.spec`,
      'device spec is required',
    );
  } else if (node.device.spec.general?.hostname !== hostname) {
    issue(
      issues,
      'device.hostname.mismatch',
      `${path}.device.spec.general.hostname`,
      'Node hostname must match the device Hostname',
    );
  }

  validateIconKey(
    node.device.iconKey,
    `${path}.device.iconKey`,
    issues,
    'device.icon-key.unknown',
  );
  validateIconRef(
    node.device.icon,
    `${path}.device.icon`,
    issues,
    'device.icon.invalid',
  );
  validateIconSize(
    node.device.iconSize,
    `${path}.device.iconSize`,
    issues,
    'device.icon-size.unknown',
  );
  validateColors(node.device, `${path}.device`, issues, 'device.color.invalid');
  validatePurdueLevel(
    node.device.purdueLevel,
    `${path}.device.purdueLevel`,
    issues,
    'device.purdue-level.unknown',
  );
  validateIncludedFrom(doc, node.device.includedFrom, path, issues);
  validateDeviceHandles(node, path, issues, handleOwner);
}

function validateNodes(doc, issues, nodesById, networksById, handleOwner) {
  const seenIds = new Map();
  const seenHostnames = new Map();

  (doc.nodes || []).forEach((node, index) => {
    const path = `nodes[${index}]`;

    if (!trimSpace(String(node.id || ''))) {
      issue(issues, 'node.id.required', `${path}.id`, 'node ID is required');
    } else if (seenIds.has(fold(node.id))) {
      issue(
        issues,
        'node.id.duplicate',
        `${path}.id`,
        `duplicate node ID "${node.id}" (also nodes[${seenIds.get(fold(node.id))}])`,
      );
    } else {
      seenIds.set(fold(node.id), index);
      nodesById.set(node.id, node);
    }

    validateUUID(issues, `${path}.id`, 'node', node.id, 'node.id.invalid');

    if (!finite(node.position?.x) || !finite(node.position?.y)) {
      issue(
        issues,
        'node.position.not-finite',
        `${path}.position`,
        'position values must be finite numbers',
      );
    }

    if (node.size) {
      const size = sizeOf(node);

      if (
        !finite(size.width) ||
        !finite(size.height) ||
        size.width <= 0 ||
        size.height <= 0
      ) {
        issue(
          issues,
          'node.size.invalid',
          `${path}.size`,
          'size values must be positive finite numbers',
        );
      }
    }

    if (!validateNodePayload(node, path, issues)) {
      return;
    }

    if (node.kind === 'device' && node.device) {
      validateDevice(doc, node, path, index, issues, {
        seenHostnames,
        handleOwner,
      });
    }

    if (node.kind === 'switch' && node.switch) {
      if (!node.switch.networkId) {
        issue(
          issues,
          'switch.network.required',
          `${path}.switch.networkId`,
          'switch must reference a network',
        );
      } else if (!networksById.has(node.switch.networkId)) {
        issue(
          issues,
          'switch.network.unknown',
          `${path}.switch.networkId`,
          `unknown network "${node.switch.networkId}"`,
        );
      }

      validateColors(
        node.switch,
        `${path}.switch`,
        issues,
        'switch.color.invalid',
      );
      validateIconSize(
        node.switch.iconSize,
        `${path}.switch.iconSize`,
        issues,
        'switch.icon-size.unknown',
      );
      validatePurdueLevel(
        node.switch.purdueLevel,
        `${path}.switch.purdueLevel`,
        issues,
        'switch.purdue-level.unknown',
      );
      validateNotes(
        node.switch.notes,
        issues,
        `${path}.switch.notes`,
        SWITCH_NOTE_CODES,
      );
    }

    if (node.kind === 'group' && node.group) {
      validateBorderStyle(
        node.group.borderStyle,
        `${path}.group.borderStyle`,
        issues,
        'group.border-style.unknown',
      );
      validateIconKey(
        node.group.iconKey,
        `${path}.group.iconKey`,
        issues,
        'group.icon-key.unknown',
      );
      validateIconRef(
        node.group.icon,
        `${path}.group.icon`,
        issues,
        'group.icon.invalid',
      );
      validateIconSize(
        node.group.iconSize,
        `${path}.group.iconSize`,
        issues,
        'group.icon-size.unknown',
      );
    }

    if (node.kind === 'shape' && isObject(node.shape)) {
      validateShape(node.shape, `${path}.shape`, issues);
    }

    if (node.kind === 'icon' && isObject(node.icon)) {
      validateIconNode(node.icon, `${path}.icon`, issues);
    }

    if (node.kind === 'line' && isObject(node.line)) {
      validateLine(node.line, `${path}.line`, issues);
    }
  });
}

// The border style of a group or a shape (validateBorderStyle in
// validate.go): none, or one of BORDER_STYLES.
function validateBorderStyle(style, path, issues, code) {
  if (!unset(style) && !BORDER_STYLES.includes(style)) {
    issue(
      issues,
      code,
      path,
      `unknown border style ${quoted(style)} (expected one of ${BORDER_STYLES.join(', ')})`,
    );
  }
}

// The payload of a shape (validateShape in validate.go): its figure, its
// colors and its border style.
function validateShape(shape, path, issues) {
  if (!SHAPE_FIGURES.includes(shape.shape)) {
    issue(
      issues,
      'drawing.shape.unknown',
      `${path}.shape`,
      `unknown shape ${quoted(shape.shape ?? '')} (expected one of ${SHAPE_FIGURES.join(', ')})`,
    );
  }

  validateColors(shape, path, issues, 'drawing.color.invalid');
  validateBorderStyle(
    shape.borderStyle,
    `${path}.borderStyle`,
    issues,
    'drawing.border-style.unknown',
  );
}

// The payload of an icon node (validateIconNode in validate.go): exactly
// one of a built-in icon key and a custom icon name.
function validateIconNode(icon, path, issues) {
  if (unset(icon.iconKey) === unset(icon.icon)) {
    issue(
      issues,
      'drawing.icon.ambiguous',
      path,
      'an icon node must name exactly one of a built-in icon and a custom icon',
    );
  }

  validateIconKey(
    icon.iconKey,
    `${path}.iconKey`,
    issues,
    'drawing.icon-key.unknown',
  );
  validateIconRef(icon.icon, `${path}.icon`, issues, 'drawing.icon.invalid');
}

// The payload of a line (validateLine in validate.go): from MIN_LINE_POINTS
// to MAX_LINE_POINTS points, each a finite coordinate, its color, its line
// style, and arrowheads that are true or false.
function validateLine(line, path, issues) {
  const points = line.points;

  if (!Array.isArray(points) || points.length < MIN_LINE_POINTS) {
    issue(
      issues,
      'drawing.points.too-few',
      `${path}.points`,
      `a line must have at least ${MIN_LINE_POINTS} points`,
    );
  } else if (points.length > MAX_LINE_POINTS) {
    issue(
      issues,
      'drawing.points.too-many',
      `${path}.points`,
      `a line must have at most ${MAX_LINE_POINTS} points`,
    );
  } else if (!points.every((point) => finite(point?.x) && finite(point?.y))) {
    issue(
      issues,
      'drawing.points.not-finite',
      `${path}.points`,
      'line points must be finite numbers',
    );
  }

  const problem = colorProblem(line.color);

  if (problem) {
    issue(issues, 'drawing.color.invalid', `${path}.color`, problem);
  }

  validateLineStyle(
    line.lineStyle,
    `${path}.lineStyle`,
    issues,
    'drawing.line-style.unknown',
  );

  // The server decodes nothing else into them.
  ['startArrow', 'endArrow'].forEach((key) => {
    if (line[key] != null && typeof line[key] !== 'boolean') {
      issue(
        issues,
        'drawing.arrow.not-boolean',
        `${path}.${key}`,
        `${key} must be true or false`,
      );
    }
  });
}

function validateParents(doc, issues, nodesById) {
  (doc.nodes || []).forEach((node, index) => {
    const path = `nodes[${index}].parentId`;

    if (!node.parentId) {
      return;
    }

    if (node.parentId === node.id) {
      issue(issues, 'node.parent.self', path, 'node cannot be its own parent');

      return;
    }

    const parent = nodesById.get(node.parentId);

    if (!parent) {
      issue(
        issues,
        'node.parent.unknown',
        path,
        `unknown parent node "${node.parentId}"`,
      );

      return;
    }

    if (parent.kind !== 'group') {
      issue(
        issues,
        'node.parent.not-group',
        path,
        `parent node "${node.parentId}" is not a group`,
      );

      return;
    }

    const seen = new Set([node.id]);
    let current = node;

    while (current?.parentId) {
      if (seen.has(current.parentId)) {
        issue(
          issues,
          'node.parent.cycle',
          path,
          `group membership cycle detected at node "${node.id}"`,
        );

        return;
      }

      seen.add(current.parentId);
      current = nodesById.get(current.parentId);
    }
  });
}

function validateEdges(doc, issues, nodesById, networksById) {
  const seenIds = new Map();
  const connected = new Map();

  (doc.edges || []).forEach((edge, index) => {
    const path = `edges[${index}]`;

    if (!trimSpace(String(edge.id || ''))) {
      issue(issues, 'edge.id.required', `${path}.id`, 'edge ID is required');
    } else if (seenIds.has(fold(edge.id))) {
      issue(
        issues,
        'edge.id.duplicate',
        `${path}.id`,
        `duplicate edge ID "${edge.id}" (also edges[${seenIds.get(fold(edge.id))}])`,
      );
    } else {
      seenIds.set(fold(edge.id), index);
    }

    validateUUID(issues, `${path}.id`, 'edge', edge.id, 'edge.id.invalid');
    validateRoute(edge.route, `${path}.route`, issues);
    validateLineStyle(
      edge.lineStyle,
      `${path}.lineStyle`,
      issues,
      'edge.line-style.unknown',
    );

    if (!nodesById.has(edge.sourceNodeId)) {
      issue(
        issues,
        'edge.source.unknown',
        `${path}.sourceNodeId`,
        `unknown node "${edge.sourceNodeId}"`,
      );
    }

    if (!nodesById.has(edge.targetNodeId)) {
      issue(
        issues,
        'edge.target.unknown',
        `${path}.targetNodeId`,
        `unknown node "${edge.targetNodeId}"`,
      );
    }

    if (
      !nodesById.has(edge.sourceNodeId) ||
      !nodesById.has(edge.targetNodeId)
    ) {
      return;
    }

    if (edge.sourceNodeId === edge.targetNodeId) {
      issue(issues, 'edge.endpoints.same', path, 'edge endpoints must differ');

      return;
    }

    const endpoints = edgeEndpoints(doc, edge, (id) => nodesById.get(id));

    if (!endpoints || !endpoints.handleId) {
      issue(
        issues,
        'edge.endpoints.invalid',
        path,
        'an edge must connect one device interface to one switch',
      );

      return;
    }

    const handle = deviceHandles(endpoints.device).find(
      (entry) => entry.id === endpoints.handleId,
    );

    if (!handle) {
      issue(
        issues,
        'edge.handle.unknown',
        path,
        `unknown interface handle "${endpoints.handleId}" on device node "${endpoints.device.id}"`,
      );

      return;
    }

    if (connected.has(endpoints.handleId)) {
      issue(
        issues,
        'edge.interface.connected',
        path,
        `interface "${handle.name}" of device "${endpoints.device.device.hostname}" is already connected by edges[${connected.get(endpoints.handleId)}]`,
      );
    } else {
      connected.set(endpoints.handleId, index);
    }

    if (!edge.networkId) {
      issue(
        issues,
        'edge.network.required',
        `${path}.networkId`,
        'edge must reference a network',
      );

      return;
    }

    if (!networksById.has(edge.networkId)) {
      issue(
        issues,
        'edge.network.unknown',
        `${path}.networkId`,
        `unknown network "${edge.networkId}"`,
      );

      return;
    }

    const switchNetwork = endpoints.switchNode.switch?.networkId;

    if (edge.networkId !== switchNetwork) {
      issue(
        issues,
        'edge.network.mismatch',
        `${path}.networkId`,
        `network "${edge.networkId}" does not match network "${switchNetwork}" of switch "${endpoints.switchNode.id}"`,
      );
    }
  });
}

/**
 * Why a document may not list a Scenario config name, or ''. It must be a
 * config name of at most MAX_SCENARIO_NAME_BYTES (validateScenarios in
 * validate.go). The Scenario dialog uses this to check a name before it
 * adds the name.
 *
 * @param {string} name
 * @returns {string}
 */
export function scenarioNameProblem(name) {
  return scenarioNameRule(name).message;
}

// Why a document may not list a Scenario config name, with the code of that
// rule, or no message.
function scenarioNameRule(name) {
  const text = typeof name === 'string' ? name : '';

  if (text === '') {
    return {
      code: 'scenario.name.required',
      message: 'scenario name is required',
    };
  }

  if (utf8Length(text) > MAX_SCENARIO_NAME_BYTES) {
    return {
      code: 'scenario.name.too-long',
      message: `scenario name must be at most ${MAX_SCENARIO_NAME_BYTES} bytes`,
    };
  }

  if (!CONFIG_NAME.test(text)) {
    return {
      code: 'scenario.name.invalid',
      message: `scenario name ${quoted(text)} may use only letters, numbers, underscores, at signs, periods and hyphens`,
    };
  }

  return { code: '', message: '' };
}

// The Scenario configs a document lists (validateScenarios in validate.go):
// at most MAX_SCENARIOS, each a config name, none twice ignoring case.
function validateScenarios(doc, issues) {
  const scenarios = Array.isArray(doc.scenarios) ? doc.scenarios : [];

  if (scenarios.length > MAX_SCENARIOS) {
    issue(
      issues,
      'scenario.list.too-many',
      'scenarios',
      `at most ${MAX_SCENARIOS} scenarios are allowed, not ${scenarios.length}`,
    );
  }

  const seen = new Map();

  scenarios.forEach((name, index) => {
    const path = `scenarios[${index}]`;
    const rule = scenarioNameRule(name);

    if (rule.message) {
      issue(issues, rule.code, path, rule.message);

      return;
    }

    const key = fold(name);

    if (seen.has(key)) {
      issue(
        issues,
        'scenario.name.duplicate',
        path,
        `duplicate scenario ${goQuoted(name)} (also scenarios[${seen.get(key)}])`,
      );
    } else {
      seen.set(key, index);
    }
  });
}

function validateSource(doc, issues) {
  if (!doc.source) {
    return;
  }

  if (!['manual', 'topology', 'experiment'].includes(doc.source.kind)) {
    issue(
      issues,
      'source.kind.unknown',
      'source.kind',
      `unknown source kind "${doc.source.kind ?? ''}"`,
    );
  }

  validateIncludes(
    doc.source.includeTopologies,
    'source.includeTopologies',
    issues,
  );
  validateIncludes(
    doc.source.unresolvedIncludes,
    'source.unresolvedIncludes',
    issues,
  );

  // null is no digest, as the server decodes it.
  const digest = doc.source.digest;

  if (digest != null && digest !== '' && !isDigest(digest)) {
    issue(
      issues,
      'source.digest.malformed',
      'source.digest',
      `malformed source digest "${digest}" (expected sha256:<64 hex>)`,
    );
  }

  validateAnnotations(doc.source.annotations, issues);
}

// The names of included topologies the source lists at path
// (validateIncludes in validate.go): none blank, none with whitespace.
function validateIncludes(includes, path, issues) {
  // The server refuses any other value when it decodes the document.
  if (includes != null && !Array.isArray(includes)) {
    issue(
      issues,
      'include.list.not-list',
      path,
      'included topologies must be a list of topology names',
    );
  }

  (Array.isArray(includes) ? includes : []).forEach((name, index) => {
    const at = `${path}[${index}]`;

    if (typeof name !== 'string' || !trimSpace(name)) {
      issue(
        issues,
        'include.name.required',
        at,
        'included topology name is required',
      );
    } else if (WHITESPACE.test(name)) {
      issue(
        issues,
        'include.name.whitespace',
        at,
        `included topology name "${name}" must not contain whitespace`,
      );
    }
  });
}

// What makes an annotation key unusable, with the code of that rule
// (annotationKeyProblem in validate.go), or no problem.
function annotationKeyProblem(key) {
  if (!trimSpace(key)) {
    return {
      code: 'source.annotation-key.blank',
      problem: 'must not be blank',
    };
  }

  if (utf8Length(key) > MAX_NAME_BYTES) {
    return {
      code: 'source.annotation-key.too-long',
      problem: `must be at most ${MAX_NAME_BYTES} bytes`,
    };
  }

  return hasControlCharacters(key)
    ? {
        code: 'source.annotation-key.control',
        problem: 'must not contain control characters',
      }
    : { code: '', problem: '' };
}

function validateAnnotations(annotations, issues) {
  const path = 'source.annotations';

  // null is none, as the server decodes it.
  if (annotations == null) {
    return;
  }

  // The server refuses any other value when it decodes the document.
  if (typeof annotations !== 'object' || Array.isArray(annotations)) {
    issue(
      issues,
      'source.annotations.not-object',
      path,
      'annotations must be an object of text values',
    );

    return;
  }

  const entries = Object.entries(annotations);
  let size = 0;

  entries.forEach(([key, value]) => {
    if (typeof value === 'string') {
      size += utf8Length(key) + utf8Length(value);
    } else {
      issue(
        issues,
        'source.annotation.not-text',
        `${path}.${key}`,
        `annotation "${key}" must be text`,
      );
    }
  });

  if (entries.length > MAX_ANNOTATIONS) {
    issue(
      issues,
      'source.annotations.too-many',
      path,
      `at most ${MAX_ANNOTATIONS} annotations are allowed, not ${entries.length}`,
    );
  }

  if (size > MAX_ANNOTATION_BYTES) {
    issue(
      issues,
      'source.annotations.too-large',
      path,
      `annotations must take at most ${MAX_ANNOTATION_BYTES} bytes in all, not ${size}`,
    );
  }

  entries
    .map(([key]) => key)
    .sort()
    .forEach((key) => {
      const { code, problem } = annotationKeyProblem(key);

      if (problem) {
        issue(issues, code, path, `annotation key ${quoted(key)} ${problem}`);
      }
    });
}

// What a device's spec is checked against: the folded names of the
// diagram's networks, and the disk images the server has. The disk images
// are null while they are unknown, and drive images are then not checked.
function specContext(doc, disks) {
  return {
    networks: new Set(
      (doc?.networks || [])
        .map((network) => fold(network?.name))
        .filter(Boolean),
    ),
    disks: Array.isArray(disks) ? new Set(disks) : null,
  };
}

function trimmed(value) {
  return typeof value === 'string' ? trimSpace(value) : '';
}

function arrayOf(value) {
  return Array.isArray(value) ? value : [];
}

/**
 * The fields of a device spec that name something outside the device, and
 * that thing does not exist: an interface VLAN that is not a network of
 * the diagram, and a drive image that is not a disk image of the server. A
 * VLAN that names a network in another case is not reported here. When the
 * user types it, the interface connects to that network (see connectByVLAN
 * in model.js). When an unconnected interface already has it, it is
 * reported with its connection (see interfaceWarnings). Images are matched
 * by file name, as the server lists them. An external node runs no image
 * of its own.
 *
 * @param {object} spec
 * @param {string} hostname the device's, for the issue text
 * @param {object} context see specContext
 * @returns {{code: string, field: string, path: string, warning: string,
 *   issue: string}[]} code: the rule's code. field: the JSON Forms data
 *   path of the field (spec.hardware.drives.0.image). path: its path under
 *   the node (device.spec.hardware.drives[0].image). warning: the text for
 *   the field. issue: the text for the diagram
 */
function specFindings(spec, hostname, { networks, disks }) {
  const findings = [];
  const of = hostname ? ` of "${hostname}"` : '';

  arrayOf(spec?.network?.interfaces).forEach((iface, index) => {
    const vlan = trimmed(iface?.vlan);

    if (!vlan || networks.has(fold(vlan))) {
      return;
    }

    const name = trimmed(iface?.name)
      ? `interface "${iface.name}"`
      : `interface #${index + 1}`;

    findings.push({
      code: 'interface.vlan.unknown',
      field: `spec.network.interfaces.${index}.vlan`,
      path: `device.spec.network.interfaces[${index}].vlan`,
      warning: `No network in this diagram is named "${vlan}".`,
      issue: `${name}${of} uses VLAN "${vlan}", which is not a network in this diagram`,
    });
  });

  if (!disks || spec?.external === true) {
    return findings;
  }

  arrayOf(spec?.hardware?.drives).forEach((drive, index) => {
    const image = trimmed(drive?.image);

    if (!image || disks.has(image.split('/').pop())) {
      return;
    }

    findings.push({
      code: 'device.image.unknown',
      field: `spec.hardware.drives.${index}.image`,
      path: `device.spec.hardware.drives[${index}].image`,
      warning: `The server has no disk image named "${image}".`,
      issue: `drive image "${image}"${of} is not among the server's disk images`,
    });
  });

  return findings;
}

/**
 * What phenix makes of the hostname of a device it starts, in its words: a
 * hostname it refuses, which blocks publishing, or one it warns about. Its
 * schema refuses a hostname of one character. When it creates an
 * experiment, it refuses "all", all digits, and "phenix" on a Windows node,
 * but it stores a topology with such a hostname (checkHostnameKeywords in
 * types/version/v1/hostname.go). The server also refuses to publish them
 * (checkHostnames in types/builder/topology.go). phenix checks no external
 * node.
 *
 * @param {string} hostname
 * @param {object} spec the device spec
 * @returns {{code: string, message: string, refused: boolean}|null} null
 *   for a hostname phenix takes as it is. code is the rule's code
 *   (hostnameCode in types/builder/topology.go)
 */
function hostnameFinding(hostname, spec) {
  if (typeof hostname !== 'string' || !hostname || spec?.external != null) {
    return null;
  }

  const refused = (code, problem, reason) => ({
    code,
    message: `hostname "${hostname}" ${problem}, so the device cannot be published: ${reason}`,
    refused: true,
  });
  const windows =
    String(spec?.hardware?.os_type ?? '').toLowerCase() === 'windows';
  const folded = hostname.toLowerCase();

  if ([...hostname].length === 1) {
    return refused(
      'node.hostname.short',
      'is 1 character long',
      'phenix requires hostnames of at least 2 characters, as VyOS, Vyatta and Windows do',
    );
  }

  if (hostname === MINIMEGA_WILDCARD_VM) {
    return refused(
      'node.hostname.reserved',
      'is reserved',
      `minimega uses "all" as its wildcard VM target, so it refuses to launch a VM named "all", and commands that target a VM by name (such as "vm kill all") act on every VM in the experiment`,
    );
  }

  if (/^[0-9]+$/.test(hostname)) {
    return refused(
      'node.hostname.numeric',
      'is all digits',
      `minimega reads "vm launch kvm ${hostname}" as a number of VMs to launch rather than a VM name, and reads a numeric VM target as a VM ID`,
    );
  }

  if (folded === MINIMEGA_WILDCARD_VM) {
    return {
      code: 'node.hostname.reserved-case',
      message: `hostname "${hostname}" differs from the reserved name "all" only by case: minimega accepts it because its reserved-name check is case-sensitive, but any tool or script that lowercases VM names would treat it as minimega's wildcard for every VM`,
      refused: false,
    };
  }

  if (folded === PHENIX_HOSTNAME && windows) {
    return refused(
      'node.hostname.windows-phenix',
      'cannot be used for a Windows node',
      `the startup app writes each Windows node's startup script to "<hostname>-startup.ps1" in the experiment's startup directory, where it also stages "phenix-startup.ps1", the startup wrapper every Windows node runs; the file names collide, so one file overwrites the other`,
    );
  }

  if (folded === PHENIX_HOSTNAME) {
    return {
      code: 'node.hostname.phenix',
      message: `hostname "${hostname}" matches "phenix", the hostname "phenix image" bakes into the images it builds: until the startup app renames them, other VMs built from those images also report "phenix" to miniccc, so hostname-based C2 checks for this node (such as delay.c2 without useUUID) can match the wrong VM; this hostname also cannot be published if the node's os_type is changed to windows`,
      refused: false,
    };
  }

  return null;
}

/**
 * Warnings about the fields of a device, for the Inspector's working copy:
 * the checks validateDocument makes of a device's hostname and spec, made
 * before an edit is applied.
 *
 * @param {object} doc the diagram the device is in
 * @param {object} spec the device spec
 * @param {object} [options] disks: file names of the server's disk images,
 *   or null while they are unknown. nodeId: the id of the device that has
 *   the spec. This spec replaces the device's spec in the diagram when its
 *   addresses are compared with the other devices' (see sharedAddresses).
 *   hostname: the device's, when it is to be checked
 * @returns {Object<string, string[]>} messages, keyed by the JSON Forms data
 *   path of the field (hostname, spec.network.interfaces.0.vlan)
 */
export function deviceFieldWarnings(
  doc,
  spec,
  { disks = null, nodeId = '', hostname } = {},
) {
  const warnings = {};
  const add = (field, warning) => {
    warnings[field] = [...(warnings[field] || []), warning];
  };

  const named = hostnameFinding(hostname, spec);

  if (named) {
    add(
      'hostname',
      `${named.message[0].toUpperCase()}${named.message.slice(1)}.`,
    );
  }

  specFindings(spec, '', specContext(doc, disks)).forEach((finding) => {
    add(finding.field, finding.warning);
  });

  const others = (doc?.nodes || []).filter(
    (node) => node.kind === 'device' && node.id !== nodeId,
  );
  const self = (doc?.nodes || []).find((node) => node.id === nodeId);
  const connections = connectionNetworks(doc);
  const interfaces = arrayOf(spec?.network?.interfaces);

  sharedAddresses([
    {
      hostname: self?.device?.hostname ?? '',
      interfaces,
      networks: interfaceNetworks(
        interfaces,
        workingVLANs(self, interfaces, connections),
      ),
      included: false,
      external: spec?.external != null,
    },
    ...others.map((node) => addressDevice(node, connections)),
  ])
    .filter((shared) => shared.device === 0)
    .forEach((shared) => {
      add(
        `spec.network.interfaces.${shared.index}.${shared.field}`,
        `This ${shared.name} is also used${shared.network} by ${otherUsers(shared)}.`,
      );
    });

  return warnings;
}

// Whether a spec interface names a VLAN, as the server decides it (hasVLAN
// in types/builder/topology.go). The schema checks a VLAN that is not a
// string.
function hasVLAN(iface) {
  const vlan = iface?.vlan;

  return vlan != null && (typeof vlan !== 'string' || trimSpace(vlan) !== '');
}

// How a message names each spec interface of a device: by its name, or by
// its position when the name does not identify it, as the server names it
// (interfaceLabels in types/builder/topology.go).
function interfaceLabels(interfaces) {
  const names = interfaces.map((iface) =>
    typeof iface?.name === 'string' ? iface.name : '',
  );
  const uses = new Map();

  names.forEach((name) => uses.set(name, (uses.get(name) || 0) + 1));

  return names.map((name, index) => {
    if (!trimSpace(name)) {
      return `#${index + 1}`;
    }

    return uses.get(name) > 1 ? `"${name}" (#${index + 1})` : `"${name}"`;
  });
}

/**
 * What publishing does with each interface of a device that is not
 * connected on the canvas. These are the device spec's interfaces, as the
 * server checks them. So they include the interfaces with no connection
 * point: an unnamed one, a second one of the same name (a connection point
 * is the first one's, see specInterfaceFor in model.js), and one that an
 * uploaded document gave no connection point. An interface is named by its
 * position when its name does not identify it.
 *
 * An interface's VLAN is published as it is, and phenix matches VLAN names
 * exactly. So the interface goes only on a network of exactly that name.
 *
 * @param {object} node device node
 * @param {Set<string>} connected ids of the connected interface handles
 * @param {object} networks networksByName(doc)
 * @returns {{index: number, code: string, message: string,
 *   blocksPublish?: true}[]} index: the interface's index in the spec.
 *   code: the rule's code
 */
function interfaceWarnings(node, connected, networks) {
  const interfaces = specInterfaces(node);
  const names = interfaces.map((iface) =>
    typeof iface?.name === 'string' ? iface.name : '',
  );
  const labels = interfaceLabels(interfaces);
  const handles = new Map(
    deviceHandles(node).map((handle) => [handle.name, handle]),
  );
  const external = node.device.spec?.external != null;
  const warnings = [];

  interfaces.forEach((iface, index) => {
    // The schema checks an entry of the wrong shape, as on the server.
    if (!iface || typeof iface !== 'object') {
      return;
    }

    const name = names[index];
    const handle =
      name && names.indexOf(name) === index ? handles.get(name) : undefined;

    if (handle && connected.has(handle.id)) {
      return;
    }

    const unnamed = !trimSpace(name);
    const shared = !unnamed && names.indexOf(name) !== names.lastIndexOf(name);
    const subject = `interface ${labels[index]} of "${node.device.hostname}"`;
    const warn = (code, message, extra = {}) =>
      warnings.push({ index, code, message, ...extra });
    const blocks = { blocksPublish: true };
    const missing = 'interface.vlan.missing';

    if (!hasVLAN(iface)) {
      if (external) {
        warn(
          'interface.network.unconnected',
          `${subject} is not connected to a network`,
        );
      } else if (handle) {
        warn(
          missing,
          `${subject} is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it`,
          blocks,
        );
      } else if (unnamed) {
        warn(
          missing,
          `${subject} has no name and no VLAN, so it cannot be published: name it, then connect it or type a VLAN for it`,
          blocks,
        );
      } else if (shared) {
        warn(
          missing,
          `${subject} has no VLAN, and another interface has its name, so it cannot be connected or published: rename it, then connect it or type a VLAN for it`,
          blocks,
        );
      } else {
        warn(
          missing,
          `${subject} has no VLAN and no connection point on the canvas, so it cannot be published: type a VLAN for it`,
          blocks,
        );
      }

      return;
    }

    const vlan = trimSpace(String(iface.vlan));
    const network = networks.exact.get(vlan);
    const cased = networks.folded.get(fold(vlan));

    if (network) {
      warn(
        'interface.vlan.implicit',
        `${subject} is not connected on the canvas, but its VLAN "${vlan}" puts it on network ${network.name} when published`,
      );
    } else if (cased) {
      warn(
        'interface.vlan.case-mismatch',
        `${subject} is not connected on the canvas, and its VLAN "${vlan}" differs from network ${cased.name} only in case: phenix treats them as different VLANs, so publishing does not put it on network ${cased.name}`,
      );
    } else {
      warn(
        'interface.network.unconnected',
        `${subject} is not connected to a network`,
      );
    }
  });

  return warnings;
}

// The named networks by name, and by folded name: the first of each.
function networksByName(doc) {
  const exact = new Map();
  const folded = new Map();

  for (const network of doc.networks || []) {
    if (network?.name && !exact.has(network.name)) {
      exact.set(network.name, network);
    }

    if (network?.name && !folded.has(fold(network.name))) {
      folded.set(fold(network.name), network);
    }
  }

  return { exact, folded };
}

/**
 * An IPv4 address as its four numbers. Like Go's netip.ParseAddr, it takes
 * no leading zeros.
 *
 * @param {string} text
 * @returns {number[]|null} null when it is not one
 */
export function ipv4Octets(text) {
  const parts = text.split('.');

  return parts.length === 4 &&
    parts.every(
      (part) => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255,
    )
    ? parts.map(Number)
    : null;
}

// An IPv6 address as its eight groups, or null when it is not one, as Go's
// netip.ParseAddr reads it. `::` is at least one group of zeros, and an
// IPv4 address may end it, for the last two groups.
function ipv6Groups(text) {
  const halves = text.split('::');

  if (halves.length > 2) {
    return null;
  }

  const sides = halves.map((half) => (half ? half.split(':') : []));
  const end = sides[sides.length - 1];

  if (end.length && end[end.length - 1].includes('.')) {
    const octets = ipv4Octets(end.pop());

    if (!octets) {
      return null;
    }

    end.push(
      (octets[0] * 256 + octets[1]).toString(16),
      (octets[2] * 256 + octets[3]).toString(16),
    );
  }

  const fields = sides.flat();

  if (
    fields.some((field) => !/^[0-9a-f]{1,4}$/i.test(field)) ||
    (sides.length === 1 ? fields.length !== 8 : fields.length > 7)
  ) {
    return null;
  }

  const groups =
    sides.length === 1
      ? fields
      : [...sides[0], ...Array(8 - fields.length).fill('0'), ...sides[1]];

  return groups.map((field) => parseInt(field, 16));
}

// Removes the ASCII whitespace around an address or a proto, as the server
// does (trimASCIISpace in types/builder/topology.go).
// String.prototype.trim and Go's strings.TrimSpace each remove a character
// that the other keeps (U+FEFF and U+0085), so they would disagree about an
// address.
function trimASCIISpace(text) {
  return text.replace(/^[\t\n\v\f\r ]+|[\t\n\v\f\r ]+$/g, '');
}

// Protos whose interface phenix gives no address of its own. A dhcp
// interface asks a DHCP server for one. phenix starts a manual interface
// with no address.
const UNADDRESSED_PROTOS = new Set(['dhcp', 'manual']);

/**
 * The IP address an interface uses, as typed without a prefix length, and
 * as a key that is the same in every written form. The address is parsed
 * as the server parses it (interfaceIP in types/builder/topology.go),
 * without a zone. An IPv4 address mapped into IPv6 becomes the IPv4
 * address. A manual interface's address is not read, because the Inspector
 * does not show it, but the vrouter app gives it to a Vyatta or VyOS
 * router. A QinQ interface's address is read, because minirouter and
 * Vyatta give it to the interface.
 *
 * @param {object} iface spec interface
 * @returns {{text: string, key: string}|null} null for no address: a blank
 *   address, an address that the interface asks DHCP for or starts without
 *   (proto manual), and an address that does not parse, which the
 *   Inspector's form reports
 */
function interfaceIP(iface) {
  const proto = typeof iface.proto === 'string' ? iface.proto : '';

  if (
    typeof iface.address !== 'string' ||
    UNADDRESSED_PROTOS.has(trimASCIISpace(proto).toLowerCase())
  ) {
    return null;
  }

  const text = trimASCIISpace(iface.address.split('/')[0]);
  const zone = text.indexOf('%');

  if (!text.includes(':')) {
    const octets = zone < 0 ? ipv4Octets(text) : null;

    return octets ? { text, key: octets.join('.') } : null;
  }

  const groups =
    zone === text.length - 1
      ? null
      : ipv6Groups(zone < 0 ? text : text.slice(0, zone));

  if (!groups) {
    return null;
  }

  const [high, low] = groups.slice(6);
  const mapped =
    groups.slice(0, 5).every((group) => group === 0) && groups[5] === 0xffff;

  return {
    text,
    key: mapped
      ? [high >> 8, high & 255, low >> 8, low & 255].join('.')
      : groups.map((group) => group.toString(16)).join(':'),
  };
}

/**
 * The MAC address an interface uses, as typed, and as a key that is the
 * same in either case and with any separators: aa:bb:cc:dd:ee:ff,
 * AA-BB-CC-DD-EE-FF and aabb.ccdd.eeff are one address.
 *
 * @param {object} iface spec interface
 * @returns {{text: string, key: string}|null} null for no MAC: a blank MAC,
 *   for which minimega makes one, and a MAC that is not twelve hex digits,
 *   which the Inspector's form reports
 */
function interfaceMAC(iface) {
  const text = typeof iface.mac === 'string' ? trimASCIISpace(iface.mac) : '';
  const key = text.toLowerCase().replace(/[:.-]/g, '');

  return /^[0-9a-f]{12}$/.test(key) ? { text, key } : null;
}

// The addresses sharedAddresses compares: the field each is in, its name in
// a message, whether an external device's addresses count, and the code
// for an address that two interfaces use. phenix does not start an
// external device, and its schema (so the Inspector) has no MAC.
const ADDRESS_FIELDS = [
  {
    field: 'address',
    name: 'IP address',
    read: interfaceIP,
    external: true,
    code: 'interface.ip.shared',
  },
  {
    field: 'mac',
    name: 'MAC address',
    read: interfaceMAC,
    external: false,
    code: 'interface.mac.shared',
  },
];

// The bridge name that puts an interface on the experiment's default
// bridge, as a blank bridge does. phenix replaces both with that bridge
// (defaultBridgeName in types/builder/topology.go).
const DEFAULT_BRIDGE = 'phenix';

// The network each connected interface handle is on, by handle id, as the
// server finds it (handleNetworks in types/builder/topology.go): the
// network of the switch the connection joins the device to.
function connectionNetworks(doc) {
  const nodes = new Map();
  const networks = new Map();
  const handles = new Map();

  for (const node of doc?.nodes || []) {
    if (node && !nodes.has(node.id)) {
      nodes.set(node.id, node);
    }
  }

  for (const network of doc?.networks || []) {
    if (network && !networks.has(network.id)) {
      networks.set(network.id, network);
    }
  }

  for (const edge of doc?.edges || []) {
    const ends = edge ? edgeEndpoints(doc, edge, (id) => nodes.get(id)) : null;
    const network =
      ends?.handleId && ends.device.device
        ? networks.get(ends.switchNode.switch?.networkId)
        : undefined;

    if (network) {
      handles.set(ends.handleId, network);
    }
  }

  return handles;
}

// The spec interface of a connection point of that name, as the server
// finds it (findInterface in types/builder/topology.go): the first of
// exactly that name, else the first of that name in any letter case, or
// -1.
function specInterfaceIndex(interfaces, name) {
  let fallback = -1;

  for (const [index, iface] of interfaces.entries()) {
    // The schema checks an entry of the wrong shape.
    if (iface && typeof iface === 'object') {
      const own = typeof iface.name === 'string' ? iface.name : '';

      if (own === name) {
        return index;
      }

      if (fallback < 0 && fold(own) === fold(name)) {
        fallback = index;
      }
    }
  }

  return fallback;
}

// The VLAN each spec interface of a device is published with, as the
// server projects it (connectInterfaces in types/builder/topology.go). A
// connected interface's VLAN is its network's name. Any other interface
// keeps its own VLAN.
function publishedVLANs(node, interfaces, connected) {
  const vlans = interfaces.map((iface) => iface?.vlan);

  deviceHandles(node).forEach((handle) => {
    const network = handle ? connected.get(handle.id) : undefined;
    const index = network ? specInterfaceIndex(interfaces, handle.name) : -1;

    if (index >= 0) {
      vlans[index] = network.name;
    }
  });

  return vlans;
}

// An interface's VLAN as typed, without the white space around it.
function typedVLAN(iface) {
  return typeof iface?.vlan === 'string' ? trimSpace(iface.vlan) : '';
}

// The VLAN each interface of the Inspector's working copy of a device is
// published with after Apply. Apply connects an interface whose VLAN the
// working copy changed by that VLAN (see connectByVLAN in model.js), so it
// is on the VLAN as typed. Any other interface keeps the network that
// publishing puts it on now (see publishedVLANs).
function workingVLANs(node, interfaces, connected) {
  const published = publishedVLANs(node, interfaces, connected);
  const before = new Map(
    specInterfaces(node).map((iface) => [iface?.name, typedVLAN(iface)]),
  );

  return interfaces.map((iface, index) => {
    const typed = typedVLAN(iface);

    return before.has(iface?.name) && before.get(iface.name) === typed
      ? published[index]
      : typed;
  });
}

// The network each spec interface is on after publishing, from the VLAN it
// is published with, as the server names it (interfaceNetwork in
// types/builder/topology.go):
// - bridge: as written, '' for the experiment's default bridge
// - vlan: without the white space around it, '' for none
// null for an entry of the wrong shape.
function interfaceNetworks(interfaces, vlans) {
  return interfaces.map((iface, index) => {
    if (!iface || typeof iface !== 'object') {
      return null;
    }

    const bridge =
      typeof iface.bridge === 'string' && iface.bridge !== DEFAULT_BRIDGE
        ? iface.bridge
        : '';
    const vlan =
      typeof vlans[index] === 'string' ? trimSpace(vlans[index]) : '';

    return { bridge, vlan };
  });
}

// How a message names a network that interfaceNetworks returns, after the
// words that it is used in, as the server names it (networkPhrase in
// types/builder/topology.go).
function networkPhrase({ bridge, vlan }) {
  if (!vlan) {
    return bridge
      ? ` on bridge ${goQuoted(bridge)} without a VLAN`
      : ' without a VLAN';
  }

  return bridge
    ? ` on VLAN ${goQuoted(vlan)} of bridge ${goQuoted(bridge)}`
    : ` on VLAN ${goQuoted(vlan)}`;
}

// A device node as sharedAddresses takes it, with the networks its
// interfaces are published on.
function addressDevice(node, connected) {
  const interfaces = specInterfaces(node);

  return {
    hostname: node.device?.hostname ?? '',
    interfaces,
    networks: interfaceNetworks(
      interfaces,
      publishedVLANs(node, interfaces, connected),
    ),
    included: Boolean(node.device?.includedFrom),
    external: node.device?.spec?.external != null,
  };
}

/**
 * The interfaces that use an IP address or a MAC address that another
 * interface on the same network also uses, on two devices or on one.
 *
 * Each interface is on the network that publishing puts it on: its bridge
 * and the VLAN it is published with (see interfaceNetworks). So interfaces
 * on different VLANs, or on VLANs of one name on different bridges, may use
 * the same addresses, as the server allows (checkInterfaceAddresses in
 * types/builder/topology.go). Interfaces without a VLAN are compared with
 * each other.
 *
 * The interfaces of an included device count, because phenix merges them
 * into the experiment. But they are not reported, and neither is an
 * address that only included devices share. Their topology must fix them.
 * An external device's IP addresses count, and its MAC addresses do not
 * (see ADDRESS_FIELDS). Interfaces are found by network and address, so
 * the check grows with the diagram, never with its square.
 *
 * @param {{hostname: string, interfaces: object[], networks: object[],
 *   included: boolean, external: boolean}[]} devices networks holds the
 *   network of each interface (see interfaceNetworks)
 * @returns {{device: number, index: number, field: string, name: string,
 *   code: string, text: string, network: string, label: string,
 *   hostname: string, other: object, more: number}[]} for each interface:
 *   the device's position in `devices` and the interface's position in the
 *   spec, the address as typed, the code of the rule, its network as a
 *   message names it (see networkPhrase), another interface that uses the
 *   address (label and hostname), and how many more interfaces use it
 */
function sharedAddresses(devices) {
  const users = ADDRESS_FIELDS.map(() => new Map());

  devices.forEach((device, position) => {
    const labels = interfaceLabels(device.interfaces);

    device.interfaces.forEach((iface, index) => {
      // The schema checks an entry of the wrong shape.
      if (!iface || typeof iface !== 'object') {
        return;
      }

      const network = device.networks[index];
      const scope = JSON.stringify([network.bridge, network.vlan]);

      ADDRESS_FIELDS.forEach(({ read, external }, kind) => {
        const address = device.external && !external ? null : read(iface);

        if (!address) {
          return;
        }

        const user = {
          device: position,
          index,
          text: address.text,
          network: networkPhrase(network),
          label: labels[index],
          hostname: device.hostname,
          included: device.included,
        };
        const key = `${scope} ${address.key}`;
        const list = users[kind].get(key);

        if (list) {
          list.push(user);
        } else {
          users[kind].set(key, [user]);
        }
      });
    });
  });

  return ADDRESS_FIELDS.flatMap(({ field, name, code }, kind) =>
    [...users[kind].values()]
      .filter((list) => list.length > 1)
      .flatMap((list) =>
        list.flatMap((user, position) =>
          user.included
            ? []
            : [
                {
                  ...user,
                  field,
                  name,
                  code,
                  other: list[position === 0 ? 1 : 0],
                  more: list.length - 2,
                },
              ],
        ),
      ),
  );
}

// The other interfaces that use a shared address: one by name, and how many
// more.
function otherUsers({ other, more }) {
  const named = `interface ${other.label} of "${other.hostname}"`;

  return more ? `${named} and ${count(more, 'more interface')}` : named;
}

/**
 * Advisory checks that are not server errors but are useful to show while
 * editing. None blocks saving. Three block publishing, and say so with
 * `blocksPublish`:
 * - an interface with no VLAN, which phenix stores but minimega refuses
 *   when the experiment starts
 * - an IP or MAC address that two interfaces on one network use (see
 *   sharedAddresses), which phenix also stores, but which clash when the
 *   experiment runs
 * - a hostname that phenix refuses (see hostnameFinding), which a draft
 *   imported from a topology that an older phenix stored can have
 * The server refuses to publish any of them (PublishTopologyConfig in
 * types/builder/topology.go). It checks every interface in the device spec,
 * as interfaceWarnings does. An external device is not started, so its
 * interfaces need no VLAN, and phenix does not check its hostname.
 *
 * @param {object} doc
 * @param {object[]} issues
 * @param {object} context see specContext
 */
function collectWarnings(doc, issues, context) {
  const connected = new Set(
    (doc.edges || []).flatMap((edge) =>
      [edge.sourceHandleId, edge.targetHandleId].filter(Boolean),
    ),
  );
  const networks = networksByName(doc);

  (doc.nodes || []).forEach((node, index) => {
    // An included device is its own topology's to fix, not this diagram's.
    if (node.kind !== 'device' || node.device?.includedFrom) {
      return;
    }

    const named = hostnameFinding(node.device?.hostname, node.device?.spec);

    if (named) {
      issue(
        issues,
        named.code,
        `nodes[${index}].device.hostname`,
        named.message,
        'warning',
        named.refused
          ? { blocksPublish: true, field: 'hostname' }
          : { field: 'hostname' },
      );
    }

    specFindings(node.device?.spec, node.device?.hostname, context).forEach(
      (finding) => {
        issue(
          issues,
          finding.code,
          `nodes[${index}].${finding.path}`,
          finding.issue,
          'warning',
          { field: finding.field },
        );
      },
    );

    if (specInterfaces(node).length === 0) {
      issue(
        issues,
        'device.interfaces.none',
        `nodes[${index}]`,
        `device "${node.device?.hostname}" has no interfaces`,
        'warning',
      );

      return;
    }

    interfaceWarnings(node, connected, networks).forEach(
      ({ index: position, code, message, ...extra }) => {
        issue(
          issues,
          code,
          `nodes[${index}].device.spec.network.interfaces[${position}]`,
          message,
          'warning',
          code === 'interface.vlan.missing'
            ? { ...extra, field: `spec.network.interfaces.${position}.vlan` }
            : extra,
        );
      },
    );
  });

  // At the interface's address or MAC field, where the Inspector shows its
  // warning too (see deviceFieldWarnings).
  const connections = connectionNetworks(doc);
  const devices = (doc.nodes || []).flatMap((node, index) =>
    node.kind === 'device'
      ? [{ ...addressDevice(node, connections), index }]
      : [],
  );

  sharedAddresses(devices).forEach((shared) => {
    issue(
      issues,
      shared.code,
      `nodes[${devices[shared.device].index}].device.spec.network.interfaces[${shared.index}].${shared.field}`,
      `${shared.name} ${shared.text} of interface ${shared.label} of "${shared.hostname}" is also used${shared.network} by ${otherUsers(shared)}`,
      'warning',
      {
        blocksPublish: true,
        field: `spec.network.interfaces.${shared.index}.${shared.field}`,
      },
    );
  });

  const switched = new Set(
    (doc.nodes || [])
      .filter((node) => node.kind === 'switch')
      .map((node) => node.switch?.networkId),
  );

  (doc.networks || []).forEach((network, index) => {
    if (!switched.has(network.id)) {
      issue(
        issues,
        'network.switch.missing',
        `networks[${index}]`,
        `network "${network.name}" has no switch on the canvas`,
        'warning',
      );
    }
  });
}

const ID_KEYS = { nodes: 'nodeId', edges: 'edgeId', networks: 'networkId' };

// Adds the id of the node, connection or network an issue's path starts at.
function locate(doc, entry) {
  const match = /^(nodes|edges|networks)\[(\d+)\]/.exec(entry.path);
  const id = match ? doc[match[1]]?.[Number(match[2])]?.id : '';

  return typeof id === 'string' && trimSpace(id)
    ? { ...entry, [ID_KEYS[match[1]]]: id }
    : entry;
}

/**
 * Validates a document exactly as the server does, plus editing warnings.
 *
 * @param {object} doc
 * @param {object} [options] disks: file names of the server's disk images,
 *   to check drive images against, or null while they are unknown
 * @returns {{code: string, path: string, message: string,
 *   level: 'error'|'warning', severity: 'error'|'warning', nodeId?: string,
 *   edgeId?: string, networkId?: string, field?: string,
 *   blocksPublish?: true}[]} issues sorted by path. code is a code of the
 *   registry that Go generates (schema/codes.json). severity is level
 */
export function validateDocument(doc, { disks = null } = {}) {
  const issues = [];

  if (!doc || typeof doc !== 'object') {
    issue(issues, 'document.content.missing', '', 'document is required');

    return issues;
  }

  const nodesById = new Map();
  const networksById = new Map();
  const handleOwner = new Map();

  validateHeader(doc, issues);
  validateNetworks(doc, issues, networksById);
  validateNodes(doc, issues, nodesById, networksById, handleOwner);
  validateParents(doc, issues, nodesById);
  validateEdges(doc, issues, nodesById, networksById);
  validateScenarios(doc, issues);
  validateSource(doc, issues);
  validateTemplates(doc, issues);
  issues.push(...validateIcons(doc.icons));
  collectWarnings(doc, issues, specContext(doc, disks));

  return issues
    .map((entry) => locate(doc, entry))
    .sort((a, b) => a.path.localeCompare(b.path));
}
