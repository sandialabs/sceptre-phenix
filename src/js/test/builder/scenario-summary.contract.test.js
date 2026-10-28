import { describe, expect, test } from 'vitest';

import { scenarioAppNote, scenarioCounts } from '@/builder/scenarioSummary.js';

const app = (name, hosts = [], disabled = false) => ({ name, hosts, disabled });

// The counts after a scenario's name in the Inspector.
describe('scenarioCounts', () => {
  test('counts the apps and the hosts, singular or plural', () => {
    expect(scenarioCounts([])).toBe('0 apps, 0 hosts');
    expect(scenarioCounts([app('scorch')])).toBe('1 app, 0 hosts');
    expect(scenarioCounts([app('ntp', ['alpha'])])).toBe('1 app, 1 host');
    expect(scenarioCounts([app('ntp', ['alpha', 'bravo']), app('tap')])).toBe(
      '2 apps, 2 hosts',
    );
  });

  test('counts a host once, also when several apps run on it', () => {
    expect(
      scenarioCounts([app('ntp', ['alpha', 'bravo']), app('soh', ['alpha'])]),
    ).toBe('2 apps, 2 hosts');
  });

  test('counts disabled apps among the apps, and says them only when there are some', () => {
    expect(scenarioCounts([app('ntp', ['alpha']), app('soh', [], true)])).toBe(
      '2 apps, 1 host, 1 disabled app',
    );
    expect(
      scenarioCounts([
        app('soh', ['alpha'], true),
        app('tap', ['bravo'], true),
        app('ntp'),
      ]),
    ).toBe('3 apps, 2 hosts, 2 disabled apps');
  });

  test('takes no list as no apps', () => {
    expect(scenarioCounts(undefined)).toBe('0 apps, 0 hosts');
  });
});

// What shows beside an app's name, in parentheses.
describe('scenarioAppNote', () => {
  test('says disabled, no hosts, both, or nothing', () => {
    expect(scenarioAppNote(app('ntp', ['alpha']))).toBe('');
    expect(scenarioAppNote(app('tap'))).toBe('no hosts');
    expect(scenarioAppNote(app('soh', ['alpha'], true))).toBe('disabled');
    expect(scenarioAppNote(app('soh', [], true))).toBe('disabled, no hosts');
  });
});
