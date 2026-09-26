// Inspector validation: the ajv instance JSON Forms validates with, which
// errors keep an edit from being applied (see workingCopyErrors), and the
// plain-language messages the Inspector shows for them.
//
// Raw ajv messages ("must match pattern "^\S+$"", "is a required property")
// name no field and describe the schema rather than the fix, so every message
// here starts with the field's label and says what a valid value looks like.

import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

// time.ParseDuration's syntax, which phenix reads a delay timer with: an
// optional sign, then 0 or numbers each with a unit, such as 250ms, 1.5h or
// 1h30m. Microseconds are us, or µs written with either mu (U+00B5 or
// U+03BC).
const GO_DURATION =
  /^[-+]?(?:0|(?:(?:\d+\.?\d*|\.\d+)(?:ns|[u\u00b5\u03bc]s|ms|s|m|h))+)$/;
const GO_DURATION_PART = /(\d+\.?\d*|\.\d+)(ns|[u\u00b5\u03bc]s|ms|s|m|h)/g;
const NANOSECONDS = { ns: 1, us: 1e3, ms: 1e6, s: 1e9, m: 6e10, h: 3.6e12 };

/**
 * Whether time.ParseDuration reads text as a duration: in its syntax, and
 * within the roughly 292 years a Go duration holds. phenix starts a node
 * whose delay timer it cannot read with no delay at all.
 *
 * @param {string} text
 * @returns {boolean}
 */
export function isGoDuration(text) {
  if (!GO_DURATION.test(text)) {
    return false;
  }

  let nanoseconds = 0;

  for (const [, number, unit] of text.matchAll(GO_DURATION_PART)) {
    nanoseconds +=
      Number(number) * NANOSECONDS[unit.replace(/^[\u00b5\u03bc]/, 'u')];
  }

  return nanoseconds <= 2 ** 63;
}

const OCTET = '(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
const IPV4_PREFIX = new RegExp(
  `^(${OCTET}(?:\\.${OCTET}){3})/(3[0-2]|[12]?\\d)$`,
);

/**
 * Whether text is an IPv4 network in CIDR notation, such as 10.0.0.0/24:
 * an address without leading zeros, as Go's net.ParseIP reads one, a prefix
 * length from 0 to 32, and no host bits set. The routers phenix configures
 * refuse a static route or an OSPF network with host bits (10.0.0.1/24), as
 * does ip route.
 *
 * @param {string} text
 * @returns {boolean}
 */
export function isIPv4Network(text) {
  const match = IPV4_PREFIX.exec(text);

  if (!match) {
    return false;
  }

  const address = match[1]
    .split('.')
    .reduce((value, octet) => value * 256 + Number(octet), 0);

  return address % 2 ** (32 - Number(match[2])) === 0;
}

// The formats of the builder's own that the Inspector checks phenix spec
// fields with (see SPEC_FORMATS in schema.js). An empty value passes: phenix
// reads an empty delay timer as none, and `required` and `minLength` say
// when a field needs a value.
const BUILDER_FORMATS = {
  'go-duration': isGoDuration,
  'ipv4-network': isIPv4Network,
};

export function createFormValidator() {
  const validator = new Ajv2020({
    allErrors: true,
    verbose: true,
    strict: false,
    addUsedSchema: false,
  });

  addFormats(validator);

  for (const [name, check] of Object.entries(BUILDER_FORMATS)) {
    validator.addFormat(name, (text) => text === '' || check(text));
  }

  return validator;
}

// What each pattern in the builder schema asks for, in words.
const PATTERN_HINTS = {
  '^\\S+$': 'cannot contain spaces',
  '^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$':
    'can use only letters, digits and hyphens, and cannot start or end with a hyphen',
  '^([0-9a-fA-F]{2}[:-]){5}([0-9a-fA-F]){2}$':
    'must be a MAC address, such as 00:11:22:33:44:55',
  '^[\\w-]+$': 'can use only letters, digits, underscores and hyphens',
  '^[^\\x00-\\x1f\\x7f]*$':
    'cannot contain tabs, line breaks or other control characters',
};

const FORMAT_HINTS = {
  'go-duration': 'must be a duration, such as 30s, 5m or 1h30m',
  'ipv4-network':
    'must be an IPv4 network, with the host part zero, such as 10.0.0.0/24',
  ipv4: 'must be an IPv4 address, such as 10.0.0.1',
  ipv6: 'must be an IPv6 address',
  hostname: 'must be a host name',
  uri: 'must be a URL',
  email: 'must be an email address',
};

const TYPE_HINTS = {
  integer: 'must be a whole number',
  number: 'must be a number',
  string: 'must be text',
  boolean: 'must be on or off',
  array: 'must be a list',
  object: 'must be a group of fields',
};

/**
 * Turns a schema key such as `os_type` or `useUUID` into a label, the way
 * JSON Forms does when a property has no title.
 *
 * @param {string} key
 * @returns {string}
 */
export function startCase(key) {
  return String(key ?? '')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join(' ');
}

/**
 * The singular noun for one item of a list field: "Drives" gives "drive",
 * "C2 dependencies" gives "C2 dependency". Acronyms keep their case.
 *
 * @param {string} label the list's label
 * @returns {string}
 */
export function itemNoun(label) {
  let noun = String(label || 'item').trim();

  if (/ies$/i.test(noun)) {
    noun = `${noun.slice(0, -3)}y`;
  } else if (/[^s]s$/i.test(noun)) {
    noun = noun.slice(0, -1);
  }

  return /^[A-Z][a-z]/.test(noun)
    ? noun[0].toLowerCase() + noun.slice(1)
    : noun;
}

function capitalize(text) {
  return text ? text[0].toUpperCase() + text.slice(1) : text;
}

// The property schema for `key` inside an object schema, looking through
// oneOf and anyOf branches (interface variants keep their fields there).
function propertyOf(schema, key) {
  if (schema?.properties?.[key]) {
    return schema.properties[key];
  }

  for (const branch of [...(schema?.oneOf || []), ...(schema?.anyOf || [])]) {
    const found = propertyOf(branch, key);

    if (found) {
      return found;
    }
  }

  return undefined;
}

/**
 * Names the field at a JSON Forms data path (`spec.hardware.drives.0.image`)
 * from the schema's titles.
 *
 * @param {object} schema the form's root schema
 * @param {string} path dot-separated data path
 * @returns {{label: string, context: string}} the field's own label, and the
 *   list items it sits in ("Drive 1"), if any
 */
export function fieldLabel(schema, path) {
  const segments = String(path || '')
    .split('.')
    .filter(Boolean);
  const items = [];
  let current = schema;
  let label = '';

  for (const segment of segments) {
    if (/^\d+$/.test(segment) && current?.items) {
      const item = `${capitalize(itemNoun(label))} ${Number(segment) + 1}`;

      items.push(item);
      label = item;
      current = current.items;
    } else {
      current = propertyOf(current, segment);
      label = current?.title || startCase(segment);
    }
  }

  // A primitive list item is named by its position alone ("Command 2").
  if (items.length && items[items.length - 1] === label) {
    items.pop();
  }

  return { label: label || 'This field', context: items.join(', ') };
}

// The keys from the data's root to the field an ajv error belongs to.
function errorKeys(error) {
  const base = String(error?.instancePath || error?.dataPath || '')
    .split('/')
    .filter(Boolean)
    .map((segment) => segment.replace(/~1/g, '/').replace(/~0/g, '~'));
  const missing =
    error?.keyword === 'required' || error?.keyword === 'dependencies'
      ? error.params?.missingProperty
      : undefined;

  return [...base, ...(missing ? [missing] : [])];
}

/**
 * The data path an ajv error belongs to, in JSON Forms' dot form. Errors about
 * a missing property belong to that property.
 *
 * @param {object} error ajv error
 * @returns {string}
 */
function errorPath(error) {
  return errorKeys(error).join('.');
}

function rangeHint(schema, keyword, limit) {
  const low = schema?.minimum ?? schema?.exclusiveMinimum;
  const high = schema?.maximum ?? schema?.exclusiveMaximum;

  if (low !== undefined && high !== undefined) {
    return `must be between ${low} and ${high}`;
  }

  return keyword.endsWith('inimum')
    ? `must be ${keyword === 'exclusiveMinimum' ? 'more than' : 'at least'} ${limit}`
    : `must be ${keyword === 'exclusiveMaximum' ? 'less than' : 'at most'} ${limit}`;
}

function hint(error) {
  const params = error.params || {};
  const schema = error.parentSchema;

  switch (error.keyword) {
    case 'required':
    case 'dependencies':
      return 'is required';
    case 'minLength':
      return params.limit === 1
        ? 'cannot be empty'
        : `must be at least ${params.limit} characters`;
    case 'maxLength':
      return `must be at most ${params.limit} characters`;
    case 'pattern':
      return PATTERN_HINTS[params.pattern] || 'is not in the expected format';
    case 'format':
      return FORMAT_HINTS[params.format] || `must be a valid ${params.format}`;
    case 'minimum':
    case 'maximum':
    case 'exclusiveMinimum':
    case 'exclusiveMaximum':
      return rangeHint(schema, error.keyword, params.limit);
    case 'type': {
      const types = [params.type].flat().join(',').split(',');
      const wanted = types.find((type) => type !== 'null') || types[0];

      return TYPE_HINTS[wanted] || `must be ${wanted}`;
    }
    case 'enum':
    case 'const':
      return 'must be one of the listed options';
    case 'minItems':
      return `must have at least ${params.limit} ${params.limit === 1 ? 'item' : 'items'}`;
    case 'maxItems':
      return `can have at most ${params.limit} ${params.limit === 1 ? 'item' : 'items'}`;
    case 'uniqueItems':
      return 'cannot repeat an item';
    case 'additionalProperties':
      return `has a field this editor does not know: "${params.additionalProperty}"`;
    case 'oneOf':
    case 'anyOf':
      return 'does not match any of its kinds';
    default:
      return error.message || 'is not valid';
  }
}

/**
 * A plain-language message for one ajv error, starting with the field label:
 * "Hostname cannot contain spaces", "VLAN alias must be between 1 and 4094".
 *
 * @param {object} error ajv error (with `parentSchema`, see createFormValidator)
 * @param {object} schema the form's root schema, for field titles
 * @returns {string}
 */
export function errorMessage(error, schema) {
  return `${fieldLabel(schema, errorPath(error)).label} ${hint(error)}`;
}

// How far data is from a oneOf branch, as [mismatched choices, errors]. A
// branch whose fixed choices (an interface's type and proto) the data does
// not take is further than any branch it merely leaves incomplete.
function branchDistance(errors) {
  return [
    errors.filter((error) => ['enum', 'const'].includes(error.keyword)).length,
    errors.length,
  ];
}

function closer(a, b) {
  return a[0] - b[0] || a[1] - b[1];
}

/**
 * The oneOf branch that data comes closest to matching, for data no branch
 * accepts yet (a new interface with no VLAN). The Inspector shows that
 * branch's fields so the missing values can be filled in.
 *
 * @param {object} ajv
 * @param {object[]} schemas the oneOf branches
 * @param {unknown} data
 * @returns {number|undefined}
 */
export function closestBranch(ajv, schemas, data) {
  if (data === undefined || !ajv) {
    return undefined;
  }

  let best;

  schemas.forEach((schema, index) => {
    try {
      const validate = ajv.compile(schema);
      const distance = validate(data)
        ? [0, 0]
        : branchDistance(validate.errors || []);

      if (!best || closer(distance, best.distance) < 0) {
        best = { index, distance };
      }
    } catch {
      // A branch that does not compile cannot be the closest.
    }
  });

  return best?.index;
}

// ajv reports every branch of a failed oneOf. Keep only the closest branch,
// so a half-filled static interface lists what the static variant still
// needs instead of every variant's requirements.
function closestBranches(errors) {
  let kept = errors;

  for (const combinator of errors.filter((error) =>
    ['oneOf', 'anyOf'].includes(error.keyword),
  )) {
    // More than one branch matched: that error is the one to show.
    if (combinator.params?.passingSchemas) {
      continue;
    }

    const prefix = `${combinator.schemaPath}/`;
    const branches = new Map();

    for (const error of kept) {
      if (error.schemaPath?.startsWith(prefix)) {
        const branch = error.schemaPath.slice(prefix.length).split('/')[0];

        branches.set(branch, [...(branches.get(branch) || []), error]);
      }
    }

    if (branches.size === 0) {
      continue;
    }

    const [best] = [...branches.values()].sort((a, b) =>
      closer(branchDistance(a), branchDistance(b)),
    );

    kept = kept.filter(
      (error) =>
        error !== combinator &&
        (!error.schemaPath?.startsWith(prefix) || best.includes(error)),
    );
  }

  return kept;
}

/**
 * Groups ajv errors by field, one entry per field, for the Inspector's error
 * summary.
 *
 * @param {object[]} errors ajv errors from JSON Forms
 * @param {object} schema the form's root schema, for field titles
 * @returns {{path: string, label: string, message: string}[]} `message`
 *   leads with the list item the field is in, if any ("Drive 2: Image is
 *   required")
 */
export function fieldErrors(errors, schema) {
  const fields = new Map();

  for (const error of closestBranches(errors || [])) {
    const path = errorPath(error);

    if (fields.has(path)) {
      continue;
    }

    const { label, context } = fieldLabel(schema, path);
    const text = `${label} ${hint(error)}`;

    fields.set(path, {
      path,
      label,
      message: context ? `${context}: ${text}` : text,
    });
  }

  return [...fields.values()];
}

const COMBINATORS = ['oneOf', 'anyOf'];

function valueAt(data, keys) {
  return keys.reduce(
    (value, key) =>
      value !== null && typeof value === 'object' ? value[key] : undefined,
    data,
  );
}

// What makes an error the same one again: where in the schema it comes
// from, what it says and the value it is about, but not where in the data,
// so an error a list item had still counts once the item moves up. A oneOf
// is about its whole item, which any edit of it changes.
function signature(error, data) {
  return JSON.stringify([
    error.schemaPath,
    error.keyword,
    error.params,
    COMBINATORS.includes(error.keyword)
      ? null
      : (valueAt(data, errorKeys(error)) ?? null),
  ]);
}

// The signatures of the errors of an element's data as the form loaded it,
// kept while it is the form's base.
const baseErrors = new WeakMap();

function errorsBefore(validate, base) {
  const known =
    base && typeof base === 'object' ? baseErrors.get(base) : undefined;

  if (known?.validate === validate) {
    return known.signatures;
  }

  const signatures = new Set(
    (validate(base) ? [] : validate.errors || []).map((error) =>
      signature(error, base),
    ),
  );

  if (base && typeof base === 'object') {
    baseErrors.set(base, { validate, signatures });
  }

  return signatures;
}

/**
 * The errors of an Inspector working copy that keep it from being applied:
 * those on the fields it changed, those of items it added, and any it
 * brought about elsewhere (a static interface's missing address once its
 * proto is static). An error
 * the element had already, on a field left as it was, does not count: an
 * element imported from a phenix experiment carries values phenix accepts
 * and the form's schema does not (advanced null, mac "", gateway ""), and
 * they would keep every edit of it from being applied.
 *
 * @param {object} ajv createFormValidator's
 * @param {object} schema the form's schema
 * @param {object} data the working copy
 * @param {object} base the element's data as the form loaded it
 * @returns {{errors: object[], fields: {path: string, label: string, message: string}[]}}
 *   `errors`: the ajv errors that count, for JSON Forms to show on their
 *   fields; `fields`: the Inspector's error summary of them, as
 *   fieldErrors gives it
 */
export function workingCopyErrors(ajv, schema, data, base) {
  const validate = ajv.compile(schema);
  const all = validate(data) ? [] : [...(validate.errors || [])];

  if (all.length === 0) {
    return { errors: [], fields: [] };
  }

  const before = errorsBefore(validate, base);
  const counts = new Set(
    all.filter((error) => {
      if (!before.has(signature(error, data))) {
        return true;
      }

      if (COMBINATORS.includes(error.keyword)) {
        return false;
      }

      // The loaded data has nothing where the error is, such as a list item
      // the edit added: the error is the edit's, like another item's before.
      const at = errorKeys({ instancePath: error.instancePath });

      if (valueAt(base, at) === undefined) {
        return true;
      }

      const keys = errorKeys(error);

      return (
        JSON.stringify(valueAt(data, keys) ?? null) !==
        JSON.stringify(valueAt(base, keys) ?? null)
      );
    }),
  );

  // JSON Forms shows a field only the errors of the oneOf alternative it
  // renders while the oneOf's own error is listed (see errorsAt in
  // @jsonforms/core), so that stays for the errors under it that count.
  const errors = all.filter(
    (error) =>
      counts.has(error) ||
      (COMBINATORS.includes(error.keyword) &&
        [...counts].some(
          (other) =>
            other.instancePath.startsWith(error.instancePath) &&
            other.schemaPath.startsWith(`${error.schemaPath}/`),
        )),
  );

  return {
    errors,
    fields: fieldErrors(
      closestBranches(all).filter((error) => counts.has(error)),
      schema,
    ),
  };
}
