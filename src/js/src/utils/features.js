// Feature flag helpers.
//
// Feature loading must be deterministic: routes that depend on a flag wait for
// the /features response instead of racing it, and a disabled feature is
// denied before its lazy chunk is ever requested.

export const BUILDER_BETA_FEATURE = 'builder-beta';

/**
 * @param {string[]} features
 * @param {string} flag
 * @returns {boolean}
 */
export function isFeatureEnabled(features, flag) {
  return Array.isArray(features) && features.includes(flag);
}

/**
 * Builds a Vue Router `beforeEnter` guard for a feature-flagged route.
 *
 * The guard resolves the feature list first, so a flag that is off denies
 * navigation before Vue Router resolves (and therefore downloads) the route's
 * async component.
 *
 * @param {object} options flag, ensureFeatures, fallback, onDenied, onError
 * @returns {(to: object) => Promise<true|object>}
 */
export function createFeatureGuard(options) {
  const {
    flag,
    ensureFeatures,
    fallback = { name: 'home' },
    onDenied = () => {},
    onError = () => {},
  } = options;

  return async function featureGuard(to) {
    let features;

    try {
      features = await ensureFeatures();
    } catch (error) {
      onError(error, to, flag);

      return fallback;
    }

    if (isFeatureEnabled(features, flag)) {
      return true;
    }

    onDenied(to, flag);

    return fallback;
  };
}
