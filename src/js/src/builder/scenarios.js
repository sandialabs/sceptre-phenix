// The Scenarios dialog's list, and the Scenario configs it shows, edits and
// creates. Names compare ignoring case, as the server compares them. An edit
// must keep the name, because a PUT with another name renames the config.

import YAML from 'js-yaml';

import { classifyError, errorMessage, sentence, serverReason } from './api.js';
import { AliasError, parseText } from './decode.js';
import { configName, defaultTopologyName } from './publish.js';
import {
  MAX_SCENARIO_NAME_BYTES,
  SCENARIO_API_VERSION,
  scenarioNameProblem,
} from './validate.js';
import { isPlainObject } from './values.js';

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

// The Scenarios dialog's rows, by name with the server's spelling. A listed or
// known name that the server does not list is marked missing, so a row does not
// go away when its box is cleared.
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

/** The rows whose name holds the filter text in any case, or all rows. */
export function filterScenarioRows(rows, filter) {
  const text = scenarioKey(String(filter || '').trim());

  return text
    ? rows.filter((row) => scenarioKey(row.name).includes(text))
    : rows;
}

// The list after a row's box is set or cleared. A set name goes at the end, so
// the list keeps the user's order (Publish uses the first for an experiment).
export function toggleScenario(names, name, checked) {
  const key = scenarioKey(name);
  const rest = names.filter((item) => scenarioKey(item) !== key);

  if (!checked) {
    return rest;
  }

  return rest.length === names.length ? [...names, name] : [...names];
}

// The name of a new scenario: the diagram's config name, then "-scenario",
// then "-2", "-3" while `taken` has it in any case. It fits a document's limit.
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

// The Scenario config that Create new scenario starts from: a unique name,
// the topology annotation with the Publish dialog's topology name, and no apps.
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

// A Scenario config as YAML in apiVersion, kind, metadata, spec order. Text to
// edit leaves out the times the server sets, because the server ignores them.
export function scenarioText(config, { editable = false } = {}) {
  const metadata = isPlainObject(config?.metadata) ? config.metadata : {};
  const shown = editable
    ? {
        name: metadata.name,
        ...(isPlainObject(metadata.annotations) &&
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

// Reads the scenario editor's JSON or YAML text as the config to store, or an
// error (`parse` for a js-yaml error). With `name`, the config must keep it.
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
          'The Builder does not support YAML aliases (*name). Replace each alias with the value it repeats.',
      };
    }

    return { error: error.message, parse: true };
  }

  if (!isPlainObject(value)) {
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

  if (!isPlainObject(value.metadata)) {
    return { error: 'The config must have a metadata object with a name.' };
  }

  if (!isPlainObject(value.spec)) {
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

/** Why a request about a scenario failed, in words the user can act on. */
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

// The problems the server found when it checked a config against its schema,
// one per line, or none.
export function validationLines(error) {
  const text = error?.response?.data?.metadata?.validation;

  return typeof text === 'string'
    ? text.split('\n').filter((line) => line.trim())
    : [];
}
