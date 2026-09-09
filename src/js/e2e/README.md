# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec | Needs | CI |
|---|---|---|
| `routes.spec.js` | just a running server (empty store is fine) | yes |
| `forms.spec.js` | just a running server | yes |
| `builder.spec.js` | server started with `--features builder-beta` | yes |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology | opt-in (`E2E_LIFECYCLE=1`) |
| `auth-enabled.spec.js` | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key` | opt-in (`E2E_AUTH_MODE=enabled`) |
| `auth-proxy.spec.js` | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt` | opt-in (`E2E_AUTH_MODE=proxy`) |

CI (`.github/workflows/frontend.yml`) builds the UI with `VITE_AUTH=disabled`,
starts `bin/phenix ui --features builder-beta` against a throw-away store, and
runs the default set in Google Chrome and Firefox. Builder checks include axe
accessibility scans.

## Running locally

```bash
cd src/js/e2e
npm ci
npx playwright install --with-deps chrome firefox

# default set against a server on :3000 (override with E2E_BASE_URL)
npx playwright test

# Builder suite in both supported browsers
npx playwright test builder.spec.js \
  --project=google-chrome --project=firefox

# full experiment lifecycle on a real deployment (topology defaults to helloworld)
E2E_LIFECYCLE=1 E2E_TOPOLOGY=helloworld npx playwright test experiment-lifecycle
```

The auth-mode specs need the UI rebuilt with the matching `VITE_AUTH` value and
the server restarted with the matching `--jwt-signing-key`; see the header
comment in each spec. The proxy spec simulates the auth proxy with Playwright's
`extraHTTPHeaders` — no external proxy required.

Failure artifacts (screenshots, traces) land in `test-results/`; open traces
with `npx playwright show-trace <trace.zip>`.
