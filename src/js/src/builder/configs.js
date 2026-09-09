// Helpers shared with the Configs view so builder-authored topologies are
// distinguishable: legacy diagrams carry a `builder-xml` annotation, beta
// diagrams carry `builder-doc`.

export const LEGACY_ANNOTATION = 'builder-xml';
export const BETA_ANNOTATION = 'builder-doc';

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

  if (BETA_ANNOTATION in annotations) {
    return BETA_ANNOTATION;
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

  if (annotation === BETA_ANNOTATION) {
    return 'builder beta';
  }

  if (annotation === LEGACY_ANNOTATION) {
    return 'builder legacy';
  }

  return '';
}
