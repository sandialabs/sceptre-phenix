import { describe, expect, test } from 'vitest';

import {
  DOC_ANNOTATION,
  LEGACY_ANNOTATION,
  builderAction,
  builderAnnotation,
  builderLink,
  builderTagLabel,
  isBuilderAnnotation,
  uploadedConfigInfo,
} from '@/builder/configs.js';

function config(kind, annotations) {
  return {
    kind,
    metadata: annotations ? { name: 'lab', annotations } : { name: 'lab' },
  };
}

const reference = { digest: `sha256:${'0'.repeat(64)}`, id: 'a'.repeat(64) };

describe('Builder annotations of a config', () => {
  test('the annotation names are the ones the server writes and reads', () => {
    expect(DOC_ANNOTATION).toBe('builder-doc');
    expect(LEGACY_ANNOTATION).toBe('builder-xml');
  });

  test('a topology is named by the annotation it carries', () => {
    expect(
      builderAnnotation(config('Topology', { 'builder-doc': reference })),
    ).toBe(DOC_ANNOTATION);
    expect(
      builderAnnotation(
        config('Topology', { 'builder-xml': '<mxGraphModel/>' }),
      ),
    ).toBe(LEGACY_ANNOTATION);
  });

  test('builder-doc wins when a topology carries both', () => {
    expect(
      builderAnnotation(
        config('Topology', {
          'builder-xml': '<mxGraphModel/>',
          'builder-doc': reference,
        }),
      ),
    ).toBe(DOC_ANNOTATION);
  });

  test('other configs carry neither', () => {
    expect(builderAnnotation(config('Topology'))).toBeNull();
    expect(builderAnnotation(config('Topology', { owner: 'ops' }))).toBeNull();
    expect(
      builderAnnotation(config('Scenario', { 'builder-doc': reference })),
    ).toBeNull();
    expect(builderAnnotation(null)).toBeNull();
  });

  test('every annotation of the Builder starts with its prefix', () => {
    expect(isBuilderAnnotation(DOC_ANNOTATION)).toBe(true);
    expect(isBuilderAnnotation(LEGACY_ANNOTATION)).toBe(true);
    expect(isBuilderAnnotation('builder-experiment')).toBe(true);
    expect(isBuilderAnnotation('maintainer')).toBe(false);
  });
});

describe('the Configs page tag', () => {
  test('a topology with builder-doc is tagged builder', () => {
    expect(
      builderTagLabel(config('Topology', { 'builder-doc': reference })),
    ).toBe('builder');
  });

  test('a topology with builder-xml is tagged builder legacy', () => {
    expect(
      builderTagLabel(config('Topology', { 'builder-xml': '<mxGraphModel/>' })),
    ).toBe('builder legacy');
  });

  test('a config that is not Builder authored has no tag', () => {
    // No Builder annotation, no annotations at all, and another kind.
    expect(builderTagLabel(config('Topology', { owner: 'ops' }))).toBe('');
    expect(builderTagLabel(config('Topology'))).toBe('');
    expect(
      builderTagLabel(config('Experiment', { 'builder-doc': reference })),
    ).toBe('');
  });
});

describe('the Builder controls of the Configs page', () => {
  test('a topology with a Builder diagram is opened, any other topology imported', () => {
    expect(
      builderAction(config('Topology', { 'builder-doc': reference })),
    ).toBe('open');
    expect(
      builderAction(config('Topology', { 'builder-xml': '<mxGraphModel/>' })),
    ).toBe('import');
    expect(builderAction(config('Topology', { owner: 'ops' }))).toBe('import');
    expect(builderAction(config('Topology'))).toBe('import');
    // builder-doc wins, as it does for the tag.
    expect(
      builderAction(
        config('Topology', {
          'builder-xml': '<mxGraphModel/>',
          'builder-doc': reference,
        }),
      ),
    ).toBe('open');
  });

  test('no other config has a Builder control', () => {
    expect(
      builderAction(config('Experiment', { 'builder-doc': reference })),
    ).toBe('');
    expect(builderAction(config('Scenario'))).toBe('');
    expect(builderAction({ kind: null, metadata: { name: null } })).toBe('');
    expect(builderAction(null)).toBe('');
  });

  test('every control leads to the one link, which names the topology', () => {
    expect(builderLink(config('Topology'))).toEqual({
      name: 'builder',
      query: { topology: 'lab' },
    });
    // The annotation does not change where the link leads.
    expect(
      builderLink(config('Topology', { 'builder-doc': reference })),
    ).toEqual(builderLink(config('Topology')));
  });
});

describe('what the Import dialog reads of a config file', () => {
  const yaml = [
    'apiVersion: phenix.sandia.gov/v1',
    'kind: Topology',
    'metadata:',
    '  name: riverside',
    'spec:',
    '  includeTopologies:',
    '    - corp-services',
    '    - lab-dns',
    '  nodes: []',
    '',
  ].join('\n');

  test('a YAML topology gives its name and how many topologies it includes', () => {
    expect(uploadedConfigInfo(yaml)).toEqual({
      kind: 'topology',
      name: 'riverside',
      includes: 2,
    });
  });

  test('a JSON topology is read the same way', () => {
    expect(
      uploadedConfigInfo(
        JSON.stringify({
          kind: 'Topology',
          metadata: { name: 'site' },
          spec: { includeTopologies: ['a'], nodes: [] },
        }),
      ),
    ).toEqual({ kind: 'topology', name: 'site', includes: 1 });
  });

  test('a topology without includes, or without a name, is still a topology', () => {
    expect(
      uploadedConfigInfo('kind: Topology\nmetadata: {name: lone}\nspec: {}'),
    ).toEqual({ kind: 'topology', name: 'lone', includes: 0 });
    expect(uploadedConfigInfo('kind: topology')).toEqual({
      kind: 'topology',
      name: '',
      includes: 0,
    });
    expect(
      uploadedConfigInfo('kind: Topology\nspec: {includeTopologies: site}'),
    ).toMatchObject({ includes: 0 });
  });

  test('only the entries the server counts are counted', () => {
    const entries = [
      'good',
      '',
      '  ',
      'two words',
      'tab\tbed',
      7,
      null,
      'good',
    ];

    expect(
      uploadedConfigInfo(
        JSON.stringify({
          kind: 'Topology',
          metadata: { name: 'site' },
          spec: { includeTopologies: entries },
        }),
      ).includes,
      // A name listed twice counts twice, as the server counts it.
    ).toBe(2);
  });

  test('the includes of an experiment are not offered', () => {
    expect(
      uploadedConfigInfo(
        'kind: Experiment\nmetadata: {name: run}\nspec: {includeTopologies: [a]}',
      ),
    ).toEqual({ kind: 'experiment', name: 'run', includes: 0 });
  });

  test('anything else gives nothing, and never throws', () => {
    for (const text of [
      '',
      '   ',
      'not: [closed',
      '{"kind": "Topology"',
      'kind: Scenario\nmetadata: {name: apps}',
      'just words',
      '- a\n- b',
      '42',
      'null',
      'kind: [Topology]',
      // A JavaScript value is plain text here: nothing is evaluated.
      'kind: !!js/function "function () {}"',
    ]) {
      expect(uploadedConfigInfo(text), text).toBeNull();
    }

    expect(uploadedConfigInfo(undefined)).toBeNull();
    expect(uploadedConfigInfo(null)).toBeNull();
  });
});
