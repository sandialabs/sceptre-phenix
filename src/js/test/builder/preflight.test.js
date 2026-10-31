import { readFileSync } from 'node:fs';

import { describe, expect, it, vi } from 'vitest';

import {
  createBuilderApi,
  preflightPath,
  readPreflightReport,
} from '@/builder/api.js';
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

import { memoryStorage } from './fixtures.js';

// A request and the server's answer, recorded by the Go test of the route.
const exchange = JSON.parse(
  readFileSync(
    new URL(
      '../../../go/web/testdata/builder-preflight-exchange.json',
      import.meta.url,
    ),
  ),
);

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
    expect(JSON.parse(storage.getItem(PREFLIGHT_STORAGE_KEY))).toEqual({
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
  it('sends the recorded request and reads the recorded answer', async () => {
    const http = { post: vi.fn(async () => ({ data: exchange.answer })) };
    const report = await createBuilderApi(http).preflight('d1', {
      checks: exchange.request.checks,
    });

    expect(http.post).toHaveBeenCalledWith(
      preflightPath('d1'),
      exchange.request,
    );
    expect(report).toEqual(exchange.answer);
  });

  it('reads an unknown status as unavailable, and a check without a name not at all', () => {
    const report = readPreflightReport({
      checks: [
        {
          name: 'capacity',
          status: 'unavailable',
          issues: [{ message: 'your role may not list the cluster hosts' }],
        },
        { name: 'apps', status: 'later', issues: null },
        { status: 'passed' },
      ],
    });

    expect(report.checks).toEqual([
      {
        name: 'capacity',
        status: 'unavailable',
        summary: '',
        issues: [
          {
            code: '',
            severity: 'warning',
            message: 'your role may not list the cluster hosts',
          },
        ],
      },
      { name: 'apps', status: 'unavailable', summary: '', issues: [] },
    ]);
    expect(report.unavailable).toEqual(['capacity', 'apps']);
  });

  it('refuses an answer that is no report', () => {
    expect(() => readPreflightReport({})).toThrow(TypeError);
    expect(() => readPreflightReport(null)).toThrow(TypeError);
  });

  it('names the route of a draft', () => {
    expect(preflightPath('d/1')).toBe('builder/drafts/d%2F1/preflight');
  });
});
