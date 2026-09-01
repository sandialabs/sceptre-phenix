import { describe, expect, test, vi } from 'vitest';

import {
  BUILDER_BETA_FEATURE,
  createFeatureGuard,
  isFeatureEnabled,
} from '@/utils/features.js';

describe('feature flags', () => {
  test('the builder beta flag matches the backend name', () => {
    expect(BUILDER_BETA_FEATURE).toBe('builder-beta');
  });

  test('enabled checks tolerate missing feature lists', () => {
    expect(isFeatureEnabled(['builder-beta'], 'builder-beta')).toBe(true);
    expect(isFeatureEnabled([], 'builder-beta')).toBe(false);
    expect(isFeatureEnabled(undefined, 'builder-beta')).toBe(false);
    expect(isFeatureEnabled(null, 'builder-beta')).toBe(false);
  });
});

describe('feature route guard', () => {
  test('waits for features before allowing navigation', async () => {
    const order = [];
    const guard = createFeatureGuard({
      flag: BUILDER_BETA_FEATURE,
      ensureFeatures: async () => {
        order.push('features');
        return ['builder-beta'];
      },
    });

    await expect(guard({ name: 'builder-beta' })).resolves.toBe(true);
    expect(order).toEqual(['features']);
  });

  test('denies the route without loading the editor chunk', async () => {
    const loadChunk = vi.fn();
    const onDenied = vi.fn();
    const guard = createFeatureGuard({
      flag: BUILDER_BETA_FEATURE,
      ensureFeatures: async () => [],
      onDenied,
    });

    // Simulates Vue Router: beforeEnter runs before the lazy component loads.
    const result = await guard({ name: 'builder-beta' });

    if (result === true) {
      loadChunk();
    }

    expect(result).toEqual({ name: 'home' });
    expect(loadChunk).not.toHaveBeenCalled();
    expect(onDenied).toHaveBeenCalledWith(
      { name: 'builder-beta' },
      'builder-beta',
    );
  });

  test('a custom fallback route is honoured', async () => {
    const guard = createFeatureGuard({
      flag: 'nope',
      ensureFeatures: async () => ['other'],
      fallback: { name: 'signin' },
    });

    await expect(guard({})).resolves.toEqual({ name: 'signin' });
  });

  test('a failed features fetch reports the error and denies access', async () => {
    const error = new Error('feature endpoint unavailable');
    const onError = vi.fn();
    const guard = createFeatureGuard({
      flag: BUILDER_BETA_FEATURE,
      ensureFeatures: async () => {
        throw error;
      },
      onError,
    });

    await expect(guard({})).resolves.toEqual({ name: 'home' });
    expect(onError).toHaveBeenCalledWith(error, {}, BUILDER_BETA_FEATURE);
  });
});
