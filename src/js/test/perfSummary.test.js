// @vitest-environment node
// perf/summary.mjs: the CI perf job's summary, perf-summary artifact and
// budget gate, from size-limit and Lighthouse CI output (see perf/README.md).
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  SCHEMA,
  annotations,
  buildSummary,
  escapeCell,
  loadInputs,
  renderMarkdown,
} from '../perf/summary.mjs';

const fixtures = fileURLToPath(new URL('./fixtures/perf/', import.meta.url));
const fixture = (name) => path.join(fixtures, name);
const script = fileURLToPath(new URL('../perf/summary.mjs', import.meta.url));
const meta = { baseRef: 'main', generatedAt: '2026-10-13T00:00:00.000Z' };

function inputs({ baseline = false } = {}) {
  return loadInputs({
    sizePath: fixture('size-limit.json'),
    assertPath: fixture('assertion-results.json'),
    manifestPath: fixture('manifest.json'),
    baselinePath: baseline ? fixture('baseline.json') : undefined,
  });
}

function lighthouseFailure(level) {
  return {
    name: 'maxNumericValue',
    expected: 2000,
    actual: 2345.6,
    values: [2345.6],
    operator: '<=',
    passed: false,
    auditId: 'largest-contentful-paint',
    level,
    url: 'http://127.0.0.1:3000/experiments',
  };
}

describe('buildSummary', () => {
  it('passes when every budget passes, with no baseline', () => {
    const summary = buildSummary(inputs(), meta);
    expect(summary.passed).toBe(true);
    expect(summary.baseline).toBeNull();
    expect(summary.size.rows.map((r) => r.delta)).toEqual([null, null]);
    expect(summary.lighthouse.routes).toHaveLength(1);
    expect(summary.lighthouse.routes[0]).toMatchObject({
      path: '/experiments',
      performance: 0.97,
      lcp: 801.6,
      scriptBytes: 401234,
      base: null,
    });
    const md = renderMarkdown(summary);
    expect(md).toMatch(/^### UI performance budgets: ✅ pass$/m);
    expect(md).toContain('No baseline');
    expect(annotations(summary)).toEqual([]);
  });

  it('fails on a size row over budget and annotates it', () => {
    const data = inputs({ baseline: true });
    data.size[1] = { ...data.size[1], passed: false, size: 53210 };
    const summary = buildSummary(data, meta);
    expect(summary.passed).toBe(false);
    expect(renderMarkdown(summary)).toMatch(/❌ 1 over budget/);
    expect(annotations(summary)).toEqual([
      '::error title=Perf budget::Entry CSS: 53.2 kB > 52.0 kB (+5.1 kB vs main)',
    ]);
  });

  it('computes deltas against the baseline', () => {
    const summary = buildSummary(inputs({ baseline: true }), meta);
    expect(summary.size.rows[0]).toMatchObject({
      baseSize: 149000,
      delta: 564,
    });
    expect(summary.size.rows[1].delta).toBe(0);
    expect(summary.lighthouse.routes[0].base).toEqual({
      performance: 0.95,
      lcp: 900,
      totalByteWeight: 600000,
    });
    expect(summary.baseline).toEqual({
      commit: '0123456789abcdef0123456789abcdef01234567',
      runUrl: 'https://github.com/sandialabs/sceptre-phenix/actions/runs/123',
    });
    const md = renderMarkdown(summary);
    expect(md).toContain('| +564 B |');
    expect(md).toContain('+2 / −98 ms / +12.3 kB');
    expect(md).toContain('Baseline: main at `0123456789ab`');
  });

  it('fails and says so when Lighthouse did not run', () => {
    const summary = buildSummary(
      loadInputs({
        sizePath: fixture('size-limit.json'),
        assertPath: fixture('nonexistent.json'),
        manifestPath: fixture('nonexistent-manifest.json'),
      }),
      meta,
    );
    expect(summary.lighthouse.status).toBe('not-run');
    expect(summary.passed).toBe(false);
    const md = renderMarkdown(summary);
    expect(md).toContain('❌ Lighthouse did not run');
    expect(md).toContain('Lighthouse did not run or wrote no results');
    expect(annotations(summary)).toEqual([
      '::error title=Perf budget::Lighthouse did not run; see the Run Lighthouse CI step.',
    ]);
  });

  it('fails on error-level Lighthouse assertions only', () => {
    const warn = inputs();
    warn.assertions = [lighthouseFailure('warn')];
    const warned = buildSummary(warn, meta);
    expect(warned.passed).toBe(true);
    expect(renderMarkdown(warned)).toContain(
      '- warn: /experiments largest-contentful-paint: 2346 ms (budget &lt;= 2000 ms)',
    );

    const error = inputs();
    error.assertions = [lighthouseFailure('error')];
    const failed = buildSummary(error, meta);
    expect(failed.passed).toBe(false);
    expect(annotations(failed)).toEqual([
      '::error title=Perf budget::/experiments largest-contentful-paint: 2346 ms (budget <= 2000 ms)',
    ]);
  });

  it('ignores non-numeric and hostile baseline values', () => {
    const data = inputs({ baseline: true });
    data.baseline = {
      commit: '<script>alert(1)</script>',
      runUrl: 'javascript:alert(1)',
      size: {
        rows: [
          { name: 'Entry JS (loaded on every page)', size: '1 | 2' },
          { name: 'Entry CSS', size: '<script>' },
        ],
      },
      lighthouse: {
        routes: [
          {
            path: '/experiments',
            performance: '<img src=x>',
            lcp: null,
            totalByteWeight: 'x|y',
          },
        ],
      },
    };
    const summary = buildSummary(data, meta);
    expect(summary.size.rows.map((r) => r.delta)).toEqual([null, null]);
    expect(summary.lighthouse.routes[0].base).toEqual({
      performance: null,
      lcp: null,
      totalByteWeight: null,
    });
    expect(summary.baseline).toEqual({ commit: null, runUrl: null });
    const md = renderMarkdown(summary);
    expect(md).not.toMatch(/<script|<img|javascript:/);
    // every table row keeps its column count
    for (const line of md.split('\n').filter((l) => l.startsWith('|'))) {
      const cells = line.replace(/\\\|/g, '').split('|').length;
      expect(cells === 8 || cells === 11).toBe(true);
    }
  });

  it('escapes a | and markup in budget names', () => {
    const data = inputs();
    data.size[0] = { ...data.size[0], name: 'JS | <b>chunks</b>\nnext' };
    const md = renderMarkdown(buildSummary(data, meta));
    expect(md).toContain('| JS \\| &lt;b&gt;chunks&lt;/b&gt; next |');
    expect(escapeCell('a|b')).toBe('a\\|b');
  });

  it('limits annotations to 10', () => {
    const data = inputs();
    data.assertions = Array.from({ length: 12 }, () =>
      lighthouseFailure('error'),
    );
    const lines = annotations(buildSummary(data, meta));
    expect(lines).toHaveLength(11);
    expect(lines[10]).toBe('::error title=Perf budget::and 2 more');
  });
});

describe('loadInputs', () => {
  let dir;
  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'perf-summary-'));
  });
  afterEach(() => {
    fs.rmSync(dir, { recursive: true, force: true });
  });

  it("reads Lighthouse results only from the manifest's directory", () => {
    const results = path.join(dir, 'results');
    const outside = path.join(dir, 'outside');
    fs.mkdirSync(results);
    fs.mkdirSync(outside);
    fs.writeFileSync(path.join(results, 'lhr.json'), '{"from":"results"}');
    fs.writeFileSync(path.join(outside, 'lhr.json'), '{"from":"outside"}');
    const manifest = path.join(results, 'manifest.json');
    fs.writeFileSync(
      manifest,
      JSON.stringify([
        {
          url: 'a',
          isRepresentativeRun: true,
          jsonPath: '../outside/lhr.json',
        },
        {
          url: 'b',
          isRepresentativeRun: true,
          jsonPath: outside + '/lhr.json',
        },
      ]),
    );
    expect(loadInputs({ manifestPath: manifest }).lhrs).toEqual({
      a: { from: 'results' },
      b: { from: 'results' },
    });
  });
});

describe('summary.mjs CLI', () => {
  let dir;
  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'perf-summary-'));
  });
  afterEach(() => {
    fs.rmSync(dir, { recursive: true, force: true });
  });

  // runs the script without the runner's GITHUB_* variables, so it never
  // writes to the CI job summary
  function run(...args) {
    return spawnSync(process.execPath, [script, ...args], {
      encoding: 'utf8',
      env: { PATH: process.env.PATH },
    });
  }

  function writeSummary(summary) {
    const file = path.join(dir, 'perf-summary.json');
    fs.writeFileSync(file, JSON.stringify(summary));
    return file;
  }

  it('writes the summary files, and --check passes them', () => {
    const json = path.join(dir, 'perf-summary.json');
    const md = path.join(dir, 'perf-summary.md');
    const out = run(
      '--size',
      fixture('size-limit.json'),
      '--assert',
      fixture('assertion-results.json'),
      '--manifest',
      fixture('manifest.json'),
      '--baseline',
      fixture('baseline.json'),
      '--base-ref',
      'main',
      '--out-md',
      md,
      '--out-json',
      json,
    );
    expect(out.status).toBe(0);
    expect(out.stdout).toBe('');
    const summary = JSON.parse(fs.readFileSync(json, 'utf8'));
    expect(summary).toMatchObject({ schema: SCHEMA, passed: true });
    expect(summary.size.rows[0].delta).toBe(564);
    expect(fs.readFileSync(md, 'utf8')).toMatch(/✅ pass/);

    const check = run('--check', json);
    expect(check.status).toBe(0);
    expect(check.stdout).toBe('All UI performance budgets passed.\n');
  });

  it('--check fails a summary over budget and annotates it', () => {
    const data = inputs({ baseline: true });
    data.size[1] = { ...data.size[1], passed: false, size: 53210 };
    const out = run('--check', writeSummary(buildSummary(data, meta)));
    expect(out.status).toBe(1);
    expect(out.stdout).toBe(
      '::error title=Perf budget::Entry CSS: 53.2 kB > 52.0 kB (+5.1 kB vs main)\n',
    );
  });

  it('--check fails a missing, unreadable or other-schema summary', () => {
    const passing = buildSummary(inputs(), meta);
    const missing = path.join(dir, 'missing.json');
    const garbled = path.join(dir, 'garbled.json');
    fs.writeFileSync(garbled, '{');
    const otherSchema = writeSummary({ ...passing, schema: SCHEMA + 1 });
    for (const file of [missing, garbled, otherSchema]) {
      const out = run('--check', file);
      expect(out.status).toBe(1);
      expect(out.stdout).toContain(
        `::error title=Perf budget::No perf summary in ${file}`,
      );
    }
  });

  it('rejects an argument that is not a flag', () => {
    const out = run('perf-summary.json');
    expect(out.status).not.toBe(0);
    expect(out.stderr).toContain('unexpected argument: perf-summary.json');
  });
});
