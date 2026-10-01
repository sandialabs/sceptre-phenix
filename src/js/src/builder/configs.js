// Helpers shared with the Configs view so builder-authored topologies are
// distinguishable: legacy diagrams carry a `builder-xml` annotation, Builder v2
// diagrams carry `builder-doc`.

export const LEGACY_ANNOTATION = 'builder-xml';
export const V2_ANNOTATION = 'builder-doc';

// Every annotation the Builders keep for themselves starts with this: the
// two above, and builder-experiment on an experiment Builder v2 published
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

  if (V2_ANNOTATION in annotations) {
    return V2_ANNOTATION;
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

  if (annotation === V2_ANNOTATION) {
    return 'builder v2';
  }

  if (annotation === LEGACY_ANNOTATION) {
    return 'builder legacy';
  }

  return '';
}
