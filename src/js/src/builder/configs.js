// Helpers shared with the Configs view, which identify topologies made in
// the Builder. A topology that the Builder published carries a `builder-doc`
// annotation. A topology that still has a diagram of the removed legacy
// Builder carries `builder-xml`. The Builder converts such a diagram when the
// topology is imported or uploaded as a file. Publishing the draft to the
// topology replaces `builder-xml` with `builder-doc`.
//
// The Configs page loads this file, so it imports nothing from the editor.

import YAML from 'js-yaml';

export const LEGACY_ANNOTATION = 'builder-xml';
export const DOC_ANNOTATION = 'builder-doc';

// Every annotation the Builders keep for themselves starts with this: the
// two above, and builder-experiment on an experiment Builder published
// (IsBuilderAnnotation in types/builder/generate.go).
const BUILDER_PREFIX = 'builder-';

/**
 * @param {string} key a config annotation key
 * @returns {boolean} whether it is one of the Builders' own, which a
 *   document never shows
 */
export function isBuilderAnnotation(key) {
  return String(key).startsWith(BUILDER_PREFIX);
}

/**
 * @param {object} config phenix config
 * @returns {'builder-doc'|'builder-xml'|null}
 */
export function builderAnnotation(config) {
  if (!config || config.kind !== 'Topology') {
    return null;
  }

  const annotations = config.metadata?.annotations;

  if (!annotations || typeof annotations !== 'object') {
    return null;
  }

  if (DOC_ANNOTATION in annotations) {
    return DOC_ANNOTATION;
  }

  if (LEGACY_ANNOTATION in annotations) {
    return LEGACY_ANNOTATION;
  }

  return null;
}

/**
 * Tag text for the configs table.
 *
 * @param {object} config
 * @returns {string} '' when the config is not builder authored
 */
export function builderTagLabel(config) {
  const annotation = builderAnnotation(config);

  if (annotation === DOC_ANNOTATION) {
    return 'builder';
  }

  if (annotation === LEGACY_ANNOTATION) {
    return 'builder legacy';
  }

  return '';
}

/**
 * What the Builder does with a config from the Configs page. It opens the
 * diagram of a topology that has one, and imports any other topology. That
 * import converts a legacy Builder diagram.
 *
 * @param {object} config phenix config
 * @returns {'open'|'import'|''} '' for a config that is not a Topology
 */
export function builderAction(config) {
  if (!config || config.kind !== 'Topology') {
    return '';
  }

  return builderAnnotation(config) === DOC_ANNOTATION ? 'open' : 'import';
}

/**
 * Where every Builder control of the Configs page leads. The Builder
 * decides what opens there (see openLinked in views/Builder.vue).
 *
 * @param {object} config a Topology config
 * @returns {{name: string, query: {topology: string}}} a route location
 */
export function builderLink(config) {
  return { name: 'builder', query: { topology: config.metadata.name } };
}

// An includeTopologies entry the server counts (validIncludeName in
// types/builder/generate.go).
function validInclude(entry) {
  return (
    typeof entry === 'string' && entry.trim() !== '' && !/[ \t\n]/.test(entry)
  );
}

/**
 * What the Import dialog needs to know about a config file before the server
 * reads it: its kind, its name and how many topologies it includes. This
 * function only parses the text, as JSON or else as YAML with plain scalars.
 * The server still parses and checks the file itself.
 *
 * @param {string} text the file's text
 * @returns {{kind: 'topology'|'experiment', name: string,
 *   includes: number}|null} null for text that is not a Topology or
 *   Experiment config. includes is 0 for an experiment.
 */
export function uploadedConfigInfo(text) {
  let parsed;

  try {
    parsed = JSON.parse(text);
  } catch {
    try {
      parsed = YAML.load(String(text ?? ''), { schema: YAML.JSON_SCHEMA });
    } catch {
      return null;
    }
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return null;
  }

  const kind = typeof parsed.kind === 'string' ? parsed.kind.toLowerCase() : '';

  if (kind !== 'topology' && kind !== 'experiment') {
    return null;
  }

  const name = parsed.metadata?.name;
  const entries = parsed.spec?.includeTopologies;

  return {
    kind,
    name: typeof name === 'string' ? name : '',
    includes:
      kind === 'topology' && Array.isArray(entries)
        ? entries.filter(validInclude).length
        : 0,
  };
}
