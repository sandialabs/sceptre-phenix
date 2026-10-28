// Helpers shared with the Configs view, which identify Builder topologies:
// `builder-doc` marks a published diagram, `builder-xml` a legacy one that
// the Builder converts on import or upload. The Configs page loads this file,
// so it imports nothing from the editor.

import YAML from 'js-yaml';

export const LEGACY_ANNOTATION = 'builder-xml';
export const DOC_ANNOTATION = 'builder-doc';
// What the text shows in place of a topology's legacy Builder diagram,
// which is long XML on one line.
export const SNIPPED = '<SNIPPED>';

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
 * Tag text for the configs table, or '' when the config is not Builder
 * authored. It tells Builder documents from legacy Builder diagrams.
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
 * What the Builder does with a config from the Configs page: 'open' a
 * topology with a diagram, 'import' any other topology, else ''.
 */
export function builderAction(config) {
  if (!config || config.kind !== 'Topology') {
    return '';
  }

  return builderAnnotation(config) === DOC_ANNOTATION ? 'open' : 'import';
}

/**
 * Where every Builder control of the Configs page leads (see openLinked in
 * views/Builder.vue).
 */
export function builderLink(config) {
  return { name: 'builder', query: { topology: config.metadata.name } };
}

// An includeTopologies entry the server counts (validIncludeName in
// types/builder/generate.go): no \s, and not blank. To the server, U+0085
// is blank.
function validInclude(entry) {
  return (
    typeof entry === 'string' && !/\s/.test(entry) && /[^\u0085]/.test(entry)
  );
}

/**
 * The kind, the name and the include count of a config file, for the Import
 * dialog before the server reads it. The server still parses and checks the
 * file.
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
