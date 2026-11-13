# Docs screenshots

`capture.spec.js` retakes the web UI screenshots in `docs/content/images`. Each
test is named after the image it writes, and it overwrites that image. It runs
only on demand, never in CI.

The pages read the sample data in `fixtures.json` instead of the server's: the
script answers the API requests named there (`"GET /hosts"` is
`GET /api/v1/hosts`) and lets every other request through. A key can end in
a status for an error answer, as in
`"POST /experiments/grid-training/start 400"`. The pages of a running
experiment get their VM list over the websocket, so the script also answers
that request, from `"GET /experiments/<name>/vms"`. The server applies the
VM search, so a test that searches gives the VMs that the search finds. Any
`phenix ui` built with `VITE_AUTH=disabled` works, with an empty store and no
minimega.
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

`login_create` and `create_new_account` show the sign-in page, so they need a
`phenix ui` built with `VITE_AUTH=enabled` and started with
`--jwt-signing-key`. Run them on their own, with `E2E_BASE_URL` pointing at
that server:

```bash
E2E_BASE_URL=http://127.0.0.1:3081 npm run docs-screenshots -- -g 'login_create|create_new_account'
```

`settings_page` shows the server's own default settings, so it needs no
sample data.

`vlans_cards` and `vlans_grid` read the sample cluster in
`src/js/test/helpers/vlansFixture.json`, which the VLANs page's own tests
use. The Go tests of `api/vlan` write that file from `vlan.Merge`.

Pages render at 1600 by 900 pixels at twice the pixel density. Review the new
images, and the alt text of each one in the docs, before committing them.

## Adding an image

Add a test named after the image file, without `.png`. Put the responses the
page needs in `fixtures.json`, or pass a variant to `open()`, as `empty_table`
does. Set `DOCS_SCREENSHOTS_DEBUG=1` to log each API request and websocket
message that no sample answers. `console_tab` answers the console requests and
the console websocket in its own test. `webshark_empty` answers the features
request and `/webshark/watch` in its own test. `jobs_menu` answers
`GET /jobs` with the jobs in its own test, so that the header of the other
images shows no jobs. Check that list against the `Job` schema of
`openapi.yml` when you change it: the Go test reads only `fixtures.json`.
The VNC images
(`vnc_console_split`, `vnc_tiles`, `vnc_actions_menu` and
`vnc_forwards_panel`) use the stand-in experiment and RFB servers of
`tests/vnc-support.js`. The live console on a VM details card uses its RFB
servers through `standInConsoles`.
`vm_role_denied` signs in with the Experiment User role. Other images of
WebShark and the State of Health graph itself need live data, so they are
still taken by hand. `name_kind` shows the open list of a native drop-down,
which a headless browser does not draw, so it is also taken by hand.
