// The Scenarios dialog's list, and the Scenario configs that it shows,
// edits and creates (builder/scenarios.js).

import { describe, expect, test } from 'vitest';

// api.js loads the page's HTTP client, which needs a browser.

import {
  filterScenarioRows,
  newScenarioConfig,
  newScenarioName,
  readScenarioText,
  scenarioErrorText,
  scenarioRows,
  scenarioText,
  toggleScenario,
  validationLines,
} from '@/builder/scenarios.js';
import { MAX_SCENARIO_NAME_BYTES } from '@/builder/validate.js';

const V2 = 'phenix.sandia.gov/v2';

// An axios-like error that the server answered with `status`.
function refused(status, data = {}) {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data },
  });
}

describe('scenarioRows', () => {
  test('lists every stored scenario, sorted by name, with the listed ones checked', () => {
    expect(
      scenarioRows(['plant-ntp'], ['zeta', 'plant-ntp', 'Alpha', 's10', 's2']),
    ).toEqual([
      { name: 'Alpha', checked: false, missing: false },
      { name: 'plant-ntp', checked: true, missing: false },
      { name: 's2', checked: false, missing: false },
      { name: 's10', checked: false, missing: false },
      { name: 'zeta', checked: false, missing: false },
    ]);
  });

  test('a listed scenario the server does not list is checked and missing', () => {
    expect(scenarioRows(['gone', 'plant'], ['plant'])).toEqual([
      { name: 'gone', checked: true, missing: true },
      { name: 'plant', checked: true, missing: false },
    ]);
  });

  test('a known scenario keeps its row when it is no longer listed', () => {
    expect(scenarioRows([], ['plant'], ['gone'])).toEqual([
      { name: 'gone', checked: false, missing: true },
      { name: 'plant', checked: false, missing: false },
    ]);
  });

  test('names compare ignoring case, and a row takes the stored spelling', () => {
    expect(scenarioRows(['Plant-NTP'], ['plant-ntp'])).toEqual([
      { name: 'plant-ntp', checked: true, missing: false },
    ]);
  });

  test('the filter keeps the rows whose name has its text, in any case', () => {
    const rows = scenarioRows([], ['plant-ntp', 'plant-dns', 'attack']);

    expect(filterScenarioRows(rows, ' PLANT ').map((row) => row.name)).toEqual([
      'plant-dns',
      'plant-ntp',
    ]);
    expect(filterScenarioRows(rows, '')).toBe(rows);
    expect(filterScenarioRows(rows, 'none')).toEqual([]);
  });
});

describe('toggleScenario', () => {
  test('a name set is added at the end, once', () => {
    expect(toggleScenario(['b'], 'a', true)).toEqual(['b', 'a']);
    expect(toggleScenario(['B'], 'b', true)).toEqual(['B']);
  });

  test('a name cleared is removed in any case', () => {
    expect(toggleScenario(['a', 'Plant', 'c'], 'plant', false)).toEqual([
      'a',
      'c',
    ]);
    expect(toggleScenario(['a'], 'b', false)).toEqual(['a']);
  });
});

describe('a new scenario', () => {
  test('is named after the diagram, as a config name, and is unique', () => {
    expect(newScenarioName('Plant floor (v2)')).toBe('Plant-floor-v2-scenario');
    expect(newScenarioName('plant', ['other'])).toBe('plant-scenario');
    expect(
      newScenarioName('plant', ['PLANT-scenario', 'plant-scenario-2']),
    ).toBe('plant-scenario-3');
  });

  test('a diagram name with no allowed character gives "scenario"', () => {
    expect(newScenarioName('!!!')).toBe('scenario');
    expect(newScenarioName('', ['scenario'])).toBe('scenario-2');
  });

  test('a name is never longer than a document allows', () => {
    const long = 'n'.repeat(400);

    expect(newScenarioName(long)).toHaveLength(MAX_SCENARIO_NAME_BYTES);
    expect(newScenarioName(long, [newScenarioName(long)])).toMatch(/n-2$/);
    expect(
      newScenarioName(long, [newScenarioName(long)]).length,
    ).toBeLessThanOrEqual(MAX_SCENARIO_NAME_BYTES);
  });

  test('names the topology that the Publish dialog offers, and has no apps', () => {
    const doc = { metadata: { name: 'Plant floor' } };

    expect(newScenarioConfig(doc, ['Plant-floor-scenario'])).toEqual({
      apiVersion: V2,
      kind: 'Scenario',
      metadata: {
        name: 'Plant-floor-scenario-2',
        annotations: { topology: 'Plant-floor' },
      },
      spec: { apps: [] },
    });
  });

  test('has no topology annotation when the diagram name gives no topology name', () => {
    expect(newScenarioConfig({ metadata: { name: '???' } })).toEqual({
      apiVersion: V2,
      kind: 'Scenario',
      metadata: { name: 'scenario' },
      spec: { apps: [] },
    });
  });
});

describe('scenarioText', () => {
  const stored = {
    apiVersion: V2,
    kind: 'Scenario',
    metadata: {
      name: 'plant',
      created: '2026-01-01T00:00:00Z',
      updated: '2026-01-02T00:00:00Z',
      annotations: { topology: 'plant' },
    },
    spec: { apps: [{ name: 'ntp' }] },
  };

  test('shows the stored config as YAML, in the order of a config', () => {
    expect(scenarioText(stored)).toBe(
      [
        `apiVersion: ${V2}`,
        'kind: Scenario',
        'metadata:',
        '  name: plant',
        "  created: '2026-01-01T00:00:00Z'",
        "  updated: '2026-01-02T00:00:00Z'",
        '  annotations:',
        '    topology: plant',
        'spec:',
        '  apps:',
        '    - name: ntp',
        '',
      ].join('\n'),
    );
  });

  test('text to edit leaves out the times the server sets', () => {
    const text = scenarioText(stored, { editable: true });

    expect(text).not.toContain('created');
    expect(text).not.toContain('updated');
    expect(text).toContain('annotations:\n    topology: plant');
    expect(readScenarioText(text, { name: 'plant' })).toEqual({
      config: {
        apiVersion: V2,
        kind: 'Scenario',
        metadata: { name: 'plant', annotations: { topology: 'plant' } },
        spec: { apps: [{ name: 'ntp' }] },
      },
    });
  });

  test('a new config reads back as it is', () => {
    const config = newScenarioConfig({ metadata: { name: 'plant' } });

    expect(readScenarioText(scenarioText(config, { editable: true }))).toEqual({
      config,
    });
  });
});

describe('readScenarioText', () => {
  const text = (lines) => lines.join('\n');
  const valid = [
    `apiVersion: ${V2}`,
    'kind: Scenario',
    'metadata:',
    '  name: plant',
    'spec:',
    '  apps: []',
  ];

  test('refuses empty text and text that is not YAML', () => {
    expect(readScenarioText('  ')).toEqual({
      error: 'Enter the scenario config.',
    });

    const broken = readScenarioText('a: [1, 2\nb: 3');

    expect(broken.parse).toBe(true);
    expect(broken.error).toMatch(/line \d+, column \d+|\(\d+:\d+\)/);
    expect(readScenarioText('- one\n- two')).toEqual({
      error: 'The scenario config must be a YAML object.',
    });
    expect(readScenarioText('a: &x 1\nb: *x').error).toMatch(
      /^The Builder does not support YAML aliases \(\*name\)\./,
    );
  });

  test('refuses another kind, another version and a missing part', () => {
    expect(
      readScenarioText(text(valid).replace('Scenario', 'Topology')),
    ).toEqual({ error: 'The kind must be Scenario, not Topology.' });
    expect(
      readScenarioText(text(valid).replace(V2, 'phenix.sandia.gov/v1')),
    ).toEqual({
      error: `The apiVersion must be ${V2}. The Builder stores only ${V2} scenarios.`,
    });
    expect(
      readScenarioText(text(valid.slice(0, 2).concat(valid.slice(4)))),
    ).toEqual({ error: 'The config must have a metadata object with a name.' });
    expect(readScenarioText(text(valid.slice(0, 4)))).toEqual({
      error: 'The config must have a spec object.',
    });
  });

  test('refuses a name a config cannot have, and a new name for an edit', () => {
    expect(
      readScenarioText(text(valid).replace('name: plant', 'name: plant ntp')),
    ).toEqual({
      error:
        'Scenario name "plant ntp" may use only letters, numbers, underscores, at signs, periods and hyphens.',
    });
    expect(readScenarioText(text(valid), { name: 'other' })).toEqual({
      error:
        'The name must stay other. To store the scenario with a new name, select Create new scenario.',
    });
  });

  test('reads JSON too', () => {
    const config = {
      apiVersion: V2,
      kind: 'Scenario',
      metadata: { name: 'plant' },
      spec: { apps: [] },
    };

    expect(readScenarioText(JSON.stringify(config))).toEqual({ config });
  });
});

describe('scenarioErrorText', () => {
  test('says what a refusal means', () => {
    expect(scenarioErrorText(refused(403), 'read scenario plant')).toBe(
      'You do not have permission to read scenario plant.',
    );
    expect(scenarioErrorText(refused(401), 'read scenario plant')).toBe(
      'Could not read scenario plant. Your session has ended. Log in again.',
    );
    expect(scenarioErrorText(refused(404), 'read scenario plant')).toBe(
      'Could not read scenario plant. The server does not have this scenario.',
    );
    expect(scenarioErrorText(new Error('Network'), 'read scenario plant')).toBe(
      'Could not read scenario plant. The Builder could not reach the server. Check the connection and try again.',
    );
  });

  test("uses the server's reason for any other refusal", () => {
    expect(
      scenarioErrorText(
        refused(400, { message: 'config with same name already exists' }),
        'create scenario plant',
      ),
    ).toBe(
      'Could not create scenario plant. Config with same name already exists.',
    );
    expect(scenarioErrorText(refused(500), 'save scenario plant')).toBe(
      'Could not save scenario plant. The server rejected the request.',
    );
  });

  test('lists each problem the server found in the config', () => {
    expect(
      validationLines(
        refused(400, {
          metadata: { validation: 'apps[0]: missing name\n\n  hint: x\n' },
        }),
      ),
    ).toEqual(['apps[0]: missing name', '  hint: x']);
    expect(validationLines(refused(400))).toEqual([]);
  });
});
