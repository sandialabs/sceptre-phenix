# API

The phēnix web UI is built entirely on top of a REST API served by the phēnix
daemon. That same API is available for external tooling and automation, such
as CI/CD pipelines (see [Workflow](workflow.md)) or custom scripts.

## Interactive API Docs (Swagger/OpenAPI)

Browsable, interactive API documentation is generated from the project's
[OpenAPI](https://www.openapis.org/) (Swagger) specification, and is
available in two places:

* **On this documentation site**: [swagger.html](/swagger.html) (linked from
  the gear icon in the footer) is a static snapshot of the API docs, rendered
  from the `openapi.yml` spec at the time the docs were published.
* **On a running phēnix server**: every phēnix instance also hosts its own
  copy of the same interactive API docs at the `/docs/` path:

    ```text
    http://<phenix-host>:<port>/docs/
    ```

    For a default local installation, this would be:

    ```text
    http://localhost:3000/docs/
    ```

    !!! note
        `<port>` defaults to `3000` and comes from the `ui.listen-endpoint`
        setting. See [Settings](settings.md) for details on configuring this.

    Because it's built into the phēnix binary/image itself, `/docs/` on a
    running server is always in sync with the version of phēnix you're
    running, whereas [swagger.html](/swagger.html) reflects the spec as of
    the latest docs release.

    The server also serves the spec itself at `/docs/openapi.yml`, with
    `info.version` set to its build: a release's version without its `v`
    (such as `1.0.0`), or `0.0.0-dev+<commit>` for any other build. Every
    `/api/v1` response past authentication names the same build in its
    `X-Phenix-Version` header, so a client generated from the spec can tell
    when the server runs a different version. A request that authentication
    refuses, and a `404` or `405` for a path or method the server does not
    serve, get no such header.

The API docs are organized by tag (`Configs`, `Experiments`, `Virtual
Machines`, `Hosts`, `Applications`, `Topologies`, `Disks`, `Users`,
`Workflow`, `Jobs`, `Events`, etc.) and document every route, including request
parameters, response schemas and status codes. That includes the websockets
(`/ws` and the netflow, VNC, console, port forward and SCORCH streams), each
described as a `GET` that answers `101` with its message protocol, and the
routes outside `/api/v1`: the health, features, version and tunneler download
routes, and the `/webshark/` routes that WebShark itself calls and that stream
a capture by token, which take a capture token instead of a sign-in token. Only
the web UI's own files (scripts, styles, images and the
documentation site) are left out.

!!! info
    The underlying OpenAPI spec is maintained at
    [`src/go/web/public/docs/openapi.yml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/web/public/docs/openapi.yml)
    in the [sceptre-phenix](https://github.com/sandialabs/sceptre-phenix)
    repository, and is rendered to static HTML with
    [Redoc](https://github.com/Redocly/redoc) as part of the phēnix build:
    container images, Debian packages, and binaries built with `make build`
    all use the Redocly CLI version pinned in `scripts/api-docs.sh`. Run
    `make api-docs` to render it locally.

## Authentication

If UI/API authentication is enabled (see
[User Authn/Authz](user-administration.md)), API requests must include an
auth token generated from the `Users` tab in the web UI, passed as the
`X-phenix-auth-token` header:

```http
X-phenix-auth-token: ******
```

See [Generating User Authentication Tokens](user-administration.md#generating-user-authentication-tokens)
for details.

An `OPTIONS` request to an `/api/v1` route answers `204` before
authentication, without running the route.

## Paging

The experiment, VM, disk, job, experiment VM, and experiment file lists
(`GET /experiments`, `/vms`, `/disks`, `/jobs`, `/experiments/{name}/vms` and
`/experiments/{name}/files`) take `pageNum` (from 1) and `perPage` together
and return only that page. `GET /vms`, `/disks`, `/jobs` and
`/experiments/{name}/vms` also return a `total` that counts the whole list.
Without both, they return everything; a value that is not a whole number of at
least 1 answers `400`. A page of experiments asks minimega only about the
experiments on it, and a page of `GET /vms?screenshot=` takes screenshots only
of its own VMs.

## Streaming Endpoints

Three endpoints stream rather than return a document, and are easiest to use from
the command line:

* `GET /experiments/{exp}/vms/{name}/captures/{interface}/stream` streams a
  running packet capture as a pcap file, over plain HTTP or as websocket binary
  messages. See [WebShark](webshark.md#streaming-a-running-capture) for worked
  examples, or use `phenix vm capture stream`.
* `GET /webshark/streams/{token}`, outside `/api/v1`, streams the same pcap
  with a short-lived stream token from
  `POST /experiments/{exp}/captureTokens` as its only credential, so a command
  run elsewhere need not carry a sign-in token. See
  [Capture Tokens](webshark.md#capture-tokens).
* `GET /console/{pid}/ws` attaches a websocket to a minimega console the server
  is running. See [minimega](minimega.md#from-the-web-ui).

### Slow clients

The web UI's websocket, `GET /ws`, queues up to 256 messages for each client.
The server closes a client that falls that far behind with close code 1013
(Try Again Later) and the reason `client too slow`, so one stalled browser tab
cannot hold up the others; the web UI reconnects and reloads what it shows. A
client of its own should do the same. Under heavy load the live log stream
skips lines rather than wait, and sends a `WARN` entry saying how many it
skipped; `GET /logs` and the log file still have them.

### Server shutdown

When `phenix ui` shuts down (see
[Stopping the Server](settings.md#stopping-the-server)), it closes every
websocket with close code 1001 (Going Away), and sends that code at no other
time. `GET /ws` and the capture stream and netflow websockets carry the reason
`server shutting down`; the console, SCORCH, VNC and port forward websockets
carry no reason. Until the server stops, a new websocket, starting a console
and starting a job answer `503 Service Unavailable` with `Retry-After`.

The capture stream above, and WebShark's live update stream, end when one
write to the client takes longer than 10 seconds, letting go of the capture.
The server also waits at most 10 seconds for a request's headers; request
bodies and responses are not time-limited.

## Background Jobs

Long operations run as [background jobs](jobs.md), and
`GET /api/v1/jobs/{id}` follows one to the end. `GET /api/v1/jobs` lists your
jobs, `POST /api/v1/jobs/{id}/cancel` cancels one, `DELETE /api/v1/jobs/{id}`
dismisses a finished one, and `GET /api/v1/jobs/{id}/artifact` downloads the
file it produced. A download can continue from where it stopped. Another
user's job answers `404`, as if it did not exist.

## Health Endpoints

`GET /healthz` (liveness) and `GET /readyz` (readiness) report the server's
health in the `application/health+json` format, answering `503` when it
fails. Like `/features`, they are served from the server root, outside
`/api/v1`, and need no credentials; only their `output` details need a token.
See [Health Checks](health.md).

## Integrations

The phēnix API can be used to integrate phēnix with external systems. Known
integrations include:

* [Workflow](workflow.md) - deploy a topology directory with
  `phenix workflow apply`, or drive experiment topology/scenario updates from
  a git-based CI/CD pipeline reacting to push events.
