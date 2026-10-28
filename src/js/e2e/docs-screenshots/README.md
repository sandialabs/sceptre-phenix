# Docs screenshots

`capture.spec.js` retakes the web UI screenshots in `docs/content/images`. Each
test is named after the image it writes, and it overwrites that image. It runs
only on demand, never in CI.

The pages read the sample data in `fixtures.json` instead of the server's: the
script answers the API requests named there (`"GET /hosts"` is
`GET /api/v1/hosts`) and lets every other request through. Any `phenix ui`
built with `VITE_AUTH=disabled` works, with an empty store and no minimega.
`TestDocsScreenshotFixturesMatchOpenAPI` in `src/go/web` checks each sample
response against its route's schema in `openapi.yml`, so a field renamed in the
API fails that test instead of drawing an empty column.

## Running

Start `phenix ui` on a free port (not 3000; see the root `AGENTS.md`), then:

```bash
cd src/js/e2e
npm ci
npx playwright install --with-deps chromium
E2E_BASE_URL=http://127.0.0.1:3080 npm run docs-screenshots

# one image
E2E_BASE_URL=http://127.0.0.1:3080 npm run docs-screenshots -- -g disks_table
```

`make docs-screenshots` from the repository root does the same, with
`E2E_BASE_URL` set. `DOCS_IMAGES_DIR` writes the images somewhere else, for
comparing them before replacing the committed ones.

Pages render at 1600 by 900 pixels at twice the pixel density. Review the new
images, and the alt text of each one in the docs, before committing them.

## Adding an image

Add a test named after the image file, without `.png`. Put the responses the
page needs in `fixtures.json`, or pass a variant to `open()`, as `empty_table`
does. Images of a running experiment, the console, WebShark and the State of
Health graph need live minimega data or websockets, so they are still taken by
hand.
