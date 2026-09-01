import { describe, expect, test } from 'vitest';

import {
  actionFor,
  buildPublishIntent,
  describePublishResult,
  scenarioNames,
  stageFailed,
} from '@/builder/publish.js';

describe('publish intent', () => {
  test('never carries the document', () => {
    const { intent } = buildPublishIntent(
      { mode: 'topology', topologyName: 'core' },
      { topologies: [] },
    );

    expect(intent).toEqual({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });
    expect(Object.keys(intent)).not.toContain('document');
  });

  test('an existing config name becomes an update', () => {
    const { intent } = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: ' core ',
        experimentName: 'exp',
      },
      { topologies: ['core'], experiments: ['exp'] },
    );

    expect(intent.topology).toEqual({ name: 'core', action: 'update' });
    expect(intent.experiment).toEqual({ name: 'exp', action: 'update' });
  });

  test('source entries select update by name', () => {
    expect(actionFor('core', [{ name: 'core' }])).toBe('update');
  });

  test('a missing topology name is refused', () => {
    const { intent, error } = buildPublishIntent({ topologyName: '  ' }, {});

    expect(intent).toBeNull();
    expect(error).toMatch(/topology/i);
  });

  test('an experiment name is required in experiment mode', () => {
    const { intent, error } = buildPublishIntent(
      { mode: 'topology-experiment', topologyName: 'core' },
      {},
    );

    expect(intent).toBeNull();
    expect(error).toMatch(/experiment/i);
  });

  test('a stored scenario is used, not rewritten', () => {
    const { intent } = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: 'core',
        experimentName: 'exp',
      },
      { scenario: { kind: 'stored', name: 'sc' } },
    );

    expect(intent.scenario).toEqual({ name: 'sc', action: 'use' });
  });

  test('an uploaded scenario needs an explicit target', () => {
    const context = {
      scenario: { kind: 'uploaded', name: 'sc.yaml' },
      scenarios: [{ name: 'sc', digest: `sha256:${'a'.repeat(64)}` }],
    };
    const refused = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: 'core',
        experimentName: 'exp',
      },
      context,
    );

    expect(refused.intent).toBeNull();
    expect(refused.error).toMatch(/uploaded scenario/i);

    const chosen = buildPublishIntent(
      {
        topologyName: 'core',
        mode: 'topology-experiment',
        experimentName: 'exp',
        scenarioName: 'sc',
        scenarioAction: 'update',
      },
      context,
    );

    expect(chosen.intent.scenario).toEqual({
      name: 'sc',
      action: 'update',
      expectedDigest: `sha256:${'a'.repeat(64)}`,
    });
  });

  test('an unknown mode falls back to topology only', () => {
    const { intent } = buildPublishIntent(
      { mode: 'everything', topologyName: 'core', experimentName: 'exp' },
      {},
    );

    expect(intent.mode).toBe('topology');
    expect(intent.experiment).toBeUndefined();
  });
});

describe('publish result', () => {
  test('describes success, partial failure and failure differently', () => {
    expect(describePublishResult({ ok: true })).toMatch(/succeeded/i);
    expect(describePublishResult({ ok: false, partial: true })).toMatch(
      /some configs were written/i,
    );
    expect(describePublishResult({ ok: false, partial: false })).toMatch(
      /no configs were written/i,
    );
    expect(describePublishResult(null)).toBe('');
  });

  test('marks the failed stages', () => {
    expect(stageFailed({ status: 'failed' })).toBe(true);
    expect(stageFailed({ status: 'error' })).toBe(true);
    expect(stageFailed({ status: 'created' })).toBe(false);
  });
});

describe('sources', () => {
  test('accepts scenario names as strings or entries', () => {
    expect(scenarioNames(['a', { name: 'b' }, {}, null])).toEqual(['a', 'b']);
  });

  test('chooses create or update from what the server has', () => {
    expect(actionFor('core', ['core'])).toBe('update');
    expect(actionFor(' core ', ['core'])).toBe('update');
    expect(actionFor('other', ['core'])).toBe('create');
    expect(actionFor('other')).toBe('create');
  });
});
