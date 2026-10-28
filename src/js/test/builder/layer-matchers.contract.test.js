import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  MATCH_CONFLICT_HIGHEST,
  MATCH_CONFLICT_LOWEST,
  MAX_LAYER_MATCHERS,
  MAX_MATCHER_PATH_BYTES,
  MAX_MATCHER_PATTERN_BYTES,
  MAX_MATCHER_REPEATS,
  MATCHER_PATH,
  compileMatchers,
  matchDevice,
  matchedLayer,
  matcherPathRule,
  matcherPatternRule,
  valuesAt,
} from '@/builder/layerMatchers.js';
import {
  addNode,
  effectiveNodeLayer,
  findNode,
  layerCounts,
  matchedNodeLayer,
  matcherPathProblem,
  matcherPatternProblem,
  matchersProblem,
  nodeLayerState,
  setLayerSetOptions,
  setLayers,
  setNodeLayer,
  updateNode,
} from '@/builder/model.js';

import bundle from '../../src/builder/schema/builder-v1.schema.json';
import { sampleDocument } from './fixtures.js';

const SPEC = {
  general: { hostname: 'plc-1', description: 'Line 1' },
  hardware: { os_type: 'linux', vcpus: 2, drives: [{ image: 'plc.qc2' }] },
  network: {
    interfaces: [
      { name: 'eth0', vlan: 'EXP', address: '10.0.0.5' },
      { name: 'eth1', vlan: 'MGMT', address: '10.0.1.5' },
    ],
  },
  external: false,
  tags: ['ics', 'level-1'],
  labels: { zone: 'cell' },
};

const device = (spec = SPEC, hostname = spec?.general?.hostname) => ({
  hostname,
  spec,
});

describe('the limits of node matchers', () => {
  test('are the schema bundle’s', () => {
    const matchers = bundle.$defs.layer.properties.matchers;

    expect(matchers.maxProperties).toBe(MAX_LAYER_MATCHERS);
    expect(matchers.propertyNames.maxLength).toBe(MAX_MATCHER_PATH_BYTES);
    expect(matchers.propertyNames.pattern).toBe(MATCHER_PATH.source);
    expect(matchers.additionalProperties.maxLength).toBe(
      MAX_MATCHER_PATTERN_BYTES,
    );
  });
});

describe('a field path', () => {
  test('is dotted keys of letters, digits, _ and -', () => {
    for (const path of [
      'general.hostname',
      'network.interfaces.0.vlan',
      'hardware.os_type',
      'labels.zone-a',
      'a'.repeat(MAX_MATCHER_PATH_BYTES),
    ]) {
      expect(matcherPathRule(path), path).toBe('');
    }

    expect(matcherPathRule('')).toBe('required');
    expect(matcherPathRule(undefined)).toBe('required');
    expect(matcherPathRule('a'.repeat(MAX_MATCHER_PATH_BYTES + 1))).toBe(
      'too-long',
    );

    for (const path of ['general..hostname', '.general', 'host name', 'a/b']) {
      expect(matcherPathRule(path), path).toBe('invalid');
    }
  });

  test('reads text, numbers and booleans at a path, as text', () => {
    expect(valuesAt(SPEC, ['general', 'hostname'])).toEqual(['plc-1']);
    expect(valuesAt(SPEC, ['hardware', 'vcpus'])).toEqual(['2']);
    expect(valuesAt(SPEC, ['external'])).toEqual(['false']);
  });

  test('indexes a list with a key of digits', () => {
    expect(valuesAt(SPEC, ['network', 'interfaces', '1', 'vlan'])).toEqual([
      'MGMT',
    ]);
    expect(valuesAt(SPEC, ['hardware', 'drives', '0', 'image'])).toEqual([
      'plc.qc2',
    ]);
    expect(valuesAt(SPEC, ['network', 'interfaces', '2', 'vlan'])).toEqual([]);
    expect(valuesAt(SPEC, ['network', 'interfaces', 'vlan'])).toEqual([]);
    expect(valuesAt(SPEC, ['network', 'interfaces', '01', 'vlan'])).toEqual([]);
  });

  test('reads each entry of a list at its end', () => {
    expect(valuesAt(SPEC, ['tags'])).toEqual(['ics', 'level-1']);
    expect(valuesAt({ ids: [1, true, null, { a: 1 }, 'x'] }, ['ids'])).toEqual([
      '1',
      'true',
      'x',
    ]);
  });

  test('reads nothing for an object, null or a missing field', () => {
    expect(valuesAt(SPEC, ['labels'])).toEqual([]);
    expect(valuesAt(SPEC, ['general'])).toEqual([]);
    expect(valuesAt({ a: null }, ['a'])).toEqual([]);
    expect(valuesAt(SPEC, ['general', 'missing'])).toEqual([]);
    expect(valuesAt(SPEC, ['general', 'hostname', 'more'])).toEqual([]);
    expect(valuesAt(undefined, ['general'])).toEqual([]);
    expect(valuesAt({ n: Number.NaN }, ['n'])).toEqual([]);
    // A key the object inherits is not a field of it.
    expect(valuesAt({}, ['constructor'])).toEqual([]);
  });
});

describe('a pattern', () => {
  test('is one that the server and the editor read the same way', () => {
    for (const pattern of [
      '^plc-',
      'linux|windows',
      '^(hmi|scada)-\\d+$',
      '^EXP$',
      '[a-z]{2,4}',
      '\\.',
      '\\-',
      '\\x41',
      '(?:ab)+?',
      '(?<site>a)b',
      '[\\d.]',
      'a{1000}',
      '[\\d-a]',
      '[a-b-\\d]',
      '(a+)?',
      '(a|b){0,1}',
      '(a+){1}',
      '[(a+)]+',
      '\\(a+\\)+',
      '^plc-.*-\\d+$',
      '.*foo.*bar.*',
      'a*?b+?c{1,}?',
      '\\*\\+[*+]{0,9}a{2,5}b{3}c*d*e*',
      '(a*)(b+)(c*)',
      'a'.repeat(MAX_MATCHER_PATTERN_BYTES),
    ]) {
      expect(matcherPatternRule(pattern), pattern).toBe('');
    }
  });

  // JavaScript can take a very long time to match these (nestedRepeat in
  // layerset.go refuses the same).
  test('refuses a group with a repeat or a | in it, repeated more than once', () => {
    for (const pattern of [
      '(a+)+',
      '(a*)*',
      '(a|aa)+',
      '(?:a|b)+?',
      '((a+)?b)+',
      '(\\d{1,3}\\.){3}',
      // Repeats in repeats of at most 1000 copies.
      '(a{10}){100}',
      '((a{100}){0}){100}',
      '(a{0,}){1000}',
      '(a*){1000}',
    ]) {
      expect(matcherPatternRule(pattern), pattern).toBe('nested');
    }

    // A pattern that is also not portable is invalid.
    expect(matcherPatternRule('(a+)+(?=b)')).toBe('invalid');
  });

  // JavaScript takes time that grows fast with the count of these repeats
  // (unboundedRepeats in layerset.go refuses the same).
  test('refuses more than 3 of *, + and {n,}', () => {
    expect(MAX_MATCHER_REPEATS).toBe(3);

    for (const pattern of [
      `${'a*'.repeat(12)}b`,
      'a*a*a*a*b',
      '^.*-.*-.*-.*$',
      'a+b{2,}c*d+?',
      '(a*)b+(c*|d)e+',
    ]) {
      expect(matcherPatternRule(pattern), pattern).toBe('repeats');
    }

    // A nested repeat is refused as nested first.
    expect(matcherPatternRule('(a+)+b*c*d*')).toBe('nested');
    expect(matcherPatternRule('a*a*a*a*(?=b)')).toBe('invalid');
  });

  test('a pattern with too many repeats in a saved diagram is not applied', () => {
    const layers = [
      { id: 1, label: 'Slow', matchers: { 'general.hostname': 'a*a*a*a*b' } },
    ];

    expect(compileMatchers(layers)).toEqual([]);
    expect(matchedLayer({ hostname: `${'a'.repeat(63)}!` }, layers)).toBeNull();
  });

  // The worst text for these patterns: a hostname of the most bytes the
  // config schema allows (63), with no match at its end.
  test('a pattern it accepts matches a long hostname quickly', () => {
    const slowText = `${'a'.repeat(63)}!`;
    // V8 compiles each new source, so each run is a first run. The least
    // time leaves out pauses that the pattern does not cause.
    const leastTime = (pattern) =>
      Math.min(
        ...[pattern, `(?:${pattern})`, `(?:(?:${pattern}))`].map((source) => {
          const regex = new RegExp(source);
          const start = performance.now();

          regex.test(slowText);

          return performance.now() - start;
        }),
      );
    // Four repeats, one over the cap, set the bound, so a slow machine slows both.
    const overCap = leastTime(`${'a*'.repeat(4)}b`);
    const part = fc.constantFrom(
      'a*',
      'a+',
      '.*',
      '\\w*',
      '[a-z]+',
      'a{2,}',
      'a*?',
      'a',
    );

    fc.assert(
      fc.property(fc.array(part, { minLength: 1, maxLength: 5 }), (parts) => {
        const pattern = `${parts.join('')}b`;

        if (matcherPatternRule(pattern) !== '') {
          return;
        }

        expect(leastTime(pattern), pattern).toBeLessThan(0.4 * overCap);
      }),
      { numRuns: 25, endOnFailure: true },
    );
  });

  test('a nested repeat in a saved diagram is not applied', () => {
    const matchers = compileMatchers([
      { id: 1, label: 'Slow', matchers: { 'general.hostname': '^(a+)+$' } },
    ]);

    expect(matchers).toEqual([]);
    expect(
      matchedLayer({ hostname: `${'a'.repeat(40)}!` }, [
        { id: 1, label: 'Slow', matchers: { 'general.hostname': '^(a+)+$' } },
      ]),
    ).toBeNull();
  });

  test('is required, and at most 256 bytes', () => {
    expect(matcherPatternRule('')).toBe('required');
    expect(matcherPatternRule(5)).toBe('required');
    expect(matcherPatternRule('a'.repeat(MAX_MATCHER_PATTERN_BYTES + 1))).toBe(
      'too-long',
    );
  });

  test('refuses what does not compile, and what only one side reads', () => {
    for (const pattern of [
      // Neither compiles it.
      '(plc',
      '*a',
      // Only JavaScript compiles these.
      'plc(?=-1)',
      'plc(?!-1)',
      '(?<=a)b',
      '(?<!a)b',
      '(a)\\1',
      '(?<n>a)\\k<n>',
      '\\cA',
      '\\u0041',
      '[\\b]',
      'a{2}*',
      'a{1001}',
      '\\8',
      // A class escape at the end of a range.
      '[a-\\d]',
      '[--\\s]',
      // Repeats in repeats that make more than 1000 copies.
      '(a{500}){500}',
      '(?:a{3}b){334}',
      '(a{2,}){600}',
      '((a{500}){1}){3}',
      // Only RE2 compiles these.
      '(?i)plc',
      '(?P<n>a)',
      // Both compile these, with different meanings.
      '\\Aplc',
      'plc\\z',
      '\\pL',
      '\\x{41}',
      '[[:alpha:]]',
      '[]a]',
      '[^]',
      '\\Qa.b\\E',
    ]) {
      expect(matcherPatternRule(pattern), pattern).toBe('invalid');
    }
  });

  test('a problem reads as a sentence for the editor', () => {
    expect(matcherPathProblem('general.hostname')).toBe('');
    expect(matcherPathProblem('')).toBe(
      'Type a field, such as general.hostname.',
    );
    expect(matcherPathProblem('host name')).toMatch(/joined by dots/);
    expect(matcherPathProblem('a'.repeat(129))).toBe(
      'A field holds at most 128 bytes.',
    );
    expect(matcherPatternProblem('^plc-')).toBe('');
    expect(matcherPatternProblem('')).toBe(
      'Type a regular expression, such as ^plc-.',
    );
    expect(matcherPatternProblem('(?=a)')).toMatch(/lookarounds/);
    expect(matcherPatternProblem('(a+)+')).toBe(
      'Do not repeat a group that has a repeat or | in it, as in (a+)+. Such a pattern can stop the editor.',
    );
    expect(matcherPatternProblem('a*a*a*a*b')).toBe(
      'Use at most 3 of *, + and {n,} in a pattern. More can stop the editor.',
    );
    expect(matchersProblem({ 'general.hostname': '^plc' })).toBe('');
    expect(matchersProblem(null)).toBe('');
    expect(matchersProblem(['general.hostname'])).toMatch(/not an object/);
    expect(
      matchersProblem(
        Object.fromEntries(
          Array.from({ length: 17 }, (_, at) => [`general.k${at}`, 'x']),
        ),
      ),
    ).toBe('A layer has at most 16 node matchers.');
    expect(matchersProblem({ 'general.hostname': '(' })).toMatch(
      /not a regular expression/,
    );
  });
});

describe('matching a device', () => {
  const LAYERS = [
    { id: 3, label: 'Operations', matchers: { 'general.hostname': '^hmi-' } },
    {
      id: 1,
      label: 'Control',
      matchers: { 'general.hostname': '^plc-', 'labels.zone': 'cell' },
    },
    { id: 0, label: 'Process', matchers: { tags: '^level-1$' } },
    { id: 5, label: 'Plain' },
  ];

  test('finds the layer of a matcher that matches, and the field', () => {
    const matchers = compileMatchers(LAYERS);

    // Layer 0 comes first: the lowest ID wins.
    expect(matchDevice(device(), matchers)).toEqual({
      id: 0,
      path: 'tags',
    });
    expect(
      matchDevice(device({ general: { hostname: 'hmi-2' } }), matchers),
    ).toEqual({ id: 3, path: 'general.hostname' });
    expect(
      matchDevice(device({ general: { hostname: 'ws-1' } }), matchers),
    ).toBeNull();
  });

  test('gives a conflict to the lowest layer ID, or the highest', () => {
    const lowest = compileMatchers(LAYERS, MATCH_CONFLICT_LOWEST);
    const highest = compileMatchers(LAYERS, MATCH_CONFLICT_HIGHEST);
    const both = device({
      general: { hostname: 'plc-1' },
      tags: ['level-1'],
    });

    expect(lowest.map((layer) => layer.id)).toEqual([0, 1, 3]);
    expect(highest.map((layer) => layer.id)).toEqual([3, 1, 0]);
    expect(matchDevice(both, lowest).id).toBe(0);
    expect(matchDevice(both, highest)).toEqual({
      id: 1,
      path: 'general.hostname',
    });
  });

  test('a list matches when one entry matches', () => {
    const matchers = compileMatchers([
      { id: 2, label: 'DMZ', matchers: { 'hardware.drives': 'fw' } },
      { id: 4, label: 'EXP', matchers: { 'network.interfaces.0.vlan': 'EXP' } },
    ]);

    expect(matchDevice(device(), matchers)).toEqual({
      id: 4,
      path: 'network.interfaces.0.vlan',
    });
    // The drives are objects: they never match.
    expect(
      matchDevice(device({ hardware: { drives: [{ image: 'fw' }] } }), [
        matchers[0],
      ]),
    ).toBeNull();
  });

  test('numbers and booleans match as text', () => {
    const matchers = compileMatchers([
      { id: 2, label: 'Big', matchers: { 'hardware.vcpus': '^2$' } },
      { id: 3, label: 'External', matchers: { external: '^true$' } },
    ]);

    expect(matchDevice(device(), matchers)?.id).toBe(2);
    expect(
      matchDevice(device({ external: true, hardware: { vcpus: 4 } }), matchers)
        ?.id,
    ).toBe(3);
  });

  test('the device’s hostname stands for a spec without one', () => {
    const matchers = compileMatchers([
      { id: 1, label: 'Control', matchers: { 'general.hostname': '^plc' } },
    ]);

    expect(matchDevice({ hostname: 'plc-9', spec: {} }, matchers)?.id).toBe(1);
    expect(matchDevice({ hostname: 'plc-9' }, matchers)?.id).toBe(1);
    expect(
      matchDevice(
        { hostname: 'plc-9', spec: { general: { hostname: 'ws' } } },
        matchers,
      ),
    ).toBeNull();
  });

  test('a matcher that is not usable is left out', () => {
    const matchers = compileMatchers([
      {
        id: 1,
        label: 'Control',
        matchers: {
          'general.hostname': '(plc',
          'bad path': '.',
          'hardware.os_type': '(?=linux)',
          external: '',
        },
      },
      { id: 2, label: 'No list', matchers: ['general.hostname'] },
      { label: 'No ID', matchers: { 'general.hostname': '.' } },
    ]);

    expect(matchers).toEqual([]);
    expect(matchDevice(device(), matchers)).toBeNull();
  });

  test('keeps an answer while the list and the payload stay the same', () => {
    const layers = [
      { id: 1, label: 'C', matchers: { 'general.hostname': 'p' } },
    ];
    const payload = device();

    expect(matchedLayer(payload, layers)).toBe(matchedLayer(payload, layers));
    expect(matchedLayer(payload, layers, MATCH_CONFLICT_HIGHEST)).toEqual({
      id: 1,
      path: 'general.hostname',
    });
    expect(matchedLayer(payload, [])).toBeNull();
    expect(matchedLayer(payload, undefined)).toBeNull();
    expect(matchedLayer(null, layers)).toBeNull();
  });
});

describe('the layer of a node in a diagram', () => {
  function plant() {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const layered = setLayers(doc, [
      {
        id: 3,
        label: 'Operations',
        matchers: { 'general.hostname': `^${alpha.device.hostname}$` },
      },
      {
        id: 1,
        label: 'Control',
        matchers: { 'general.hostname': '.' },
      },
    ]);

    return { doc: layered, alpha, bravo, sw };
  }

  test('is the layer chosen for it, else the layer its fields match', () => {
    const { doc, alpha, bravo, sw } = plant();

    expect(effectiveNodeLayer(doc, findNode(doc, alpha.id))).toBe(1);
    expect(matchedNodeLayer(doc, findNode(doc, alpha.id))).toEqual({
      id: 1,
      path: 'general.hostname',
    });
    // A switch never matches.
    expect(effectiveNodeLayer(doc, findNode(doc, sw.id))).toBeNull();

    // The highest ID wins when the layer set says so.
    const highest = setLayerSetOptions(doc, { matchConflict: 'highest' });

    expect(effectiveNodeLayer(highest, findNode(highest, alpha.id))).toBe(3);
    expect(effectiveNodeLayer(highest, findNode(highest, bravo.id))).toBe(1);

    // A layer chosen by hand wins, and removing it gives the match again.
    const manual = setNodeLayer(highest, alpha.id, 7);

    expect(nodeLayerState(manual, findNode(manual, alpha.id))).toEqual({
      layer: 7,
      manual: 7,
      matched: { id: 3, path: 'general.hostname' },
    });

    const cleared = setNodeLayer(manual, alpha.id, null);

    expect(nodeLayerState(cleared, findNode(cleared, alpha.id))).toEqual({
      layer: 3,
      manual: null,
      matched: { id: 3, path: 'general.hostname' },
    });
  });

  test('follows the fields as they change', () => {
    const { doc, alpha } = plant();
    const node = findNode(doc, alpha.id);
    const highest = setLayerSetOptions(doc, { matchConflict: 'highest' });

    expect(effectiveNodeLayer(highest, findNode(highest, alpha.id))).toBe(3);

    const renamed = updateNode(highest, alpha.id, {
      device: {
        ...node.device,
        hostname: 'other',
        spec: {
          ...node.device.spec,
          general: { ...node.device.spec.general, hostname: 'other' },
        },
      },
    });

    expect(effectiveNodeLayer(renamed, findNode(renamed, alpha.id))).toBe(1);
  });

  test('counts each node at the layer it is at', () => {
    const { doc, alpha, bravo, sw } = plant();
    const withSwitch = setNodeLayer(doc, sw.id, 3);

    expect(Object.fromEntries(layerCounts(withSwitch))).toEqual({
      1: [alpha, bravo].length,
      3: 1,
    });
  });

  test('a new device matches as soon as it is added', () => {
    const { doc } = plant();
    const { doc: next, node } = addNode(doc, {
      kind: 'device',
      hostname: 'ws-1',
    });

    expect(effectiveNodeLayer(next, findNode(next, node.id))).toBe(1);
  });
});
