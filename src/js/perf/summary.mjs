// Summarizes the perf job's results: size-limit's JSON output and Lighthouse
// CI's assertion results and report manifest become perf-summary.json and
// perf-summary.md, with deltas against a baseline perf-summary.json from the
// default branch. See README.md ("CI summary").
//
//   node summary.mjs --size size-limit.json \
//     --assert .lighthouseci/assertion-results.json \
//     --manifest lighthouse-results/manifest.json \
//     [--baseline baseline/perf-summary.json] [--base-ref main] \
//     --out-md perf-summary.md --out-json perf-summary.json
//   node summary.mjs --check perf-summary.json
//
// The first form always exits 0 and appends the markdown to
// $GITHUB_STEP_SUMMARY when set. The second prints one GitHub error
// annotation per failed budget and exits 1 unless every budget passed.
//
// Node built-ins only, so it runs without `npm ci`.
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export const SCHEMA = 1;
const MAX_ANNOTATIONS = 10;

// the Lighthouse audits each route row reports, by summary field
const METRIC_AUDITS = {
  fcp: 'first-contentful-paint',
  lcp: 'largest-contentful-paint',
  tbt: 'total-blocking-time',
  cls: 'cumulative-layout-shift',
  totalByteWeight: 'total-byte-weight',
};

function readJSON(file) {
  if (!file || !fs.existsSync(file)) {
    return null;
  }
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

// Reads the inputs; a missing or unreadable file becomes null. Only the
// representative (median) run of each URL is loaded, from the manifest's
// directory, whatever directory the manifest names.
export function loadInputs({
  sizePath,
  assertPath,
  manifestPath,
  baselinePath,
}) {
  const manifest = readJSON(manifestPath);
  let lhrs = null;
  if (Array.isArray(manifest)) {
    lhrs = {};
    for (const entry of manifest) {
      if (!entry || !entry.isRepresentativeRun || !entry.jsonPath) {
        continue;
      }
      const file = path.join(
        path.dirname(manifestPath),
        path.basename(String(entry.jsonPath)),
      );
      lhrs[entry.url] = readJSON(file);
    }
  }
  return {
    size: readJSON(sizePath),
    assertions: readJSON(assertPath),
    lhrs,
    baseline: readJSON(baselinePath),
  };
}

// The baseline is another run's artifact: trust only finite numbers from it.
function num(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function routePath(url) {
  try {
    return new URL(url).pathname;
  } catch {
    return String(url);
  }
}

function diff(value, base) {
  return value === null || base === null ? null : value - base;
}

function baselineIndex(baseline) {
  const size = new Map();
  const routes = new Map();
  if (!baseline || typeof baseline !== 'object') {
    return { size, routes, meta: null };
  }
  for (const row of baseline.size?.rows ?? []) {
    if (row && typeof row.name === 'string' && num(row.size) !== null) {
      size.set(row.name, row.size);
    }
  }
  for (const route of baseline.lighthouse?.routes ?? []) {
    if (route && typeof route.path === 'string') {
      routes.set(route.path, {
        performance: num(route.performance),
        lcp: num(route.lcp),
        totalByteWeight: num(route.totalByteWeight),
      });
    }
  }
  const commit =
    typeof baseline.commit === 'string' &&
    /^[0-9a-f]{7,40}$/.test(baseline.commit)
      ? baseline.commit
      : null;
  const runUrl =
    typeof baseline.runUrl === 'string' &&
    /^https:\/\/[\w.-]+\/[\w./-]+$/.test(baseline.runUrl)
      ? baseline.runUrl
      : null;
  return { size, routes, meta: { commit, runUrl } };
}

function routeMetrics(lhr) {
  const audits = lhr?.audits ?? {};
  const metrics = {
    performance: num(lhr?.categories?.performance?.score),
  };
  for (const [field, id] of Object.entries(METRIC_AUDITS)) {
    metrics[field] = num(audits[id]?.numericValue);
  }
  const script = (audits['resource-summary']?.details?.items ?? []).find(
    (item) => item.resourceType === 'script',
  );
  metrics.scriptBytes = num(script?.transferSize ?? script?.size);
  return metrics;
}

// Builds the summary object written to perf-summary.json. `meta` holds
// commit, ref, runUrl, baseRef and generatedAt.
export function buildSummary(inputs, meta = {}) {
  const base = baselineIndex(inputs.baseline);

  const size = { status: 'not-run', rows: [] };
  if (Array.isArray(inputs.size)) {
    size.status = 'ok';
    size.rows = inputs.size.map((row) => {
      const value = num(row.size);
      const baseSize = base.size.get(row.name) ?? null;
      return {
        name: String(row.name),
        size: value,
        limit: num(row.sizeLimit),
        passed: row.passed === true,
        baseSize,
        delta: diff(value, baseSize),
      };
    });
  }

  const lighthouse = { status: 'not-run', routes: [], failures: [] };
  if (Array.isArray(inputs.assertions) && inputs.lhrs) {
    lighthouse.status = 'ok';
    for (const [url, lhr] of Object.entries(inputs.lhrs)) {
      const routeBase = base.routes.get(routePath(url)) ?? null;
      lighthouse.routes.push({
        path: routePath(url),
        ...routeMetrics(lhr),
        base: routeBase,
      });
    }
    lighthouse.failures = inputs.assertions
      .filter((a) => a && a.passed === false)
      .map((a) => ({
        path: routePath(a.url),
        auditId: String(
          a.auditProperty ? `${a.auditId}:${a.auditProperty}` : a.auditId,
        ),
        operator: String(a.operator ?? ''),
        expected: num(a.expected),
        actual: num(a.actual),
        level: a.level === 'warn' ? 'warn' : 'error',
      }));
  }

  const passed =
    size.status === 'ok' &&
    lighthouse.status === 'ok' &&
    size.rows.every((row) => row.passed) &&
    lighthouse.failures.every((f) => f.level !== 'error');

  return {
    schema: SCHEMA,
    commit: meta.commit ?? null,
    ref: meta.ref ?? null,
    baseRef: meta.baseRef ?? null,
    runUrl: meta.runUrl ?? null,
    generatedAt: meta.generatedAt ?? null,
    size,
    lighthouse,
    baseline: base.meta,
    passed,
  };
}

// Escapes text for a markdown table cell or list item: no HTML, no column
// breaks, no line breaks.
export function escapeCell(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\|/g, '\\|')
    .replace(/[\r\n]+/g, ' ');
}

function formatBytes(bytes) {
  if (bytes === null) {
    return 'n/a';
  }
  const abs = Math.abs(bytes);
  return abs < 1000 ? `${bytes} B` : `${(bytes / 1000).toFixed(1)} kB`;
}

function signed(value, format) {
  if (value === null) {
    return 'n/a';
  }
  if (value === 0) {
    return '±0';
  }
  return value > 0 ? `+${format(value)}` : `−${format(-value)}`;
}

const ms = (v) => (v === null ? 'n/a' : `${Math.round(v)} ms`);
const score = (v) => (v === null ? 'n/a' : String(Math.round(v * 100)));
const cls = (v) => (v === null ? 'n/a' : v.toFixed(3));

function sizeFailures(summary) {
  return summary.size.rows.filter((row) => !row.passed);
}

function errorFailures(summary) {
  return summary.lighthouse.failures.filter((f) => f.level === 'error');
}

function headline(summary) {
  if (summary.passed) {
    return '✅ pass';
  }
  const parts = [];
  const over = sizeFailures(summary).length + errorFailures(summary).length;
  if (over > 0) {
    parts.push(`${over} over budget`);
  }
  if (summary.size.status !== 'ok') {
    parts.push('bundle sizes not measured');
  }
  if (summary.lighthouse.status !== 'ok') {
    parts.push('Lighthouse did not run');
  }
  return `❌ ${parts.join(', ')}`;
}

function formatValue(auditId, value) {
  if (value === null) {
    return 'n/a';
  }
  if (auditId === 'cumulative-layout-shift') {
    return value.toFixed(3);
  }
  if (auditId.startsWith('categories:')) {
    return String(value);
  }
  if (/byte|size/.test(auditId)) {
    return formatBytes(value);
  }
  if (/paint|blocking-time/.test(auditId)) {
    return `${Math.round(value)} ms`;
  }
  return String(value);
}

function describeFailure(f) {
  return `${f.path} ${f.auditId}: ${formatValue(f.auditId, f.actual)} (budget ${f.operator} ${formatValue(f.auditId, f.expected)})`;
}

function scoreDelta(route, base) {
  const pct = (v) => (v === null ? null : Math.round(v * 100));
  return diff(pct(route.performance), pct(base.performance));
}

export function renderMarkdown(summary) {
  const base = escapeCell(summary.baseRef || 'base');
  const out = [`### UI performance budgets: ${headline(summary)}`, ''];

  out.push('#### Bundle budgets', '');
  if (summary.size.status === 'ok') {
    out.push(
      `| | Name | Size | Budget | Headroom | Δ vs ${base} |`,
      '|---|---|--:|--:|--:|--:|',
    );
    for (const row of summary.size.rows) {
      const headroom =
        row.size === null || row.limit === null ? null : row.limit - row.size;
      const pct =
        headroom === null || !row.limit
          ? ''
          : ` (${((headroom / row.limit) * 100).toFixed(1)}%)`;
      out.push(
        `| ${row.passed ? '✅' : '❌'} | ${escapeCell(row.name)} | ${formatBytes(row.size)} | ${formatBytes(row.limit)} | ${formatBytes(headroom)}${pct} | ${signed(row.delta, formatBytes)} |`,
      );
    }
  } else {
    out.push('size-limit did not run or wrote no results.');
  }
  out.push('');

  out.push(
    '#### Lighthouse (median of 3 runs)',
    '',
    `Deltas against ${base} are informational: Lighthouse timings vary between runners, so only the absolute budgets fail the job.`,
    '',
  );
  if (summary.lighthouse.status === 'ok') {
    out.push(
      `| Route | Score | FCP | LCP | TBT | CLS | Page bytes | Script bytes | Δ score / LCP / page bytes vs ${base} |`,
      '|---|--:|--:|--:|--:|--:|--:|--:|--:|',
    );
    for (const r of summary.lighthouse.routes) {
      const b = r.base;
      const delta = b
        ? [
            signed(scoreDelta(r, b), String),
            signed(diff(r.lcp, b.lcp), (v) => `${Math.round(v)} ms`),
            signed(diff(r.totalByteWeight, b.totalByteWeight), formatBytes),
          ].join(' / ')
        : 'n/a';
      out.push(
        `| ${escapeCell(r.path)} | ${score(r.performance)} | ${ms(r.fcp)} | ${ms(r.lcp)} | ${ms(r.tbt)} | ${cls(r.cls)} | ${formatBytes(r.totalByteWeight)} | ${formatBytes(r.scriptBytes)} | ${delta} |`,
      );
    }
  } else {
    out.push(
      'Lighthouse did not run or wrote no results; see the Run Lighthouse CI step.',
    );
  }
  out.push('');

  if (summary.lighthouse.failures.length > 0) {
    out.push('#### Failing assertions', '');
    for (const f of summary.lighthouse.failures) {
      out.push(`- ${f.level}: ${escapeCell(describeFailure(f))}`);
    }
    out.push('');
  }

  const b = summary.baseline;
  if (b && (b.commit || b.runUrl)) {
    const commit = b.commit ? `\`${b.commit.slice(0, 12)}\`` : 'unknown commit';
    const link = b.runUrl ? ` ([run](${b.runUrl}))` : '';
    out.push(`Baseline: ${base} at ${commit}${link}.`);
  } else {
    out.push(
      `No baseline: no unexpired \`perf-summary\` artifact from ${base} was found, so deltas show n/a.`,
    );
  }
  return out.join('\n') + '\n';
}

// Escapes a GitHub workflow command's message.
function escapeCommand(text) {
  return String(text)
    .replace(/%/g, '%25')
    .replace(/\r/g, '%0D')
    .replace(/\n/g, '%0A');
}

// The error annotations for a failed summary, at most MAX_ANNOTATIONS.
export function annotations(summary) {
  const base = summary.baseRef || 'base';
  const messages = [];
  if (summary.size.status !== 'ok') {
    messages.push('Bundle sizes were not measured; see the size-limit step.');
  }
  if (summary.lighthouse.status !== 'ok') {
    messages.push('Lighthouse did not run; see the Run Lighthouse CI step.');
  }
  for (const row of sizeFailures(summary)) {
    const delta =
      row.delta === null
        ? ''
        : ` (${signed(row.delta, formatBytes)} vs ${base})`;
    messages.push(
      `${row.name}: ${formatBytes(row.size)} > ${formatBytes(row.limit)}${delta}`,
    );
  }
  for (const f of errorFailures(summary)) {
    messages.push(describeFailure(f));
  }
  const shown = messages.slice(0, MAX_ANNOTATIONS);
  if (messages.length > shown.length) {
    shown.push(`and ${messages.length - shown.length} more`);
  }
  return shown.map((m) => `::error title=Perf budget::${escapeCommand(m)}`);
}

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 2) {
    const key = argv[i];
    if (!key.startsWith('--')) {
      throw new Error(`unexpected argument: ${key}`);
    }
    args[key.slice(2)] = argv[i + 1];
  }
  return args;
}

// --check: prints the annotations for a perf-summary.json and returns the
// exit code.
function check(file) {
  const summary = readJSON(file);
  let lines;
  if (!summary || summary.schema !== SCHEMA) {
    lines = [
      `::error title=Perf budget::${escapeCommand(`No perf summary in ${file}; see the summary step.`)}`,
    ];
  } else if (summary.passed) {
    lines = ['All UI performance budgets passed.'];
  } else {
    lines = annotations(summary);
  }
  process.stdout.write(lines.join('\n') + '\n');
  return summary?.schema === SCHEMA && summary.passed ? 0 : 1;
}

function main(argv, env) {
  const args = parseArgs(argv);
  if (args.check !== undefined) {
    return check(args.check);
  }

  const runUrl =
    env.GITHUB_SERVER_URL && env.GITHUB_REPOSITORY && env.GITHUB_RUN_ID
      ? `${env.GITHUB_SERVER_URL}/${env.GITHUB_REPOSITORY}/actions/runs/${env.GITHUB_RUN_ID}`
      : null;
  const summary = buildSummary(
    loadInputs({
      sizePath: args.size,
      assertPath: args.assert,
      manifestPath: args.manifest,
      baselinePath: args.baseline,
    }),
    {
      commit: env.GITHUB_SHA ?? null,
      ref: env.GITHUB_REF_NAME ?? null,
      baseRef: args['base-ref'] ?? null,
      runUrl,
      generatedAt: new Date().toISOString(),
    },
  );
  const markdown = renderMarkdown(summary);

  if (args['out-json']) {
    fs.writeFileSync(args['out-json'], JSON.stringify(summary, null, 2) + '\n');
  }
  if (args['out-md']) {
    fs.writeFileSync(args['out-md'], markdown);
  } else {
    process.stdout.write(markdown);
  }
  if (env.GITHUB_STEP_SUMMARY) {
    fs.appendFileSync(env.GITHUB_STEP_SUMMARY, markdown);
  }
  return 0;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  process.exitCode = main(process.argv.slice(2), process.env);
}
