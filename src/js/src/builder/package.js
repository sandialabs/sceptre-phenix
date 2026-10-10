// Builder packages: one file that holds a diagram, the Scenario and Topology
// configs and the custom icons it names that the user chose to put in, and
// the list of what the diagram needs on a phenix server (Package in
// phenix/types/builder package.go).
//
// The server makes a package of the open diagram (POST /builder/package)
// and says, for a package uploaded to it, which of the diagram's needs it
// has (POST /builder/package/resolve). This module decodes a package as
// strictly as the server does, groups what the server says, and creates the
// configs the user ticks, one at a time. A package never holds the content
// of a file, a script or an app: those are only named.

import { isBuilderAnnotation } from './configs.js';
import { parseDocument } from './decode.js';
import { exportFileName } from './exporters.js';
import { hasControlCharacters, isBlank, utf8Length } from './text.js';
import { MAX_SCENARIOS, MAX_SCENARIO_NAME_BYTES } from './validate.js';

export const PACKAGE_SCHEMA_URI =
  'https://phenix.sandia.gov/schemas/builder/package/v1';

// The bounds on a package (package.go): the most included Topology configs
// it carries, the most entries one list of its requirements holds, and the
// longest entry. It carries at most MAX_SCENARIOS Scenario configs, the
// most a document names.
export const MAX_PACKAGE_TOPOLOGIES = 100;
export const MAX_PACKAGE_REQUIREMENTS = 1000;
export const MAX_REQUIREMENT_BYTES = 4096;

// The sections a package may carry besides its document, in the order the
// Download dialog offers them, with the label of each tick.
export const PACKAGE_SECTIONS = [
  { id: 'scenarios', label: 'Scenario configs' },
  { id: 'topologies', label: 'Included topologies' },
  { id: 'icons', label: 'Custom icons' },
  { id: 'images', label: 'Disk-image requirements' },
];

// The keys of a package and of its parts: the properties of the package
// schema (GET /schemas/builder/package/v1).
export const PACKAGE_KEYS = new Set([
  '$schema',
  'document',
  'scenarios',
  'topologies',
  'requirements',
]);
export const PACKAGE_CONFIG_KEYS = new Set([
  'apiVersion',
  'kind',
  'metadata',
  'spec',
]);
export const PACKAGE_METADATA_KEYS = new Set(['name', 'annotations']);
export const REQUIREMENT_KEYS = [
  'scenarios',
  'topologies',
  'templates',
  'icons',
  'images',
  'apps',
  'files',
];
export const PACKAGE_IMAGE_KEYS = new Set(['name', 'usedBy']);

// The kind of the configs of each list a package may carry.
const CONFIG_KINDS = { scenarios: 'Scenario', topologies: 'Topology' };

// A phenix config name (configNamePattern in schema.go).
const CONFIG_NAME = /^[A-Za-z0-9_@.-]+$/;

// The apiVersion of a phenix config.
const API_VERSION = /^phenix\.sandia\.gov\/v[0-9]+$/;

/**
 * Why a file is not a package this Builder can open.
 */
export class PackageError extends Error {
  constructor(message) {
    super(message);
    this.name = 'PackageError';
  }
}

function isObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/**
 * Whether a parsed file is a package: an object that names the package
 * schema. Anything else is read as a Builder document.
 *
 * @param {*} value
 * @returns {boolean}
 */
export function isPackageValue(value) {
  return isObject(value) && value.$schema === PACKAGE_SCHEMA_URI;
}

function rejectUnknown(value, allowed, path) {
  if (!isObject(value)) {
    throw new PackageError(`${path}: expected an object`);
  }

  Object.keys(value).forEach((key) => {
    if (!allowed.has(key)) {
      throw new PackageError(`${path}: unknown field "${key}"`);
    }
  });
}

/**
 * A value as an error message names it, as the server's truncate does: at
 * most 64 UTF-8 bytes of whole characters, then "...".
 *
 * @param {string} value
 * @returns {string}
 */
function truncate(value) {
  const limit = 64;

  if (utf8Length(value) <= limit) {
    return value;
  }

  let cut = '';

  for (const ch of value) {
    if (utf8Length(cut + ch) > limit) {
      break;
    }

    cut += ch;
  }

  return `${cut}...`;
}

// Why a value is no entry of the requirements, in the words of the
// server's requirementProblem, or ''.
function requirementProblem(value) {
  if (typeof value !== 'string') {
    return 'must be a name or a path';
  }

  if (isBlank(value)) {
    return 'must not be blank';
  }

  if (utf8Length(value) > MAX_REQUIREMENT_BYTES) {
    return `must be at most ${MAX_REQUIREMENT_BYTES} bytes`;
  }

  if (hasControlCharacters(value)) {
    return 'must not contain control characters';
  }

  return '';
}

function checkEntry(value, path) {
  const problem = requirementProblem(value);

  if (problem) {
    throw new PackageError(`${path}: ${problem}`);
  }
}

// A list of the requirements: present, at most MAX_PACKAGE_REQUIREMENTS
// entries.
function checkList(values, path) {
  if (!Array.isArray(values)) {
    throw new PackageError(
      `${path}: the list is required, empty when there is nothing in it`,
    );
  }

  if (values.length > MAX_PACKAGE_REQUIREMENTS) {
    throw new PackageError(
      `${path}: the list holds at most ${MAX_PACKAGE_REQUIREMENTS} entries, not ${values.length}`,
    );
  }
}

function checkEntries(values, path) {
  checkList(values, path);
  values.forEach((value, index) => checkEntry(value, `${path}[${index}]`));
}

function checkRequirements(requirements) {
  rejectUnknown(requirements, new Set(REQUIREMENT_KEYS), 'requirements');

  REQUIREMENT_KEYS.filter((key) => key !== 'images').forEach((key) => {
    checkEntries(requirements[key], `requirements.${key}`);
  });

  checkList(requirements.images, 'requirements.images');

  requirements.images.forEach((image, index) => {
    const path = `requirements.images[${index}]`;

    rejectUnknown(image, PACKAGE_IMAGE_KEYS, path);
    checkEntry(image.name, `${path}.name`);
    checkEntries(image.usedBy, `${path}.usedBy`);
  });
}

/**
 * @param {string} name
 * @returns {boolean} whether it is a name phenix gives a config, and one a
 *   package may carry a config under: not empty, and at most
 *   MAX_SCENARIO_NAME_BYTES (IsConfigName in validate.go)
 */
function isConfigName(name) {
  return CONFIG_NAME.test(name) && utf8Length(name) <= MAX_SCENARIO_NAME_BYTES;
}

function checkConfig(config, name, key) {
  const path = `${key}.${name}`;

  rejectUnknown(config, PACKAGE_CONFIG_KEYS, path);

  if (!isConfigName(name)) {
    const shown = truncate(name);

    throw new PackageError(`${path}: "${shown}" is not a config name`);
  }

  if (config.kind !== CONFIG_KINDS[key]) {
    throw new PackageError(`${path}.kind: must be "${CONFIG_KINDS[key]}"`);
  }

  if (!API_VERSION.test(String(config.apiVersion ?? ''))) {
    throw new PackageError(
      `${path}.apiVersion: must be phenix.sandia.gov/v<number>`,
    );
  }

  rejectUnknown(config.metadata, PACKAGE_METADATA_KEYS, `${path}.metadata`);

  if (config.metadata.name !== name) {
    throw new PackageError(
      `${path}.metadata.name: must be "${name}", the name the package keeps it under`,
    );
  }

  const { annotations } = config.metadata;

  if (
    annotations !== undefined &&
    annotations !== null &&
    (!isObject(annotations) ||
      Object.values(annotations).some((value) => typeof value !== 'string'))
  ) {
    throw new PackageError(
      `${path}.metadata.annotations: must map names to text`,
    );
  }

  // A Builder annotation names a record of the server that wrote it, so a
  // config made from a package must never carry one.
  const builderKey = Object.keys(annotations || {})
    .sort()
    .find(isBuilderAnnotation);

  if (builderKey !== undefined) {
    throw new PackageError(
      `${path}.metadata.annotations: "${truncate(builderKey)}" is a Builder annotation, which a package never carries`,
    );
  }

  if (!isObject(config.spec)) {
    throw new PackageError(`${path}.spec: a config needs a spec`);
  }
}

// The most configs of each list a package carries, by its key.
const CONFIG_LIMITS = {
  scenarios: MAX_SCENARIOS,
  topologies: MAX_PACKAGE_TOPOLOGIES,
};

/**
 * Strictly decodes a package from a parsed file, as the server decodes one
 * (DecodePackage and Package.Validate in package.go): unknown fields are
 * refused, the document is decoded and validated as an uploaded document
 * is, a config must be of the kind of its list, named by its key (a config
 * name of at most MAX_SCENARIO_NAME_BYTES) and by the requirements, and
 * free of Builder annotations, at most MAX_SCENARIOS Scenario and
 * MAX_PACKAGE_TOPOLOGIES Topology configs are carried, and every list of
 * the requirements must be present, with at most MAX_PACKAGE_REQUIREMENTS
 * entries, none blank, longer than MAX_REQUIREMENT_BYTES or with control
 * characters. The messages are the server's words.
 *
 * @param {object} value
 * @returns {object} the package (a copy): $schema, document, scenarios and
 *   topologies (each an object, empty when the package carries none) and
 *   requirements
 * @throws {PackageError}
 */
export function decodePackage(value) {
  if (!isPackageValue(value)) {
    throw new PackageError(
      `A Builder package names the schema "${PACKAGE_SCHEMA_URI}".`,
    );
  }

  rejectUnknown(value, PACKAGE_KEYS, 'package');

  let document;

  try {
    document = parseDocument(value.document);
  } catch (error) {
    throw new PackageError(`document: ${error.message}`);
  }

  checkRequirements(value.requirements);

  Object.keys(CONFIG_KINDS).forEach((key) => {
    const configs = value[key];

    if (configs === undefined || configs === null) {
      return;
    }

    if (!isObject(configs)) {
      throw new PackageError(`${key}: expected an object`);
    }

    const carried = Object.keys(configs).length;

    if (carried > CONFIG_LIMITS[key]) {
      throw new PackageError(
        `${key}: a package carries at most ${CONFIG_LIMITS[key]} ${CONFIG_KINDS[key]} configs, not ${carried}`,
      );
    }

    Object.entries(configs).forEach(([name, config]) => {
      checkConfig(config, name, key);

      if (!value.requirements[key].includes(name)) {
        throw new PackageError(
          `${key}.${name}: the package carries it, but its requirements do not name it`,
        );
      }
    });
  });

  const copy = JSON.parse(JSON.stringify(value));

  return {
    ...copy,
    document,
    scenarios: copy.scenarios || {},
    topologies: copy.topologies || {},
  };
}

// The kinds of what a package's diagram needs, in the order the server
// lists them, with the heading of each group.
export const DEPENDENCY_GROUPS = [
  { kind: 'scenario', label: 'Scenario configs' },
  { kind: 'topology', label: 'Included topologies' },
  { kind: 'template', label: 'Templates' },
  { kind: 'icon', label: 'Custom icons' },
  { kind: 'image', label: 'Disk images' },
  { kind: 'app', label: 'Apps' },
  { kind: 'file', label: 'Files' },
];

// What each status says, in words: the list never says it by color alone.
export const DEPENDENCY_STATUS_TEXT = {
  present: 'Present',
  missing: 'Missing',
  different: 'Different',
  unknown: 'Not checked',
};

const STATUSES = Object.keys(DEPENDENCY_STATUS_TEXT);

/**
 * What the server says a package's diagram needs, as the dialog shows it:
 * each entry's kind, name, status, whether the package carries it and why
 * the status is what it is. An entry of an unknown kind or status is left
 * out.
 *
 * @param {object} response the server's answer
 * @returns {{kind: string, name: string, status: string, packaged: boolean,
 *   detail: string}[]}
 */
export function readDependencies(response) {
  const list = Array.isArray(response?.dependencies)
    ? response.dependencies
    : [];
  const kinds = DEPENDENCY_GROUPS.map((group) => group.kind);

  return list
    .filter(
      (entry) =>
        kinds.includes(entry?.kind) &&
        STATUSES.includes(entry.status) &&
        typeof entry.name === 'string',
    )
    .map((entry) => ({
      kind: entry.kind,
      name: entry.name,
      status: entry.status,
      packaged: entry.packaged === true,
      detail: typeof entry.detail === 'string' ? entry.detail : '',
    }));
}

/**
 * The dependencies grouped by kind, in the order of DEPENDENCY_GROUPS, each
 * group with its heading; a kind with no entry has no group.
 *
 * @param {object[]} dependencies see readDependencies
 * @returns {{kind: string, label: string, items: object[]}[]}
 */
export function groupDependencies(dependencies) {
  return DEPENDENCY_GROUPS.map(({ kind, label }) => ({
    kind,
    label,
    items: dependencies.filter((dependency) => dependency.kind === kind),
  })).filter((group) => group.items.length > 0);
}

/**
 * Whether the user may have a dependency created on this server: a Scenario
 * or Topology config the package carries and the server does not have. A
 * config the server has, the same or different, is never replaced, and
 * disk images, apps and files are only listed.
 *
 * @param {object} dependency
 * @returns {boolean}
 */
export function canCreate(dependency) {
  return (
    dependency?.packaged === true &&
    dependency.status === 'missing' &&
    (dependency.kind === 'scenario' || dependency.kind === 'topology')
  );
}

/**
 * @param {object} dependency
 * @returns {string} what tells the dependency apart from the others
 */
export function dependencyKey(dependency) {
  return `${dependency.kind}/${dependency.name}`;
}

/**
 * The dependencies the user ticked for creation, by their keys (see
 * dependencyKey). One canCreate does not allow is never among them.
 *
 * @param {object[]} dependencies
 * @param {string[]} keys the keys of the ticked checkboxes
 * @returns {object[]}
 */
export function tickedDependencies(dependencies, keys) {
  return dependencies.filter(
    (dependency) =>
      canCreate(dependency) && keys.includes(dependencyKey(dependency)),
  );
}

/**
 * The config of a package a dependency names, as POST /configs takes it.
 *
 * @param {object} pkg a decoded package
 * @param {object} dependency
 * @returns {object|null}
 */
export function packagedConfig(pkg, dependency) {
  const list = {
    scenario: pkg?.scenarios,
    topology: pkg?.topologies,
  }[dependency?.kind];

  return isObject(list) && Object.hasOwn(list, dependency.name)
    ? list[dependency.name]
    : null;
}

/**
 * Creates the configs of a package the user ticked, one at a time, through
 * create (POST /configs, which checks the user's permission and the config).
 * A failure is kept with its error and the rest are still created. Only a
 * dependency canCreate allows is created.
 *
 * @param {object} pkg a decoded package
 * @param {object[]} ticked the dependencies the user ticked
 * @param {(config: object) => Promise<void>} create
 * @returns {Promise<{created: object[], failed: {dependency: object,
 *   error: *}[]}>}
 */
export async function createTickedConfigs(pkg, ticked, create) {
  const created = [];
  const failed = [];

  for (const dependency of ticked.filter(canCreate)) {
    const config = packagedConfig(pkg, dependency);

    if (config) {
      try {
        await create(config);
        created.push(dependency);
      } catch (error) {
        failed.push({ dependency, error });
      }
    }
  }

  return { created, failed };
}

/**
 * The name of a config kind as the dialog says it.
 *
 * @param {string} kind scenario or topology
 * @returns {string}
 */
export function configNoun(kind) {
  return kind === 'topology' ? 'Topology config' : 'Scenario config';
}

/**
 * The name a downloaded package is saved under: the diagram's file name,
 * then .package.json or .package.yaml.
 *
 * @param {object} doc
 * @param {'json'|'yaml'} format
 * @returns {string}
 */
export function packageFileName(doc, format) {
  return exportFileName(doc, `package.${format === 'yaml' ? 'yaml' : 'json'}`);
}
