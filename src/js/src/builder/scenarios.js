// The Scenarios dialog's list, and the Scenario configs that it shows,
// edits and creates.
//
// The list has every scenario the server lists for the user and each one
// the diagram lists that the server does not, sorted by name. A checked
// row is a scenario the diagram lists. Names compare as the server compares
// the names of a document: ignoring case.
//
// A scenario shows as YAML. The text the user edits is read as the server
// reads a config, and is checked before it is sent: it must be a
// phenix.sandia.gov/v2 Scenario, and an edit must keep its name, because a
// PUT with another name renames the config.

import YAML from 'js-yaml';

import { classifyError, errorMessage, sentence, serverReason } from './api.js';
import { AliasError, parseText } from './decode.js';
import { configName, defaultTopologyName } from './publish.js';
import {
  MAX_SCENARIO_NAME_BYTES,
  SCENARIO_API_VERSION,
  scenarioNameProblem,
} from './validate.js';

// The list shows a filter field when it has more rows than this.
export const SCENARIO_FILTER_MIN = 8;

/**
 * @param {string} name
 * @returns {string} the name as names compare: in lower case
 */
export function scenarioKey(name) {
  return String(name).toLowerCase();
}

// Names in the order a person reads a sorted list: letters in any case
// together, and "s2" before "s10". Names that differ only in case keep one
// order.
function byName(a, b) {
  return (
    a.name.localeCompare(b.name, 'en', {
      sensitivity: 'base',
      numeric: true,
    }) || (a.name < b.name ? -1 : Number(a.name > b.name))
  );
}

/**
 * The rows of the Scenarios dialog's list, sorted by name. Each scenario
 * the server lists has a row. A scenario that the diagram lists, or listed
 * when the dialog opened, and that the server does not list has a row too,
 * marked missing. Thus a row does not go away when its box is cleared. A
 * row takes the server's spelling of the name.
 *
 * @param {string[]} listed the names the list being edited has
 * @param {string[]} stored the names the server lists
 * @param {string[]} [known] other names to show, such as those the diagram
 *   listed when the dialog opened
 * @returns {{name: string, checked: boolean, missing: boolean}[]}
 */
export function scenarioRows(listed = [], stored = [], known = []) {
  const rows = new Map();

  stored.forEach((name) => {
    if (!rows.has(scenarioKey(name))) {
      rows.set(scenarioKey(name), { name, checked: false, missing: false });
    }
  });

  [...listed, ...known].forEach((name) => {
    if (!rows.has(scenarioKey(name))) {
      rows.set(scenarioKey(name), { name, checked: false, missing: true });
    }
  });

  const checked = new Set(listed.map(scenarioKey));

  rows.forEach((row, key) => {
    row.checked = checked.has(key);
  });

  return [...rows.values()].sort(byName);
}

/**
 * The rows whose name has the filter text, in any case. All rows for no
 * filter.
 *
 * @param {{name: string}[]} rows
 * @param {string} filter
 * @returns {{name: string}[]}
 */
export function filterScenarioRows(rows, filter) {
  const text = scenarioKey(String(filter || '').trim());

  return text
    ? rows.filter((row) => scenarioKey(row.name).includes(text))
    : rows;
}

/**
 * The list after a row's box is set or cleared. A name set is added at the
 * end, so the list keeps the order in which the user chose its scenarios
 * (Publish uses the first one for an experiment). A name cleared is removed
 * in any letter case.
 *
 * @param {string[]} names the list
 * @param {string} name
 * @param {boolean} checked
 * @returns {string[]} a new list
 */
export function toggleScenario(names, name, checked) {
  const key = scenarioKey(name);
  const rest = names.filter((item) => scenarioKey(item) !== key);

  if (!checked) {
    return rest;
  }

  return rest.length === names.length ? [...names, name] : [...names];
}

/**
 * The name of a new scenario of a diagram: the diagram name made into a
 * config name, then "-scenario". When a name in `taken` has it, in any
 * case, "-2", "-3" and so on are added. The name is never longer than a
 * document allows.
 *
 * @param {string} diagramName
 * @param {string[]} taken
 * @returns {string} for example "plant-scenario" or "plant-scenario-2"
 */
export function newScenarioName(diagramName, taken = []) {
  const base = configName(diagramName);
  const stem = base ? `${base}-scenario` : 'scenario';
  const used = new Set(taken.map(scenarioKey));

  // A config name has only ASCII characters, so its length is its bytes.
  for (let number = 1; ; number += 1) {
    const suffix = number === 1 ? '' : `-${number}`;
    const name =
      stem.slice(0, MAX_SCENARIO_NAME_BYTES - suffix.length) + suffix;

    if (!used.has(scenarioKey(name))) {
      return name;
    }
  }
}

/**
 * The Scenario config that "Create new scenario" starts from: a unique name
 * made from the diagram name, the topology annotation with the topology
 * name that the Publish dialog offers, and no apps.
 *
 * @param {object} doc the diagram
 * @param {string[]} taken the scenario names already in use
 * @returns {object} apiVersion, kind, metadata and spec
 */
export function newScenarioConfig(doc, taken = []) {
  const topology = defaultTopologyName(doc);
  const metadata = { name: newScenarioName(doc?.metadata?.name, taken) };

  if (topology) {
    metadata.annotations = { topology };
  }

  return {
    apiVersion: SCENARIO_API_VERSION,
    kind: 'Scenario',
    metadata,
    spec: { apps: [] },
  };
}

function isObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/**
 * A Scenario config as YAML text: apiVersion, kind, metadata and spec, in
 * that order. Text to edit leaves out the times the server sets (created,
 * updated), since the server ignores them.
 *
 * @param {object} config
 * @param {{editable?: boolean}} [options]
 * @returns {string}
 */
export function scenarioText(config, { editable = false } = {}) {
  const metadata = isObject(config?.metadata) ? config.metadata : {};
  const shown = editable
    ? {
        name: metadata.name,
        ...(isObject(metadata.annotations) &&
        Object.keys(metadata.annotations).length
          ? { annotations: metadata.annotations }
          : {}),
      }
    : metadata;

  return YAML.dump(
    {
      apiVersion: config?.apiVersion,
      kind: config?.kind,
      metadata: shown,
      spec: config?.spec ?? {},
    },
    { noRefs: true, sortKeys: false, lineWidth: -1 },
  );
}

/**
 * Reads the text of the scenario editor as the Scenario config to store, or
 * says why it cannot be stored. The text is JSON or YAML, as for an upload.
 * With `name`, the config must keep that name.
 *
 * @param {string} text
 * @param {{name?: string}} [options] name: the name of the scenario edited
 * @returns {{config: object} | {error: string, parse?: boolean}} parse: the
 *   error is js-yaml's, with its position
 */
export function readScenarioText(text, { name = '' } = {}) {
  if (!String(text || '').trim()) {
    return { error: 'Enter the scenario config.' };
  }

  let value;

  try {
    value = parseText(text);
  } catch (error) {
    if (error instanceof AliasError) {
      return {
        error:
          'YAML aliases (*name) are not supported. Replace each alias with the value it repeats.',
      };
    }

    return { error: error.message, parse: true };
  }

  if (!isObject(value)) {
    return { error: 'The scenario config must be a YAML object.' };
  }

  if (value.kind !== 'Scenario') {
    return {
      error: `The kind must be Scenario, not ${value.kind || 'empty'}.`,
    };
  }

  if (value.apiVersion !== SCENARIO_API_VERSION) {
    return {
      error: `The apiVersion must be ${SCENARIO_API_VERSION}. The Builder stores only ${SCENARIO_API_VERSION} scenarios.`,
    };
  }

  if (!isObject(value.metadata)) {
    return { error: 'The config must have a metadata object with a name.' };
  }

  if (!isObject(value.spec)) {
    return { error: 'The config must have a spec object.' };
  }

  const named = value.metadata.name;
  const problem = scenarioNameProblem(named);

  if (problem) {
    return { error: sentence(problem) };
  }

  if (name && named !== name) {
    return {
      error: `The name must stay ${name}. To store the scenario with a new name, select Create new scenario.`,
    };
  }

  return {
    config: {
      apiVersion: value.apiVersion,
      kind: 'Scenario',
      metadata: value.metadata,
      spec: value.spec,
    },
  };
}

/**
 * Why a request about a scenario failed, in words the user can act on.
 *
 * @param {object} error axios-like error
 * @param {string} action what could not be done: "read scenario nera"
 * @returns {string}
 */
export function scenarioErrorText(error, action) {
  const kind = classifyError(error);

  switch (kind) {
    case 'forbidden':
      return `You do not have permission to ${action}.`;
    case 'missing':
      return `Could not ${action}. The server does not have this scenario.`;
    case 'unauthenticated':
    case 'offline':
    case 'too-large':
      return `Could not ${action}. ${errorMessage(kind, error)}`;
    default:
      return `Could not ${action}. ${serverReason(error) || 'The server rejected the request.'}`;
  }
}

/**
 * The problems that the server found when it checked a config against its
 * schema, one for each line, or none.
 *
 * @param {object} error axios-like error
 * @returns {string[]}
 */
export function validationLines(error) {
  const text = error?.response?.data?.metadata?.validation;

  return typeof text === 'string'
    ? text.split('\n').filter((line) => line.trim())
    : [];
}
