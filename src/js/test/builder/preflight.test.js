import { describe, expect, it, vi } from 'vitest';

// The app's HTTP client loads the router, which needs a browser; these
// tests send no request.
vi.mock('@/utils/axios.js', () => ({ default: {} }));

import { preflightPath, readPreflightReport } from '@/builder/api.js';
import {
  PREFLIGHT_CHECKS,
  PREFLIGHT_STORAGE_KEY,
  checkLabel,
  loadPreflightChecks,
  orderedChecks,
  preflightOutcome,
  savePreflightChecks,
  statusText,
} from '@/builder/preflight.js';

// A Storage that keeps its items in a map, or throws on every call.
function memoryStorage(items = {}) {
  const map = new Map(Object.entries(items));

  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    map,
  };
}

const brokenStorage = {
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('blocked');
  },
};

describe('preflight checks', () => {
  it('lists the four checks the server makes, each with a label and a hint', () => {
    expect(PREFLIGHT_CHECKS.map((check) => check.id)).toEqual([
      'capacity',
      'network',
      'disks',
      'apps',
    ]);
    PREFLIGHT_CHECKS.forEach((check) => {
      expect(check.label).toBeTruthy();
      expect(check.hint).toMatch(/\.$/);
    });
    expect(checkLabel('disks')).toBe('Disk images');
    expect(checkLabel('later')).toBe('later');
  });

  it('orders a choice as the dialog lists the checks, each once', () => {
    expect(orderedChecks(['apps', 'capacity', 'apps', 'nope'])).toEqual([
      'capacity',
      'apps',
    ]);
    expect(orderedChecks('apps')).toEqual([]);
  });

  it('remembers the checks chosen, and none when storage fails', () => {
    const storage = memoryStorage();

    expect(loadPreflightChecks(storage)).toEqual([]);

    savePreflightChecks(['apps', 'disks'], storage);
    expect(JSON.parse(storage.map.get(PREFLIGHT_STORAGE_KEY))).toEqual({
      checks: ['disks', 'apps'],
    });
    expect(loadPreflightChecks(storage)).toEqual(['disks', 'apps']);

    expect(
      loadPreflightChecks(memoryStorage({ [PREFLIGHT_STORAGE_KEY]: '{oops' })),
    ).toEqual([]);
    expect(loadPreflightChecks(brokenStorage)).toEqual([]);
    expect(() => savePreflightChecks(['apps'], brokenStorage)).not.toThrow();
    expect(loadPreflightChecks(null)).toEqual([]);
  });

  it('words each status and what a report comes to', () => {
    expect(statusText('passed')).toBe('Passed');
    expect(statusText('failed')).toBe('Failed');
    expect(statusText('unavailable')).toBe('Unavailable');
    expect(statusText('odd')).toBe('Unavailable');

    expect(
      preflightOutcome({
        checks: [
          { status: 'passed' },
          { status: 'failed' },
          { status: 'passed' },
          { status: 'unavailable' },
        ],
      }),
    ).toBe('Preflight: 2 passed, 1 failed, 1 unavailable');
    expect(preflightOutcome({ checks: [{ status: 'unavailable' }] })).toBe(
      'Preflight: 1 unavailable',
    );
    expect(preflightOutcome({ checks: [] })).toBe(
      'Preflight: no check was made',
    );
  });
});

describe('readPreflightReport', () => {
  it('reads each check with its issues, and the checks by status', () => {
    const report = readPreflightReport({
      checks: [
        {
          name: 'disks',
          status: 'failed',
          summary: 'Found: 0 of 1 drive image.',
          issues: [
            {
              code: 'preflight.disk.missing',
              severity: 'error',
              message: 'device "web" uses drive image "x.qc2"',
              nodeId: 'n1',
              field: 'spec.hardware.drives.0.image',
            },
          ],
        },
        {
          name: 'capacity',
          status: 'unavailable',
          summary: 'Your role may not list the cluster hosts.',
          issues: [
            {
              code: 'preflight.unavailable',
              message: 'your role may not list the cluster hosts',
            },
          ],
        },
        { name: 'apps', status: 'later', issues: null },
        { status: 'passed' },
      ],
      passed: [],
    });

    expect(report.checks.map((check) => [check.name, check.status])).toEqual([
      ['disks', 'failed'],
      ['capacity', 'unavailable'],
      ['apps', 'unavailable'],
    ]);
    expect(report.checks[0].issues[0]).toMatchObject({
      code: 'preflight.disk.missing',
      severity: 'error',
      nodeId: 'n1',
      field: 'spec.hardware.drives.0.image',
    });
    expect(report.checks[1].issues[0].severity).toBe('warning');
    expect(report.checks[2]).toMatchObject({ summary: '', issues: [] });
    expect(report.passed).toEqual([]);
    expect(report.failed).toEqual(['disks']);
    expect(report.unavailable).toEqual(['capacity', 'apps']);
  });

  it('refuses an answer that is no report', () => {
    expect(() => readPreflightReport({})).toThrow(TypeError);
    expect(() => readPreflightReport(null)).toThrow(TypeError);
  });

  it('names the route of a draft', () => {
    expect(preflightPath('alice', 'd/1')).toBe(
      'builder/drafts/alice/d%2F1/preflight',
    );
  });
});
