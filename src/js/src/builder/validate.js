// Document validation, mirroring phenix/types/builder validate.go with the
// same rules, codes and path form. An issue also carries nodeId, edgeId or
// networkId, which still finds its item after an edit moves indexes.
// types/builder/testdata/validation-corpus.json holds both sides to each other.

import {
  formatIPv4,
  hostAddress,
  interfacesByNetwork,
  isRoutingType,
  isSubnetHost,
  overlappingSubnets,
  parseIPv4,
  parseSubnet,
  UNADDRESSED_PROTOS,
} from './addressing.js';
import { count } from './announce.js';
import { isIconKey } from './catalog.js';
import { canonicalJSON, isDigest } from './digest.js';
import {
  foldKey as fold,
  goEqualFold,
  goToLower,
  goTrimSpace as trimSpace,
  trimASCIISpace,
} from './goCase.js';
import {
  ICON_NAME,
  MAX_DOCUMENT_ICONS,
  MAX_ICON_BYTES,
  MAX_ICON_NAME_BYTES,
  decodeIconData,
  iconPNGProblem,
} from './icons.js';
import {
  MATCH_CONFLICTS,
  MAX_LAYER_MATCHERS,
  MAX_MATCHER_PATH_BYTES,
  MAX_MATCHER_PATTERN_BYTES,
  MAX_MATCHER_REPEATS,
  matcherPathRule,
  matcherPatternRule,
} from './layerMatchers.js';
import { MAX_USER_BYTES } from './limits.js';
import {
  BORDER_STYLES,
  HEX_COLOR,
  ICON_SIZES,
  LINE_STYLES,
  MAX_DIAGRAM_NOTE_BYTES,
  MAX_DIAGRAM_NOTES,
  MAX_GRID_SIZE,
  MAX_LAYER_ID,
  MAX_LAYER_LABEL_BYTES,
  MAX_LAYER_SET_DESCRIPTION_BYTES,
  MAX_LAYER_SET_ID_BYTES,
  MAX_LAYER_SET_NAME_BYTES,
  MAX_LAYERS,
  MAX_LINE_POINTS,
  MIN_GRID_SIZE,
  MIN_LINE_POINTS,
  NODE_KINDS,
  SCHEMA_REVISION,
  SCHEMA_URI,
  SHAPE_FIGURES,
  deviceHandles,
  edgeEndpoints,
  isGridSize,
  isLayerId,
  isLayerSetId,
  metadataOf,
  sizeOf,
  specInterfaces,
} from './model.js';
import {
  hasControlCharacters,
  hasControlCharactersInLines,
  utf8Length,
} from './text.js';
import { isPlainObject } from './values.js';

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

// Where the library template a document's template was copied from is kept
// (TemplateSourceKinds in template.go), and the form of its ID
// (templateSourceIDPattern).
export const TEMPLATE_SOURCE_KINDS = ['library', 'builtin', 'preloaded'];
const TEMPLATE_SOURCE_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

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

// The white space a name must not contain: \s, as the schema's pattern
// ^\S+$ reads it (hasNameSpace in types/builder/spec.go).
const WHITESPACE = /\s/;

// minimega's wildcard VM target, and the hostname phenix image bakes into the
// images it builds (types/version/v1/hostname.go).
const MINIMEGA_WILDCARD_VM = 'all';
const PHENIX_HOSTNAME = 'phenix';

// A canonical RFC 4122 UUID with a known version and the RFC 4122 variant, in
// either case (IsUUID in types/builder/uuid.go).
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

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

// The messages of a layer ID out of range: of a device, a switch or a
// template's device, and of a layer the document defines (layerRangeMessage
// and layerIDRangeMessage in validate.go). The server's decoder refuses a
// value that is not an integer with the same words (decodeRule in
// decodeissues.go), and so does validation here.
const LAYER_RANGE = `layer must be an integer from 0 to ${MAX_LAYER_ID}`;
const LAYER_ID_RANGE = `layer ID must be an integer from 0 to ${MAX_LAYER_ID}`;

// The layer of a device, a switch or a template's device (validateLayer in
// validate.go): none, or a layer ID. It need not be a layer the document
// defines. code is the code of the owner.
function validateLayer(layer, path, issues, code) {
  if (layer !== undefined && layer !== null && !isLayerId(layer)) {
    issue(issues, code, path, LAYER_RANGE);
  }
}

// The hideDescription of a device, a group or a template's device: none, true
// or false. The server's decoder refuses any other value (decodeRule in Go).
function validateHideDescription(value, path, issues, code) {
  if (value != null && typeof value !== 'boolean') {
    issue(issues, code, path, 'hideDescription must be true or false');
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

// Why text is not an icon name, in the server's words (IconNameProblem in
// customicons.go), or ''.
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

// Checks a document's custom icons (ValidateIcons in customicons.go). An icon
// that nothing uses is valid: the editor drops it on its next edit.
export function validateIcons(icons, path = 'icons') {
  const issues = [];

  // null is none, as the server decodes it.
  if (icons === undefined || icons === null) {
    return issues;
  }

  // The server refuses any other value when it decodes the document.
  if (!isPlainObject(icons)) {
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
      `a document can carry at most ${MAX_DOCUMENT_ICONS} custom icons, not ${keys.length}`,
    );
  }

  keys.forEach((key) => {
    const { data } = isPlainObject(icons[key]) ? icons[key] : {};
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

// What makes a template unusable (Template.Issues in template.go). The spec is
// not checked against the phenix schema, and the id is not checked here,
// because its form depends on where the template is kept.
export function templateIssues(template, path) {
  const issues = [];
  const { name, description, device, source } = isPlainObject(template)
    ? template
    : {};

  if (typeof name !== 'string' || !trimSpace(name)) {
    issue(
      issues,
      'template.name.required',
      `${path}.name`,
      'a template needs a name',
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

  if (!isPlainObject(device) || !isPlainObject(device.spec)) {
    issue(
      issues,
      'template.spec.required',
      `${path}.device.spec`,
      'a template device needs a spec',
    );
  } else {
    const general = device.spec.general;
    const hostname =
      isPlainObject(general) && typeof general.hostname === 'string'
        ? general.hostname
        : '';

    if (!trimSpace(hostname)) {
      issue(
        issues,
        'template.hostname.required',
        `${path}.device.spec.general.hostname`,
        'a template needs a hostname',
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

  if (isPlainObject(source)) {
    templateSourceIssues(source, `${path}.source`, issues);
  }

  if (!isPlainObject(device)) {
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
  validateLayer(
    device.layer,
    `${path}.device.layer`,
    issues,
    'template.layer.out-of-range',
  );
  validateHideDescription(
    device.hideDescription,
    `${path}.device.hideDescription`,
    issues,
    'template.hide-description.not-boolean',
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

// What makes the source of a template unusable (TemplateSource.issues in
// template.go).
function templateSourceIssues(source, path, issues) {
  const { kind, id, version, digest } = source;

  if (!TEMPLATE_SOURCE_KINDS.includes(kind)) {
    issue(
      issues,
      'template.source.kind-unknown',
      `${path}.kind`,
      `template source kind ${quoted(kind ?? '')} is not library, builtin or preloaded`,
    );
  }

  if (unset(id)) {
    issue(
      issues,
      'template.source.id-invalid',
      `${path}.id`,
      'a template source needs an ID',
    );
  } else if (typeof id !== 'string' || !TEMPLATE_SOURCE_ID.test(id)) {
    issue(
      issues,
      'template.source.id-invalid',
      `${path}.id`,
      `template source ID ${quoted(id)} is not a template ID`,
    );
  }

  if (!Number.isInteger(version) || version < 1) {
    issue(
      issues,
      'template.source.version-invalid',
      `${path}.version`,
      `template source version must be 1 or more, not ${version ?? 0}`,
    );
  }

  // null is no digest, as the server decodes it.
  if (!unset(digest) && !isDigest(digest)) {
    issue(
      issues,
      'template.source.digest-invalid',
      `${path}.digest`,
      `template source digest ${quoted(digest)} is not sha256 and 64 lowercase hex digits`,
    );
  }
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
      `a document can have at most ${MAX_TEMPLATES} templates, not ${doc.templates.length}`,
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
        'a template needs an ID',
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

// Whether text is a document header time (IsTime in validate.go): UTC, whole
// seconds and a literal Z. The fields are checked here, not by Date, because
// browsers parse such text differently (hour 24, February 30).
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

// The diagram notes, as validateNotes in validate.go checks them: at most
// MAX_DIAGRAM_NOTES, each not blank, not too long, and with no bad character.
function validateNotes(notes, issues) {
  const at = 'metadata.notes';

  if (notes === undefined || notes === null) {
    return;
  }

  // The server refuses any other value when it decodes the document.
  if (!Array.isArray(notes)) {
    issue(
      issues,
      'metadata.notes.not-list',
      at,
      'notes must be a list of text',
    );

    return;
  }

  if (notes.length > MAX_DIAGRAM_NOTES) {
    issue(
      issues,
      'metadata.notes.too-many',
      at,
      `a document can have at most ${MAX_DIAGRAM_NOTES} notes, not ${notes.length}`,
    );
  }

  notes.forEach((note, index) => {
    const path = `${at}[${index}]`;

    if (typeof note !== 'string') {
      issue(issues, 'metadata.note.not-text', path, 'note must be text');
    } else if (!trimSpace(note)) {
      issue(issues, 'metadata.note.blank', path, 'note must not be blank');
    } else if (utf8Length(note) > MAX_DIAGRAM_NOTE_BYTES) {
      issue(
        issues,
        'metadata.note.too-long',
        path,
        `note must be at most ${MAX_DIAGRAM_NOTE_BYTES} bytes`,
      );
    } else if (hasControlCharactersInLines(note)) {
      issue(
        issues,
        'metadata.note.control',
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
      'a document needs an ID',
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

  if (!isGridSize(grid.size)) {
    issue(
      issues,
      'document.grid.invalid',
      'grid.size',
      `grid size must be a whole number from ${MIN_GRID_SIZE} to ${MAX_GRID_SIZE}`,
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
        'a network needs an ID',
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
        'a network needs a name',
      );
    } else if (WHITESPACE.test(network.name)) {
      issue(
        issues,
        'network.name.whitespace',
        `${path}.name`,
        `network name ${goQuoted(String(network.name))} must not contain whitespace`,
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
    validateNetworkAddressing(network, path, issues);

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

// The subnet and the gateway of a network (validateNetworkAddressing in
// validate.go): each none, or the subnet an IPv4 network without host bits
// and the gateway an IPv4 address, a host address of the subnet when the
// network has a valid one.
function validateNetworkAddressing(network, path, issues) {
  const { subnet, gateway } = network;
  let parsed = null;

  if (typeof subnet !== 'string' && !unset(subnet)) {
    issue(
      issues,
      'network.subnet.invalid',
      `${path}.subnet`,
      'subnet must be a string',
    );
  } else if (!unset(subnet)) {
    parsed = parseSubnet(subnet);

    if (!parsed) {
      issue(
        issues,
        'network.subnet.invalid',
        `${path}.subnet`,
        `subnet ${quoted(subnet)} is not an IPv4 network in CIDR form, such as 10.1.0.0/24`,
      );
    }
  }

  if (unset(gateway)) {
    return;
  }

  if (typeof gateway !== 'string') {
    issue(
      issues,
      'network.gateway.invalid',
      `${path}.gateway`,
      'gateway must be a string',
    );

    return;
  }

  const address = parseIPv4(gateway);

  if (address === null) {
    issue(
      issues,
      'network.gateway.invalid',
      `${path}.gateway`,
      `gateway ${quoted(gateway)} is not an IPv4 address`,
    );
  } else if (parsed && !isSubnetHost(parsed, address)) {
    issue(
      issues,
      'network.gateway.outside-subnet',
      `${path}.gateway`,
      `gateway ${gateway} is not a host address of subnet ${subnet}`,
    );
  }
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
      `node of kind "${node.kind}" has no "${node.kind}" payload`,
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
        'a connection point needs an ID',
      );
    } else if (handleOwner.has(handle.id)) {
      issue(
        issues,
        'interface.id.duplicate',
        `${handlePath}.id`,
        `duplicate connection point ID "${handle.id}" (node "${handleOwner.get(handle.id).id}" also has it)`,
      );
    } else {
      handleOwner.set(handle.id, node);
    }

    validateUUID(
      issues,
      `${handlePath}.id`,
      'connection point',
      handle.id,
      'interface.id.invalid',
    );

    if (!trimSpace(String(handle.name || ''))) {
      issue(
        issues,
        'interface.name.required',
        `${handlePath}.name`,
        'an interface needs a name',
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
      `included topology name ${goQuoted(String(name))} must not be blank or contain whitespace`,
    );
  } else if (!(doc.source?.includeTopologies || []).length) {
    issue(
      issues,
      'device.included-from.no-includes',
      `${path}.device.includedFrom`,
      `device is from included topology "${name}", but the document includes no topologies`,
    );
  }
}

// The payload of the device node at index (validateDevice in validate.go).
// seenHostnames maps each folded hostname to its index. handleOwner maps each
// connection point id to its node.
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
      'a device needs a hostname',
    );
  } else if (WHITESPACE.test(hostname)) {
    issue(
      issues,
      'node.hostname.whitespace',
      `${path}.device.hostname`,
      `hostname ${goQuoted(String(hostname))} must not contain whitespace`,
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
      'a device needs a spec',
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
  validateLayer(
    node.device.layer,
    `${path}.device.layer`,
    issues,
    'device.layer.out-of-range',
  );
  validateHideDescription(
    node.device.hideDescription,
    `${path}.device.hideDescription`,
    issues,
    'device.hide-description.not-boolean',
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
      issue(issues, 'node.id.required', `${path}.id`, 'a node needs an ID');
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
      validateLayer(
        node.switch.layer,
        `${path}.switch.layer`,
        issues,
        'switch.layer.out-of-range',
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
      validateHideDescription(
        node.group.hideDescription,
        `${path}.group.hideDescription`,
        issues,
        'group.hide-description.not-boolean',
      );
    }

    if (node.kind === 'shape' && isPlainObject(node.shape)) {
      validateShape(node.shape, `${path}.shape`, issues);
    }

    if (node.kind === 'icon' && isPlainObject(node.icon)) {
      validateIconNode(node.icon, `${path}.icon`, issues);
    }

    if (node.kind === 'line' && isPlainObject(node.line)) {
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

// What is known of the chain of parents from a node ID (chainState in
// validate.go).
const CHAIN_WALKING = 'walking';
const CHAIN_ENDS = 'ends';
const CHAIN_CYCLES = 'cycles';

// Whether the chain of parents from the node with the ID leads into a cycle
// (parentCycle in validate.go). states keeps results, so it walks a node once.
function parentCycle(id, nodesById, states) {
  const walk = [];
  let state = CHAIN_ENDS;
  let current = id;

  for (;;) {
    const node = nodesById.get(current);

    if (!node) {
      break;
    }

    const known = states.get(current);

    if (known) {
      state = known === CHAIN_WALKING ? CHAIN_CYCLES : known;
      break;
    }

    states.set(current, CHAIN_WALKING);
    walk.push(current);

    if (!node.parentId) {
      break;
    }

    current = node.parentId;
  }

  walk.forEach((walked) => states.set(walked, state));

  return state === CHAIN_CYCLES;
}

function validateParents(doc, issues, nodesById) {
  const states = new Map();

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

    if (parentCycle(node.parentId, nodesById, states)) {
      issue(
        issues,
        'node.parent.cycle',
        path,
        `groups contain each other in a cycle at node "${node.id}"`,
      );
    }
  });
}

function validateEdges(doc, issues, nodesById, networksById) {
  const seenIds = new Map();
  const connected = new Map();

  (doc.edges || []).forEach((edge, index) => {
    const path = `edges[${index}]`;

    if (!trimSpace(String(edge.id || ''))) {
      issue(
        issues,
        'edge.id.required',
        `${path}.id`,
        'a connection needs an ID',
      );
    } else if (seenIds.has(fold(edge.id))) {
      issue(
        issues,
        'edge.id.duplicate',
        `${path}.id`,
        `duplicate connection ID "${edge.id}" (also edges[${seenIds.get(fold(edge.id))}])`,
      );
    } else {
      seenIds.set(fold(edge.id), index);
    }

    validateUUID(
      issues,
      `${path}.id`,
      'connection',
      edge.id,
      'edge.id.invalid',
    );
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
      issue(
        issues,
        'edge.endpoints.same',
        path,
        'connection endpoints must differ',
      );

      return;
    }

    const endpoints = edgeEndpoints(doc, edge, (id) => nodesById.get(id));

    if (!endpoints || !endpoints.handleId) {
      issue(
        issues,
        'edge.endpoints.invalid',
        path,
        'a connection must join one device interface to one switch',
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
        `unknown connection point "${endpoints.handleId}" on device node "${endpoints.device.id}"`,
      );

      return;
    }

    if (connected.has(endpoints.handleId)) {
      issue(
        issues,
        'edge.interface.connected',
        path,
        `interface "${handle.name}" of device "${endpoints.device.device.hostname}" already has another connection, edges[${connected.get(endpoints.handleId)}]`,
      );
    } else {
      connected.set(endpoints.handleId, index);
    }

    if (!edge.networkId) {
      issue(
        issues,
        'edge.network.required',
        `${path}.networkId`,
        'connection must reference a network',
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

/** Why a document may not list a Scenario config name, or '' (validateScenarios in validate.go). */
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
      message: 'a scenario needs a name',
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
      `a document can have at most ${MAX_SCENARIOS} scenarios, not ${scenarios.length}`,
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

// The layers of a list at path (layerListIssues in layerset.go): at most
// MAX_LAYERS, each with a layer ID that no other layer has, and each layer
// one that layerIssues accepts.
function layerListIssues(layers, path, issues) {
  const list = Array.isArray(layers) ? layers : [];

  if (list.length > MAX_LAYERS) {
    issue(
      issues,
      'layer.list.too-many',
      path,
      `there can be at most ${MAX_LAYERS} layers, not ${list.length}`,
    );
  }

  const seen = new Map();

  list.forEach((layer, index) => {
    const at = `${path}[${index}]`;
    const id = layer?.id;

    if (!isLayerId(id)) {
      issue(issues, 'layer.id.out-of-range', `${at}.id`, LAYER_ID_RANGE);
    } else if (seen.has(id)) {
      issue(
        issues,
        'layer.id.duplicate',
        `${at}.id`,
        `duplicate layer ID ${id} (also ${path}[${seen.get(id)}])`,
      );
    } else {
      seen.set(id, index);
    }

    layerIssues(layer, at, issues);
  });
}

// One layer at path, apart from its ID (layerIssues in layerset.go): a
// label that is not blank, is at most MAX_LAYER_LABEL_BYTES and holds no
// control character (only the first of its problems is reported), an icon
// key of the registry, a custom icon that is an icon name, a color that is
// none or #rrggbb, and node matchers that matcherIssues accepts.
function layerIssues(layer, path, issues) {
  const label = typeof layer?.label === 'string' ? layer.label : '';

  if (trimSpace(label) === '') {
    issue(
      issues,
      'layer.label.required',
      `${path}.label`,
      'a layer needs a label',
    );
  } else if (utf8Length(label) > MAX_LAYER_LABEL_BYTES) {
    issue(
      issues,
      'layer.label.too-long',
      `${path}.label`,
      `layer label must be at most ${MAX_LAYER_LABEL_BYTES} bytes`,
    );
  } else if (hasControlCharacters(label)) {
    issue(
      issues,
      'layer.label.control',
      `${path}.label`,
      'layer label must not contain control characters',
    );
  }

  validateIconKey(
    layer?.iconKey,
    `${path}.iconKey`,
    issues,
    'layer.icon-key.unknown',
  );
  validateIconRef(layer?.icon, `${path}.icon`, issues, 'layer.icon.invalid');

  const color = colorProblem(layer?.color);

  if (color) {
    issue(issues, 'layer.color.invalid', `${path}.color`, color);
  }

  matcherIssues(layer?.matchers, `${path}.matchers`, issues);
}

// The node matchers of a layer (matcherIssues in layerset.go). The server uses
// RE2, but the editor also applies each pattern, so a pattern must read the
// same in JavaScript and stay quick (matcherPatternRule in layerMatchers.js).
function matcherIssues(matchers, path, issues) {
  if (!isPlainObject(matchers)) {
    return;
  }

  const fields = Object.keys(matchers).sort();

  if (fields.length > MAX_LAYER_MATCHERS) {
    issue(
      issues,
      'layer.matchers.too-many',
      path,
      `a layer can have at most ${MAX_LAYER_MATCHERS} node matchers, not ${fields.length}`,
    );
  }

  fields.forEach((field) => {
    const at = `${path}.${field}`;
    const fieldRule = matcherPathRule(field);

    if (fieldRule === 'too-long') {
      issue(
        issues,
        'layer.matcher-path.too-long',
        at,
        `node matcher field must be at most ${MAX_MATCHER_PATH_BYTES} bytes`,
      );
    } else if (fieldRule) {
      issue(
        issues,
        'layer.matcher-path.invalid',
        at,
        `node matcher field ${quoted(field)} must be keys of letters, digits, "_" or "-", joined by "."`,
      );
    }

    const pattern = matchers[field];
    const patternRule = matcherPatternRule(pattern ?? '');

    if (patternRule === 'required') {
      issue(
        issues,
        'layer.matcher-pattern.required',
        at,
        'a node matcher needs a pattern',
      );
    } else if (patternRule === 'too-long') {
      issue(
        issues,
        'layer.matcher-pattern.too-long',
        at,
        `node matcher pattern must be at most ${MAX_MATCHER_PATTERN_BYTES} bytes`,
      );
    } else if (patternRule === 'nested') {
      issue(
        issues,
        'layer.matcher-pattern.invalid',
        at,
        `node matcher pattern ${quoted(pattern)} repeats a group that has a repeat or "|" in it`,
      );
    } else if (patternRule === 'repeats') {
      issue(
        issues,
        'layer.matcher-pattern.invalid',
        at,
        `node matcher pattern ${quoted(pattern)} has more than ${MAX_MATCHER_REPEATS} *, + or {n,} repeats`,
      );
    } else if (patternRule) {
      issue(
        issues,
        'layer.matcher-pattern.invalid',
        at,
        `node matcher pattern ${quoted(pattern)} is not a regular expression`,
      );
    }
  });
}

// What the server says of a layer set version that is not an integer of 1
// or more (layerSetVersionMessage in layerset.go).
const LAYER_SET_VERSION = 'layer set version must be an integer of 1 or more';

// Why a match conflict rule is not one, in the server's words
// (matchConflictProblem in layerset.go), or ''. None is the lowest.
function matchConflictProblem(rule) {
  return unset(rule) || MATCH_CONFLICTS.includes(rule)
    ? ''
    : `unknown match conflict rule ${quoted(rule)} (expected one of ${MATCH_CONFLICTS.join(', ')})`;
}

// The name of a layer set at path, in the server's words. A set needs one.
// A document's options may have none.
function layerSetNameIssues(name, path, issues, required) {
  const text = typeof name === 'string' ? name : '';

  if (required && trimSpace(text) === '') {
    issue(issues, 'layerset.name.required', path, 'a layer set needs a name');
  } else if (utf8Length(text) > MAX_LAYER_SET_NAME_BYTES) {
    issue(
      issues,
      'layerset.name.too-long',
      path,
      `layer set name must be at most ${MAX_LAYER_SET_NAME_BYTES} bytes`,
    );
  } else if (hasControlCharacters(text)) {
    issue(
      issues,
      'layerset.name.control',
      path,
      'layer set name must not contain control characters',
    );
  }
}

// Joins the path of a part to the path of what holds it, '' for the root.
function joinPath(path, key) {
  return path ? `${path}.${key}` : key;
}

// The layer set options of the document (documentLayerSetIssues in
// layerset.go): a name a set may have (empty is allowed), a known match
// conflict rule, and a source with a layer set ID and a version of 1 or
// more.
function validateLayerSetOptions(doc, issues) {
  const options = doc.layerSet;

  if (!isPlainObject(options)) {
    return;
  }

  layerSetNameIssues(options.name, 'layerSet.name', issues, false);

  const conflict = matchConflictProblem(options.matchConflict);

  if (conflict) {
    issue(
      issues,
      'layerset.match-conflict.unknown',
      'layerSet.matchConflict',
      conflict,
    );
  }

  const { source } = options;

  if (!isPlainObject(source)) {
    return;
  }

  if (!isLayerSetId(source.id)) {
    issue(
      issues,
      'layerset.source-id.invalid',
      'layerSet.source.id',
      `layer set ID ${quoted(source.id ?? '')} must be 1 to ${MAX_LAYER_SET_ID_BYTES} letters, digits, ".", "_", ":" or "-"`,
    );
  }

  if (!Number.isInteger(source.version) || source.version < 1) {
    issue(
      issues,
      'layerset.source-version.invalid',
      'layerSet.source.version',
      LAYER_SET_VERSION,
    );
  }
}

/** What makes a layer set unusable, as LayerSet.Issues in layerset.go finds it. */
export function layerSetIssues(set, path = '') {
  const issues = [];

  layerSetNameIssues(set?.name, joinPath(path, 'name'), issues, true);

  const description =
    typeof set?.description === 'string' ? set.description : '';

  if (utf8Length(description) > MAX_LAYER_SET_DESCRIPTION_BYTES) {
    issue(
      issues,
      'layerset.description.too-long',
      joinPath(path, 'description'),
      `layer set description must be at most ${MAX_LAYER_SET_DESCRIPTION_BYTES} bytes`,
    );
  } else if (hasControlCharacters(description)) {
    issue(
      issues,
      'layerset.description.control',
      joinPath(path, 'description'),
      'layer set description must not contain control characters',
    );
  }

  validateIconKey(
    set?.iconKey,
    joinPath(path, 'iconKey'),
    issues,
    'layerset.icon-key.unknown',
  );
  validateIconRef(
    set?.icon,
    joinPath(path, 'icon'),
    issues,
    'layerset.icon.invalid',
  );

  const conflict = matchConflictProblem(set?.matchConflict);

  if (conflict) {
    issue(
      issues,
      'layerset.match-conflict.unknown',
      joinPath(path, 'matchConflict'),
      conflict,
    );
  }

  const layers = Array.isArray(set?.layers) ? set.layers : [];

  if (layers.length === 0) {
    issue(
      issues,
      'layerset.layers.empty',
      joinPath(path, 'layers'),
      'a layer set must hold at least one layer',
    );
  }

  layerListIssues(layers, joinPath(path, 'layers'), issues);

  return issues;
}

// The layers the document defines and the options of the layer set they
// came from (validateLayers in validate.go).
function validateLayers(doc, issues) {
  layerListIssues(doc.layers, 'layers', issues);
  validateLayerSetOptions(doc, issues);
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
        'an included topology needs a name',
      );
    } else if (WHITESPACE.test(name)) {
      issue(
        issues,
        'include.name.whitespace',
        at,
        `included topology name ${goQuoted(String(name))} must not contain whitespace`,
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
      `the source can have at most ${MAX_ANNOTATIONS} annotations, not ${entries.length}`,
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

// Spec fields that name a VLAN that is not a network or a drive image the
// server lacks. A VLAN in another case is not reported here: typing it
// connects the interface (connectByVLAN in model.js). An external node runs no
// image of its own.
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
      warning: `This diagram has no network named "${vlan}".`,
      issue: `${name}${of} uses VLAN "${vlan}", which is not a network in this diagram`,
    });
  });

  unknownDriveImages(spec, disks).forEach(({ index, image }) => {
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

// The drive images of a spec that are not the server's disk images, matched by
// file name in the same case. Nothing while disks is null or for an external node.
export function unknownDriveImages(spec, disks) {
  if (!disks || spec?.external === true) {
    return [];
  }

  return arrayOf(spec?.hardware?.drives).flatMap((drive, index) => {
    const image = trimmed(drive?.image);

    return !image || disks.has(image.split('/').pop())
      ? []
      : [{ index, image }];
  });
}

// What phenix makes of a started device's hostname: refused, which blocks
// publishing, or warned about. Mirrors checkHostnameKeywords in
// types/version/v1/hostname.go and checkHostnames in types/builder/topology.go.
// Returns null for a hostname phenix takes as it is.
function hostnameFinding(hostname, spec) {
  if (typeof hostname !== 'string' || !hostname || spec?.external != null) {
    return null;
  }

  const refused = (code, problem, reason) => ({
    code,
    message: `hostname "${hostname}" ${problem}, so the device cannot be published: ${reason}`,
    refused: true,
  });
  const windows = goEqualFold(String(spec?.hardware?.os_type ?? ''), 'windows');

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
      `minimega uses "all" as its wildcard VM target, so it refuses to launch a VM named "all". A command that targets a VM by name, such as "vm kill all", acts on every VM in the experiment`,
    );
  }

  if (/^[0-9]+$/.test(hostname)) {
    return refused(
      'node.hostname.numeric',
      'is all digits',
      `minimega reads "vm launch kvm ${hostname}" as a number of VMs to launch, not as a VM name. It also reads a numeric VM target as a VM ID`,
    );
  }

  if (goEqualFold(hostname, MINIMEGA_WILDCARD_VM)) {
    return {
      code: 'node.hostname.reserved-case',
      message: `hostname "${hostname}" differs from the reserved name "all" only by case: minimega accepts it, because its reserved-name check is case-sensitive. But any tool or script that lowercases VM names would treat it as minimega's wildcard for every VM`,
      refused: false,
    };
  }

  if (goEqualFold(hostname, PHENIX_HOSTNAME) && windows) {
    return refused(
      'node.hostname.windows-phenix',
      'cannot be used for a Windows node',
      `the startup app writes the startup script of each Windows node to "<hostname>-startup.ps1" in the startup directory of the experiment. It also puts "phenix-startup.ps1", the startup wrapper that every Windows node runs, in that directory. The two file names collide, so one file overwrites the other`,
    );
  }

  if (goEqualFold(hostname, PHENIX_HOSTNAME)) {
    return {
      code: 'node.hostname.phenix',
      message: `hostname "${hostname}" matches "phenix", the hostname that "phenix image" writes into the images it builds. Until the startup app renames them, other VMs built from those images also report "phenix" to miniccc. So a C2 check that finds this node by hostname, such as delay.c2 without useUUID, can match the wrong VM. If you change the os_type of the node to windows, this hostname also blocks publishing`,
      refused: false,
    };
  }

  return null;
}

// The keys of a spec interface that name a firewall ruleset, with the
// traffic each applies to.
const RULESET_KEYS = [
  { key: 'ruleset_in', direction: 'inbound' },
  { key: 'ruleset_out', direction: 'outbound' },
];

/** The ruleset names a device spec defines, each once, for the Inspector's suggestions. */
export function rulesetNames(spec) {
  return [
    ...new Set(
      arrayOf(spec?.network?.rulesets)
        .map((ruleset) => ruleset?.name)
        .filter((name) => typeof name === 'string' && name !== ''),
    ),
  ];
}

/** The interface names of a device spec, each once, for the Inspector's suggestions. */
export function interfaceNames(spec) {
  return [
    ...new Set(
      arrayOf(spec?.network?.interfaces)
        .map((iface) => iface?.name)
        .filter((name) => typeof name === 'string' && trimSpace(name) !== ''),
    ),
  ];
}

// The firewall rulesets phenix does not apply (rulesetWarnings in
// types/builder/warnings.go). The vrouter app applies them only for a Router or
// Firewall and skips a ruleset_in or ruleset_out that names no ruleset.
function rulesetFindings(spec, hostname, at = '') {
  if (spec?.external != null) {
    return [];
  }

  const rulesets = arrayOf(spec?.network?.rulesets);
  const interfaces = arrayOf(spec?.network?.interfaces);
  const refs = interfaces.flatMap((iface, index) =>
    isPlainObject(iface)
      ? RULESET_KEYS.filter(
          ({ key }) => typeof iface[key] === 'string' && iface[key] !== '',
        ).map(({ key, direction }) => ({
          index,
          key,
          direction,
          name: iface[key],
        }))
      : [],
  );
  const type = typeof spec?.type === 'string' ? spec.type : '';

  if (!isRoutingType(type)) {
    if (rulesets.length === 0 && refs.length === 0) {
      return [];
    }

    const kind = type
      ? `The type of the device is ${goQuoted(type)}`
      : 'The device has no type';

    return [
      {
        code: 'device.rulesets.ignored',
        field: `spec.type`,
        path: `${at}.device.spec.type`,
        warning: `phenix applies firewall rulesets only to a device of type Router or Firewall. Thus phenix ignores the rulesets of this device.`,
        issue: `device ${goQuoted(hostname)} has firewall rulesets, but phenix applies them only to a device of type Router or Firewall. ${kind}`,
      },
    ];
  }

  const names = new Set(rulesetNames(spec));
  const labels = interfaceLabels(interfaces);

  return refs
    .filter(({ name }) => !names.has(name))
    .map(({ index, key, direction, name }) => ({
      code: 'interface.ruleset.unknown',
      field: `spec.network.interfaces.${index}.${key}`,
      path: `${at}.device.spec.network.interfaces[${index}].${key}`,
      warning: `The device has no ruleset named ${goQuoted(name)}. Thus phenix applies no ${direction} firewall to this interface.`,
      issue: `interface ${labels[index]} of ${goQuoted(hostname)} names ruleset ${goQuoted(name)} for ${direction} traffic, but the device has no ruleset of that name. Thus phenix applies no ${direction} firewall to the interface`,
    }));
}

// The interface addresses outside their network's subnet (outsideSubnet in
// types/builder/warnings.go). An included device is its own topology's to fix.
function outsideSubnetFindings(doc, members) {
  return (doc.networks || []).flatMap((network) => {
    const subnet = parseSubnet(network?.subnet);

    if (!subnet) {
      return [];
    }

    return (members.get(network.id) || []).flatMap(({ node, index, iface }) => {
      const address = hostAddress(iface);

      if (
        node.device.includedFrom ||
        address === null ||
        isSubnetHost(subnet, address)
      ) {
        return [];
      }

      const label = interfaceLabels(specInterfaces(node))[index];
      const text = formatIPv4(address);

      return [
        {
          node,
          index,
          field: `spec.network.interfaces.${index}.address`,
          warning: `This address is not a host address of subnet ${network.subnet} of network ${goQuoted(network.name)}.`,
          issue: `IP address ${text} of interface ${label} of ${goQuoted(node.device.hostname ?? '')} is not a host address of subnet ${network.subnet} of network ${goQuoted(network.name)}`,
        },
      ];
    });
  });
}

// The networks whose gateway no interface on them holds (unownedGateway in
// types/builder/warnings.go). Every device counts as a holder.
function unownedGateways(doc, members) {
  return (doc.networks || []).flatMap((network, position) => {
    const gateway = parseIPv4(network?.gateway);
    const subnet = parseSubnet(network?.subnet);

    if (
      gateway === null ||
      (!unset(network.subnet) && !(subnet && isSubnetHost(subnet, gateway))) ||
      (members.get(network.id) || []).some(
        ({ iface }) => hostAddress(iface) === gateway,
      )
    ) {
      return [];
    }

    return [
      {
        position,
        message: `no interface on network ${goQuoted(network.name)} has its gateway address ${network.gateway}. Give that address to a router interface on the network, or change the gateway`,
      },
    ];
  });
}

// The Inspector field that shows a warning about a field: a label or an
// annotation shows it under its map.
function mapField(field) {
  return /^spec\.(labels|annotations)\./.test(field)
    ? field.split('.').slice(0, 2).join('.')
    : field;
}

// Warnings about a device's fields for the Inspector's working copy, keyed by
// JSON Forms data path. The spec replaces the nodeId device's spec when
// addresses are compared (sharedAddresses).
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

  [
    ...specFindings(spec, '', specContext(doc, disks)),
    ...rulesetFindings(spec, ''),
    ...deviceSetupFindings(spec, ''),
  ].forEach((finding) => {
    add(mapField(finding.field), finding.warning);
  });

  const self = (doc?.nodes || []).find((node) => node.id === nodeId);
  const ntpWarning = ntpFieldWarning(doc, spec, self);

  if (ntpWarning) {
    add(`spec.labels`, `ntp-server: ${ntpWarning}`);
  }

  const others = (doc?.nodes || []).filter(
    (node) => node.kind === 'device' && node.id !== nodeId,
  );
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
        `This ${shared.name}${shared.network} is also the address of ${otherUsers(shared)}.`,
      );
    });

  // The working copy's addresses, on the networks of the device's
  // connections and VLANs.
  if (self) {
    const working = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node === self ? { ...self, device: { ...self.device, spec } } : node,
      ),
    };

    outsideSubnetFindings(working, interfacesByNetwork(working))
      .filter((finding) => finding.node.id === nodeId)
      .forEach((finding) => add(finding.field, finding.warning));
  }

  return warnings;
}

// The Inspector's warning about the ntp-server label of a spec, or null.
// phenix reads only the first of the diagram's own devices with the label
// (see ntpFindings), so another device gets the warning that it is ignored.
function ntpFieldWarning(doc, spec, self) {
  const labels = objectAt(spec, 'labels');

  if (!Object.hasOwn(labels, NTP_SERVER)) {
    return null;
  }

  const first = self
    ? (doc?.nodes || []).find(
        (node) =>
          node.kind === 'device' &&
          !node.device?.includedFrom &&
          Object.hasOwn(
            node === self ? labels : objectAt(node.device?.spec, 'labels'),
            NTP_SERVER,
          ),
      )
    : self;

  if (first && first !== self) {
    return `phenix uses only the first device with this label, ${goQuoted(first.device?.hostname ?? '')}. Thus phenix ignores the label here.`;
  }

  const value = labels[NTP_SERVER];

  return typeof value === 'string'
    ? (ntpLabelProblem(spec, '', value)?.warning ?? null)
    : null;
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

    return uses.get(name) > 1
      ? `${goQuoted(name)} (#${index + 1})`
      : goQuoted(name);
  });
}

// What publishing does with each spec interface that is not connected on the
// canvas, including interfaces with no connection point. phenix matches VLAN
// names exactly, so an interface goes only on a network of exactly that name.
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
          `${subject} is not connected to a network and has no VLAN. Thus you cannot publish the interface. Connect it, or type a VLAN for it`,
          blocks,
        );
      } else if (unnamed) {
        warn(
          missing,
          `${subject} has no name and no VLAN. Thus you cannot publish the interface. Name it. Then connect it, or type a VLAN for it`,
          blocks,
        );
      } else if (shared) {
        warn(
          missing,
          `${subject} has no VLAN, and another interface has the same name. Thus you cannot connect or publish the interface. Rename it. Then connect it, or type a VLAN for it`,
          blocks,
        );
      } else {
        warn(
          missing,
          `${subject} has no VLAN and no connection point on the canvas. Thus you cannot publish the interface. Type a VLAN for it`,
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
        `${subject} is not connected on the canvas, but its VLAN "${vlan}" puts it on network ${network.name} when you publish the diagram`,
      );
    } else if (cased) {
      warn(
        'interface.vlan.case-mismatch',
        `${subject} is not connected on the canvas, and its VLAN "${vlan}" differs from network ${cased.name} only in case. phenix treats them as different VLANs. Thus publishing does not put the interface on network ${cased.name}`,
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

/** An IPv4 address as four numbers, or null. Like netip.ParseAddr, it takes no leading zeros. */
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

// The IP address an interface uses and a key the same in every form, parsed as
// interfaceIP in types/builder/topology.go does. A manual interface's address
// is not read. A QinQ interface's address is read, because minirouter and
// Vyatta give it to the interface.
function interfaceIP(iface) {
  const proto = typeof iface.proto === 'string' ? iface.proto : '';

  if (
    typeof iface.address !== 'string' ||
    UNADDRESSED_PROTOS.has(goToLower(trimASCIISpace(proto)))
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

// The MAC address an interface uses and a key the same in any case and with
// any separators. Returns null for a blank or malformed MAC.
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

// The network each spec interface is on after publishing (interfaceNetwork in
// types/builder/topology.go), or null for an entry of the wrong shape.
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

// The interfaces that share an IP or MAC address with another interface on the
// same published network (checkInterfaceAddresses in types/builder/topology.go).
// Included devices count but are not reported. An external device's MACs do
// not count. Lookups go by network and address, so the check stays linear.
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

// The warnings about the addresses of the networks and of the interfaces on
// them, as the server gives them (addressingWarnings in
// types/builder/warnings.go): an interface address outside its network's
// subnet, a gateway that no interface has, and subnets that overlap.
function addressingWarnings(doc, issues) {
  const members = interfacesByNetwork(doc);
  const positions = new Map(
    (doc.nodes || []).map((node, position) => [node, position]),
  );

  outsideSubnetFindings(doc, members).forEach((finding) => {
    issue(
      issues,
      'interface.ip.outside-subnet',
      `nodes[${positions.get(finding.node)}].device.spec.network.interfaces[${finding.index}].address`,
      finding.issue,
      'warning',
      { field: finding.field },
    );
  });

  unownedGateways(doc, members).forEach(({ position, message }) => {
    issue(
      issues,
      'network.gateway.unowned',
      `networks[${position}].gateway`,
      message,
      'warning',
    );
  });

  const networks = doc.networks || [];

  overlappingSubnets(networks).forEach(({ index, other }) => {
    issue(
      issues,
      'network.subnet.overlap',
      `networks[${index}].subnet`,
      `subnet ${networks[index].subnet} of network ${goQuoted(networks[index].name)} overlaps subnet ${networks[other].subnet} of network ${goQuoted(networks[other].name)}. Thus a router cannot route between them. Ignore this warning for networks that never connect, such as isolated enclaves that reuse a subnet on purpose`,
      'warning',
    );
  });
}

// The labels and annotations of a node spec that phenix's apps read.
const NTP_SERVER = 'ntp-server';
const DEFAULT_APPS = 'phenix/default-apps';
const AUTOTUNNEL = 'phenix/startup-autotunnel';
const ENABLE_SSH = 'vrouter/enable-ssh';

// os_type values, in lowercase, as phenix's apps compare them: see the
// lists of the same names in types/builder/devicewarnings.go.
const VYATTA_OS_TYPES = ['vyatta', 'vyos', 'linux'];
const ROUTER_OS_TYPES = ['vyatta', 'vyos', 'minirouter'];
const STARTUP_OS_TYPES = ['linux', 'rhel', 'centos', 'windows'];

const TUNNEL_PORT = /^[0-9]{1,5}$/;
const MAX_TUNNEL_PORT = 65535;
const TUNNEL_FORMS = '"port", "port:dport" or "port:host:dport"';

function textOf(value) {
  return typeof value === 'string' ? value : '';
}

// The object at a key of a spec object, or an empty one.
function objectAt(value, key) {
  return isPlainObject(value) && isPlainObject(value[key]) ? value[key] : {};
}

// Whether phenix runs its default apps on a node: unless its
// phenix/default-apps annotation is false.
function defaultAppsOn(spec) {
  return objectAt(spec, 'annotations')[DEFAULT_APPS] !== false;
}

function doNotBoot(spec) {
  return objectAt(spec, 'general').do_not_boot === true;
}

function osPhrase(osType) {
  return osType
    ? `The os_type of the device is ${goQuoted(osType)}`
    : 'The device has no os_type';
}

function interfacesPhrase(labels) {
  return labels.length === 1
    ? `interface ${labels[0]}`
    : `interfaces ${labels.join(', ')}`;
}

// The index of the first spec interface whose name is name in any case, as
// phenix finds an interface by name, or -1.
function interfaceByName(interfaces, name) {
  return interfaces.findIndex(
    (iface) => isPlainObject(iface) && goEqualFold(textOf(iface.name), name),
  );
}

function interfaceAddress(iface) {
  return isPlainObject(iface) ? textOf(iface.address) : '';
}

// A finding at a spec field of a device: field is its JSON Forms data path
// (spec.labels.ntp-server), and the path has brackets for indexes.
function fieldFinding(at, field, finding) {
  const below = field
    .split('.')
    .map((part) => (/^\d+$/.test(part) ? `[${part}]` : `.${part}`))
    .join('');

  return { ...finding, field, path: `${at}.device${below}` };
}

// An IPv4 address as net.ParseIP and To4 read it, an IPv4 address mapped
// into IPv6 too, as a number, or null.
function parsedIPv4(text) {
  const octets = ipv4Octets(text);

  if (octets) {
    return octets.reduce((value, octet) => value * 256 + octet, 0);
  }

  const groups = text.includes('%') ? null : ipv6Groups(text);

  return groups &&
    groups.slice(0, 5).every((group) => group === 0) &&
    groups[5] === 0xffff
    ? groups[6] * 65536 + groups[7]
    : null;
}

// Whether text is an IP address, as net.ParseIP reads it.
function isIP(text) {
  return (
    ipv4Octets(text) !== null ||
    (!text.includes('%') && ipv6Groups(text) !== null)
  );
}

// An IPv4 network as net.ParseCIDR reads it, host bits allowed, as
// {base, size}, or null.
function ipv4Network(text) {
  const slash = text.indexOf('/');
  const octets = slash < 0 ? null : ipv4Octets(text.slice(0, slash));
  const bits = text.slice(slash + 1);

  if (!octets || !/^\d+$/.test(bits) || Number(bits) > 32) {
    return null;
  }

  const size = 2 ** (32 - Number(bits));
  const address = octets.reduce((value, octet) => value * 256 + octet, 0);

  return { base: address - (address % size), size };
}

/** What phenix's default apps do not configure as a spec draws it (deviceSetupWarnings). */
function setupFindings(spec, hostname, at) {
  const findings = [];
  const deviceType = textOf(spec?.type);
  const osType = textOf(objectAt(spec, 'hardware').os_type);
  const lower = goToLower(osType);
  const interfaces = arrayOf(objectAt(spec, 'network').interfaces);
  const labels = interfaceLabels(interfaces);
  const osFinding = (code, warning, text) =>
    findings.push(
      fieldFinding(at, 'spec.hardware.os_type', { code, warning, issue: text }),
    );

  if (defaultAppsOn(spec)) {
    const staticLabels = [];
    const serialLabels = [];

    interfaces.forEach((iface, index) => {
      if (!isPlainObject(iface)) {
        return;
      }

      if (iface.type === 'serial') {
        serialLabels.push(labels[index]);
      } else if (['static', 'ospf'].includes(goToLower(textOf(iface.proto)))) {
        staticLabels.push(labels[index]);
      }
    });

    const routing = isRoutingType(deviceType);

    if (routing && lower === 'linux') {
      osFinding(
        'device.router-os.unsupported',
        'phenix configures a router of os_type linux as vyatta, but phenix deprecated it.',
        `device ${goQuoted(hostname)} is a ${deviceType} with os_type ${goQuoted(osType)}. phenix configures this os_type as vyatta, but phenix deprecated it for routers. Set hardware.os_type to vyatta, vyos or minirouter`,
      );
    } else if (routing && !ROUTER_OS_TYPES.includes(lower)) {
      osFinding(
        'device.router-os.unsupported',
        'phenix configures a router only when its os_type is vyatta, vyos or minirouter.',
        `device ${goQuoted(hostname)} is a ${deviceType}, but phenix configures a router only when its os_type is vyatta, vyos or minirouter. ${osPhrase(osType)}. Thus phenix does not configure its routing or firewall. Set hardware.os_type to vyatta, vyos or minirouter`,
      );
    } else if (
      !routing &&
      osType &&
      !STARTUP_OS_TYPES.includes(lower) &&
      staticLabels.length
    ) {
      osFinding(
        'device.interfaces.unconfigured',
        'phenix writes no interface configuration for this os_type. Thus the static interfaces get no address from phenix.',
        `device ${goQuoted(hostname)} has os_type ${goQuoted(osType)}, for which phenix writes no interface configuration. Thus phenix gives its static ${interfacesPhrase(staticLabels)} no address. Set hardware.os_type to linux, rhel, centos or windows, or set the addresses in its image`,
      );
    }

    if (osType !== 'linux' && serialLabels.length) {
      osFinding(
        'device.serial.unconfigured',
        'phenix configures serial interfaces only when the os_type is exactly linux.',
        `device ${goQuoted(hostname)} has serial ${interfacesPhrase(serialLabels)}, but phenix configures serial interfaces only on a device whose os_type is exactly "linux". ${osPhrase(osType)}. Set hardware.os_type to linux`,
      );
    }
  }

  const injection = injectionsFinding(spec, hostname, at);

  return injection ? [...findings, injection] : findings;
}

// The file injections and deletions of a device that minimega does not
// apply, or null (injectionsWarning in types/builder/devicewarnings.go).
function injectionsFinding(spec, hostname, at) {
  const injections = arrayOf(spec?.injections).length;
  const deletions = arrayOf(spec?.deletions).length;

  if (injections + deletions === 0 || doNotBoot(spec)) {
    return null;
  }

  let files = 'file injections and deletions';

  if (deletions === 0) {
    files = 'file injections';
  } else if (injections === 0) {
    files = 'file deletions';
  }

  if (objectAt(spec, 'general').snapshot === false) {
    return fieldFinding(at, `spec.general.snapshot`, {
      code: 'device.injections.unapplied',
      warning:
        'Without a snapshot, phenix neither injects nor deletes the files of this device.',
      issue: `device ${goQuoted(hostname)} has ${files}, but its general.snapshot is false. Thus phenix neither injects nor deletes its files. Set general.snapshot to true`,
    });
  }

  const drives = arrayOf(objectAt(spec, 'hardware').drives);

  if (
    !drives.length ||
    !isPlainObject(drives[0]) ||
    drives[0].inject_partition !== 0
  ) {
    return null;
  }

  return fieldFinding(at, 'spec.hardware.drives.0.inject_partition', {
    code: 'device.injections.unapplied',
    warning:
      'With inject partition 0, phenix neither injects nor deletes the files of this device.',
    issue: `device ${goQuoted(hostname)} has ${files}, but the inject_partition of its first drive is 0. Thus phenix neither injects nor deletes its files. Set hardware.drives[0].inject_partition to the partition that holds the file system`,
  });
}

// The lowercase os_type of a device that the vrouter app takes as a router:
// a Router or Firewall whose default apps are on. null for another device.
function routerOS(spec) {
  return isRoutingType(textOf(spec?.type)) && defaultAppsOn(spec)
    ? goToLower(textOf(objectAt(spec, 'hardware').os_type))
    : null;
}

/** A router's OSPF findings (ospfError, which blocks publishing, and ospfWarnings). */
function ospfFindings(spec, hostname, at) {
  const osType = routerOS(spec);
  const network = objectAt(spec, 'network');
  const interfaces = arrayOf(network.interfaces);
  const labels = interfaceLabels(interfaces);

  if (VYATTA_OS_TYPES.includes(osType)) {
    const index = interfaces.findIndex(
      (iface) => isPlainObject(iface) && iface.proto === 'ospf',
    );

    return network.ospf != null || index < 0
      ? []
      : [
          fieldFinding(at, `spec.network.interfaces.${index}.proto`, {
            code: 'device.ospf.missing',
            warning:
              'The device has no network.ospf, which this proto needs. Thus the experiment fails to start.',
            issue: `interface ${labels[index]} of ${goQuoted(hostname)} has proto ospf, but the device has no network.ospf. Thus phenix cannot write its router configuration. Thus the experiment fails to start. Add network.ospf to the device, or set the proto of the interface to static`,
            blocksPublish: true,
          }),
        ];
  }

  if (osType !== 'minirouter' || doNotBoot(spec)) {
    return [];
  }

  return minirouterOSPF(network, interfaces, labels, hostname, at);
}

// The OSPF warnings of a minirouter (see ospfFindings).
function minirouterOSPF(network, interfaces, labels, hostname, at) {
  const indexes = interfaces.flatMap((iface, index) =>
    isPlainObject(iface) && goEqualFold(textOf(iface.proto), 'ospf')
      ? [index]
      : [],
  );
  const outside = (index, warning, text) =>
    fieldFinding(at, `spec.network.interfaces.${index}.address`, {
      code: 'interface.ospf.outside-area',
      warning,
      issue: text,
    });

  if (network.ospf == null) {
    return indexes.map((index) =>
      outside(
        index,
        'The device has no network.ospf. Thus this interface is in no OSPF area.',
        `interface ${labels[index]} of ${goQuoted(hostname)} has proto ospf, but the device has no network.ospf. Thus the interface is in no OSPF area. Add network.ospf with an area network that holds the address of the interface`,
      ),
    );
  }

  if (!indexes.length) {
    return [
      fieldFinding(at, `spec.network.ospf`, {
        code: 'device.ospf.unused',
        warning:
          'No interface of the device has proto ospf. Thus minirouter runs OSPF on no interface.',
        issue: `device ${goQuoted(hostname)} has network.ospf, but none of its interfaces has proto ospf. Thus minirouter runs OSPF on no interface. Set the proto of the interfaces that must run OSPF to ospf, or remove network.ospf`,
      }),
    ];
  }

  const ospf = isPlainObject(network.ospf) ? network.ospf : {};
  const networks = arrayOf(ospf.areas).flatMap((area, a) =>
    arrayOf(isPlainObject(area) ? area.area_networks : null).flatMap(
      (entry, n) => {
        const text = isPlainObject(entry) ? textOf(entry.network) : '';
        const range = ipv4Network(text);

        return range ? [{ a, n, text, ...range }] : [];
      },
    ),
  );
  const used = networks.map(() => false);
  const findings = [];

  indexes.forEach((index) => {
    const address = parsedIPv4(interfaceAddress(interfaces[index]));

    if (address === null) {
      return;
    }

    let held = false;

    networks.forEach(({ base, size }, n) => {
      if (address >= base && address < base + size) {
        used[n] = true;
        held = true;
      }
    });

    if (!held) {
      findings.push(
        outside(
          index,
          'This address is in no area network of the device. Thus the interface is in no OSPF area.',
          `IP address ${formatIPv4(address)} of interface ${labels[index]} of ${goQuoted(hostname)} is in no area network of its network.ospf. Thus the interface is in no OSPF area. Add an area network that holds the address`,
        ),
      );
    }
  });

  networks.forEach(({ a, n, text }, position) => {
    if (!used[position]) {
      findings.push(
        fieldFinding(
          at,
          `spec.network.ospf.areas.${a}.area_networks.${n}.network`,
          {
            code: 'device.ospf-network.unused',
            warning:
              'No interface with proto ospf has an address in this network. Thus minirouter does not advertise the network.',
            issue: `OSPF area network ${text} of ${goQuoted(hostname)} holds the address of no interface with proto ospf. Thus minirouter does not advertise the area network. Give an ospf interface of the device an address in the area network, or remove the area network`,
          },
        ),
      );
    }
  });

  return findings;
}

/** The annotation values phenix reads (annotationWarnings in types/builder/devicewarnings.go). */
function annotationFindings(spec, hostname, at) {
  const annotations = objectAt(spec, 'annotations');
  const findings = [];
  const add = (code, key, warning, text) =>
    findings.push(
      fieldFinding(at, `spec.annotations.${key}`, {
        code,
        warning,
        issue: text,
      }),
    );

  // Only the vrouter app reads vrouter/enable-ssh, for a vyatta-template router.
  if (
    Object.hasOwn(annotations, ENABLE_SSH) &&
    VYATTA_OS_TYPES.includes(routerOS(spec))
  ) {
    const problem = enableSSHProblem(spec, hostname, annotations[ENABLE_SSH]);

    if (problem) {
      add(
        'device.enable-ssh.invalid',
        ENABLE_SSH,
        `vrouter/enable-ssh: ${problem.warning}`,
        problem.issue,
      );
    }
  }

  if (Object.hasOwn(annotations, AUTOTUNNEL)) {
    autotunnelProblems(hostname, annotations[AUTOTUNNEL]).forEach((problem) =>
      add(
        'device.autotunnel.invalid',
        AUTOTUNNEL,
        `phenix/startup-autotunnel: ${problem.warning}`,
        problem.issue,
      ),
    );
  }

  if (
    Object.hasOwn(annotations, DEFAULT_APPS) &&
    typeof annotations[DEFAULT_APPS] !== 'boolean'
  ) {
    add(
      'device.default-apps.invalid',
      DEFAULT_APPS,
      'phenix/default-apps: not true or false. Thus phenix runs its default apps. To disable them, type false without quotes.',
      `the phenix/default-apps annotation of ${goQuoted(hostname)} is not true or false. Thus phenix runs its default apps on the device. To disable them, set the annotation to false, not the text "false"`,
    );
  }

  return findings;
}

// Why the vrouter app does not turn on SSH for a vrouter/enable-ssh value,
// or null (enableSSHProblem in types/builder/devicewarnings.go).
function enableSSHProblem(spec, hostname, value) {
  const fix =
    'Set the annotation to an IP address or to the name of an interface of the device';

  if (typeof value !== 'string') {
    return {
      warning: 'not text. Thus phenix does not enable SSH.',
      issue: `the vrouter/enable-ssh annotation of ${goQuoted(hostname)} is not text. Thus phenix does not enable SSH. ${fix}`,
    };
  }

  if (isIP(value)) {
    return null;
  }

  const interfaces = arrayOf(objectAt(spec, 'network').interfaces);
  const index = interfaceByName(interfaces, value);

  if (index < 0) {
    return {
      warning: `${goQuoted(value)} is neither an IP address nor an interface of this device. Thus phenix does not enable SSH.`,
      issue: `the vrouter/enable-ssh annotation of ${goQuoted(hostname)} is ${goQuoted(value)}, which is neither an IP address nor the name of an interface of the device. Thus phenix does not enable SSH. ${fix}`,
    };
  }

  if (!interfaceAddress(interfaces[index])) {
    const label = interfaceLabels(interfaces)[index];

    return {
      warning: `interface ${label} has no IP address. Thus phenix does not enable SSH.`,
      issue: `the vrouter/enable-ssh annotation of ${goQuoted(hostname)} names interface ${label}, which has no IP address. Thus phenix does not enable SSH. Give the interface an address, or name another interface`,
    };
  }

  return null;
}

// What of a phenix/startup-autotunnel value the startup app does not open
// (autotunnelProblems in types/builder/devicewarnings.go).
function autotunnelProblems(hostname, value) {
  const of = `of the phenix/startup-autotunnel annotation of ${goQuoted(hostname)}`;

  if (value == null) {
    return [];
  }

  if (!Array.isArray(value)) {
    return [
      {
        warning:
          'not a list. Thus phenix opens none of the tunnels. Type a list such as ["8080", "2222:22"].',
        issue: `the phenix/startup-autotunnel annotation of ${goQuoted(hostname)} is not a list. Thus phenix opens none of its tunnels. Set the annotation to a list of texts such as "8080", "2222:22" or "9090:10.0.0.5:80"`,
      },
    ];
  }

  const other = value.findIndex(
    (entry) => typeof entry !== 'string' && entry != null,
  );

  if (other >= 0) {
    return [
      {
        warning: `entry ${other + 1} is not text. Thus phenix opens none of the tunnels.`,
        issue: `entry ${other + 1} ${of} is not text. Thus phenix opens none of its tunnels. Write each tunnel as ${TUNNEL_FORMS}`,
      },
    ];
  }

  return value.flatMap((entry, index) => {
    if (validTunnel(textOf(entry))) {
      return [];
    }

    const shown = entry == null ? 'null' : goQuoted(entry);

    return [
      {
        warning: `entry ${index + 1} is not ${TUNNEL_FORMS}. Thus phenix does not open that tunnel.`,
        issue: `entry ${index + 1} ${of} is ${shown}, which is not ${TUNNEL_FORMS}. Thus phenix does not open that tunnel`,
      },
    ];
  });
}

// Whether an autotunnel entry is "port", "port:dport" or "port:host:dport",
// with ports from 1 to 65535. A dport can also be VNC, in any case, which
// needs no host.
function validTunnel(text) {
  const parts = text.split(':');
  const port = (part) =>
    TUNNEL_PORT.test(part) &&
    Number(part) >= 1 &&
    Number(part) <= MAX_TUNNEL_PORT;
  const vnc = (part) => goEqualFold(part, 'VNC');

  switch (parts.length) {
    case 1:
      return port(parts[0]);
    case 2:
      return port(parts[0]) && (port(parts[1]) || vnc(parts[1]));
    case 3:
      return (
        port(parts[0]) && (vnc(parts[2]) || (parts[1] !== '' && port(parts[2])))
      );
    default:
      return false;
  }
}

/** Every app setup finding for a device that phenix starts, which no external device is. */
function deviceSetupFindings(spec, hostname, at = '') {
  if (spec?.external != null) {
    return [];
  }

  return [
    ...drivesFindings(spec, hostname, at),
    ...setupFindings(spec, hostname, at),
    ...ospfFindings(spec, hostname, at),
    ...annotationFindings(spec, hostname, at),
  ];
}

/** A device with no drive, which the phenix schema refuses (drivesError). It blocks publishing. */
function drivesFindings(spec, hostname, at) {
  const { drives } = objectAt(spec, 'hardware');

  if (drives != null && (!Array.isArray(drives) || drives.length)) {
    return [];
  }

  return [
    fieldFinding(at, `spec.hardware.drives`, {
      code: 'device.drives.none',
      warning:
        'The device has no drive, which phenix needs to start it. Add a drive.',
      issue: `device ${goQuoted(hostname)} has no drive, which phenix needs to start it. Add a drive to hardware.drives`,
      blocksPublish: true,
    }),
  ];
}

/** The ntp-server label checks (ntpIssues). An invalid label blocks publishing. */
function ntpFindings(doc, connections) {
  const nodes = doc.nodes || [];
  const servers = nodes.flatMap((node, index) => {
    const labels = objectAt(node?.device?.spec, 'labels');

    return node?.kind === 'device' &&
      !node.device?.includedFrom &&
      Object.hasOwn(labels, NTP_SERVER)
      ? [{ node, index, value: labels[NTP_SERVER] }]
      : [];
  });

  if (!servers.length) {
    return [];
  }

  const [first] = servers;
  const hostname = first.node.device.hostname ?? '';
  const field = `spec.labels.${NTP_SERVER}`;
  const finding = (server, code, text, extra = {}) =>
    fieldFinding(`nodes[${server.index}]`, field, {
      code,
      issue: text,
      ...extra,
    });
  const findings = servers
    .slice(1)
    .map((other) =>
      finding(
        other,
        'device.ntp-server.ignored',
        `device ${goQuoted(other.node.device.hostname ?? '')} has the ntp-server label, but phenix uses only the first device with it, ${goQuoted(hostname)}`,
      ),
    );
  const problem =
    typeof first.value === 'string'
      ? ntpLabelProblem(first.node.device.spec, hostname, first.value)
      : null;

  if (problem) {
    findings.push(
      finding(first, 'device.ntp-server.invalid', problem.issue, {
        blocksPublish: true,
      }),
    );
  }

  const vyattaRouter = nodes.some(
    (node) =>
      node?.kind === 'device' &&
      !node.device?.includedFrom &&
      node.device?.spec?.external == null &&
      VYATTA_OS_TYPES.includes(routerOS(node.device?.spec)),
  );
  const interfaces = specInterfaces(first.node);
  const onMgmt = publishedVLANs(first.node, interfaces, connections).some(
    (vlan) => goEqualFold(textOf(vlan), 'mgmt'),
  );

  if (vyattaRouter && !onMgmt) {
    findings.push(
      finding(
        first,
        'device.ntp-server.no-mgmt',
        `routers take their NTP server address from an interface of ${goQuoted(hostname)} on VLAN mgmt, but the device has no such interface. Thus phenix gives the routers no NTP server, unless an ntp app of the scenario names one. Connect an interface of ${goQuoted(hostname)} to a network named mgmt`,
      ),
    );
  }

  return findings;
}

// Why an ntp-server label value fails the experiment, or null: it names no
// interface of the device, or one with no IP address.
function ntpLabelProblem(spec, hostname, value) {
  const interfaces = arrayOf(objectAt(spec, 'network').interfaces);
  const index = interfaceByName(interfaces, value);

  if (index < 0) {
    return {
      warning: `${goQuoted(value)} names no interface of this device. Thus the experiment fails to start.`,
      issue: `the ntp-server label of ${goQuoted(hostname)} is ${goQuoted(value)}, which names no interface of the device. Thus the experiment fails to start. Set the label to the name of an interface with an IP address`,
    };
  }

  if (!interfaceAddress(interfaces[index])) {
    const label = interfaceLabels(interfaces)[index];

    return {
      warning: `interface ${label} has no IP address. Thus the experiment fails to start.`,
      issue: `the ntp-server label of ${goQuoted(hostname)} names interface ${label}, which has no IP address. Thus the experiment fails to start. Give the interface an address, or name another interface`,
    };
  }

  return null;
}

// Advisory checks shown while editing. None blocks saving. Those that block
// publishing set blocksPublish, and the server refuses to publish them
// (PublishTopology in types/builder/topology.go).
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

    [
      ...rulesetFindings(
        node.device?.spec,
        node.device?.hostname ?? '',
        `nodes[${index}]`,
      ),
      ...deviceSetupFindings(
        node.device?.spec,
        node.device?.hostname ?? '',
        `nodes[${index}]`,
      ),
    ].forEach((finding) => {
      issue(issues, finding.code, finding.path, finding.issue, 'warning', {
        field: finding.field,
        ...(finding.blocksPublish ? { blocksPublish: true } : {}),
      });
    });

    if (specInterfaces(node).length === 0) {
      issue(
        issues,
        'device.interfaces.none',
        `nodes[${index}]`,
        `device "${node.device?.hostname}" has no interfaces`,
        'warning',
        { field: `spec.network.interfaces` },
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

  ntpFindings(doc, connections).forEach((finding) => {
    issue(issues, finding.code, finding.path, finding.issue, 'warning', {
      field: finding.field,
      ...(finding.blocksPublish ? { blocksPublish: true } : {}),
    });
  });
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
      `${shared.name} ${shared.text} of interface ${shared.label} of "${shared.hostname}"${shared.network} is also the address of ${otherUsers(shared)}`,
      'warning',
      {
        blocksPublish: true,
        field: `spec.network.interfaces.${shared.index}.${shared.field}`,
      },
    );
  });

  addressingWarnings(doc, issues);

  const switched = new Set(
    (doc.nodes || [])
      .filter((node) => node.kind === 'switch')
      .map((node) => node.switch?.networkId),
  );

  // Deleting the last switch of a network removes the network too, unless
  // something still uses it (see dropUnusedNetworks in model.js).
  (doc.networks || []).forEach((network, index) => {
    if (!switched.has(network.id)) {
      issue(
        issues,
        'network.switch.missing',
        `networks[${index}]`,
        `network "${network.name}" has no switch on the canvas. Add a switch for it, change each VLAN that names it, or use Remove network`,
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

// Validates a document exactly as the server does, plus editing warnings.
// Issues are sorted by path, with codes from schema/codes.json.
export function validateDocument(doc, { disks = null } = {}) {
  const issues = [];

  if (!doc || typeof doc !== 'object') {
    issue(issues, 'document.content.missing', '', 'there is no document');

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
  validateLayers(doc, issues);
  validateSource(doc, issues);
  validateTemplates(doc, issues);
  issues.push(...validateIcons(doc.icons));
  collectWarnings(doc, issues, specContext(doc, disks));

  return issues
    .map((entry) => locate(doc, entry))
    .sort((a, b) => a.path.localeCompare(b.path));
}
