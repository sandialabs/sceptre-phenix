# phenix UI performance checks

Two industry-standard tools guard the web UI's load performance. CI runs both
in the `perf` job of `.github/workflows/frontend.yml`.

| Tool                                                           | Checks                                                                                                                                         | Config             |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ------------------ |
| [size-limit](https://github.com/ai/size-limit)                 | Gzipped size of the entry JS and CSS and of all JS chunks, and the size of the image assets, in `../dist`                                      | `.size-limit.cjs`  |
| [Lighthouse CI](https://github.com/GoogleChrome/lighthouse-ci) | Performance score, FCP, LCP, TBT, CLS, page weight, script size, compression and caching of static assets, for the main pages of a real server | `lighthouserc.cjs` |

## Running locally

```bash
cd src/js
npm ci
VITE_AUTH=disabled npm run build

cd perf
npm ci
npm run size          # bundle budgets; needs only the build above

# Lighthouse needs a running `phenix ui` serving that build (see ../e2e/README.md)
# and Chrome or Chromium (set CHROME_PATH if it is not found automatically);
# point E2E_BASE_URL at that server, e.g. one started with
# `phenix ui --listen-endpoint 127.0.0.1:3080`
E2E_BASE_URL=http://127.0.0.1:3080 npm run lighthouse
```

Lighthouse writes HTML and JSON reports to `lighthouse-results/`. CI uploads
them as the `lighthouse-reports` artifact.

To see the summary CI writes, save size-limit's results as JSON and run
`summary.mjs` after Lighthouse; it prints the markdown:

```bash
npm run --silent size:json > size-limit.json
node summary.mjs --size size-limit.json \
  --assert .lighthouseci/assertion-results.json \
  --manifest lighthouse-results/manifest.json
```

## CI summary

The `perf` job runs size-limit and Lighthouse even when one of them fails, then
`summary.mjs` writes one summary of both to the job summary page and to the
`perf-summary` artifact (`perf-summary.md` and `perf-summary.json`, kept 90
days). The job's last step, Check budgets, fails the job with one short error
annotation per failed budget, at most 10.

`perf-summary.json` (`schema: 1`) holds:

- `commit`, `ref`, `baseRef`, `runUrl` and `generatedAt` for the run.
- `size.rows`: each size-limit budget's `name`, `size` and `limit` in bytes,
  `passed`, and `baseSize` and `delta` against the baseline.
- `lighthouse.routes`: each route's median-run `performance` score, `fcp`,
  `lcp`, `tbt` (ms), `cls`, `totalByteWeight` and `scriptBytes`, and the
  baseline's `performance`, `lcp` and `totalByteWeight` as `base`.
- `lighthouse.failures`: the failed assertions, with `level` `error` or `warn`.
- `size.status` and `lighthouse.status`: `not-run` when a tool wrote no results.
- `baseline`: the baseline run's `commit` and `runUrl`, or null.
- `passed`: false when a bundle budget or an `error` assertion failed, or a
  tool did not run.

The baseline is the newest unexpired `perf-summary` artifact among the last 30
completed push runs on the pull request's base branch, or on the default branch
for pushes, in this repository. Bundle size deltas are exact. Lighthouse deltas are informational:
medians of 3 runs on shared runners vary from run to run, so only the absolute
budgets in `lighthouserc.cjs` fail the job. Without a baseline, for example
before the default branch has a run or after its artifact expires, deltas show
n/a. The baseline is another run's output, so `summary.mjs` reads only numbers
from it.

## Why perf/ has its own lockfile

`perf/` is a separate npm project with its own `package-lock.json`, like
`e2e/`, and stays one. Measured with npm 11.20 and `npm ci --ignore-scripts`:

| Install                                                             | Packages | `node_modules` |
| ------------------------------------------------------------------- | -------- | -------------- |
| UI alone (`src/js`)                                                 | 382      | 220 MB         |
| `perf/` alone                                                       | 335      | 158 MB         |
| Merged as an npm workspace (`workspaces: ["perf"]`), plain `npm ci` | 700      | 375 MB         |
| Merged, `npm ci -w perf`                                            | 346      | 159 MB         |
| Merged, `npm ci --workspaces=false`                                 | 384      | 223 MB         |

Merging would add 318 packages and about 155 MB (Lighthouse,
puppeteer-core, Sentry and their dependencies) to every plain `npm ci` in `src/js`, including the
container image builds, and save no repository space: the merged lock is
344 kB against 351 kB for the two today. Workspace flags can scope an install,
but every install site (the Makefile and the container builds through it, the
JIT image, the Lint and Frontend workflows, and the documented developer
setup) would need one, and a missed site pays the full cost silently. So
`perf/` keeps its own lock, `npm ci` here installs it, and Vitest and ESLint
treat `perf/` as tooling (`vite.config.js`, `eslint.config.js`).

## Changing a budget

When a change trips a budget, first check whether it can be avoided: import
only what is used, lazy-load code needed by a single page, and register heavy
Buefy components locally in the view that uses them (see `src/utils/buefy.js`).
Raise a limit only deliberately, and say why in the pull request. When you raise
a size-limit budget, set it about 5 to 10% above the measured size.
Lighthouse's page weight and script size budgets are looser, because phēnix runs
on desktops and laptops over fast links; they catch large regressions.

To see what is in a chunk, build with source maps and open the treemap:

```bash
cd src/js
VITE_AUTH=disabled npx vite build --sourcemap
npx source-map-explorer 'dist/assets/index-*.js'
```
