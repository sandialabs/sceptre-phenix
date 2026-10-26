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

The API docs are organized by tag (`Configs`, `Experiments`, `Virtual
Machines`, `Hosts`, `Applications`, `Topologies`, `Disks`, `Users`,
`Workflow`, `Jobs`, `Events`, etc.) and document every route, including request
parameters, response schemas and status codes. That includes the websockets
(`/ws` and the netflow, VNC, console, port forward and SCORCH streams), each
described as a `GET` that answers `101` with its message protocol, and the
routes outside `/api/v1`: the health, features and version routes, the
Builder page, and the `/webshark/` routes that WebShark itself calls and that
stream a capture by token, which take a capture token instead of a sign-in
token. Only the web UI's own files (scripts, styles, images and the
documentation site) are left out.

!!! info
    The underlying OpenAPI spec is maintained at
    [`src/go/web/public/docs/openapi.yml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/web/public/docs/openapi.yml)
    in the [sceptre-phenix](https://github.com/sandialabs/sceptre-phenix)
    repository, and is rendered to static HTML with
    [Redoc](https://github.com/Redocly/redoc) as part of the phēnix build:
    container images, Debian packages, this site, and binaries built with
    `make build` all use the Redocly CLI version pinned in
    `scripts/api-docs.sh`. Run `make api-docs` to render it locally.

## Authentication

If UI/API authentication is enabled (see
[User Authn/Authz](user-administration.md)), API requests must include an
auth token generated from the `Users` tab in the web UI, passed as the
`X-Phenix-Auth-Token` header, which must carry the token as `Bearer <token>`:

```http
X-Phenix-Auth-Token: Bearer ******
```

A token in an `Authorization` header is ignored.

See [Generating User Authentication Tokens](user-administration.md#generating-user-authentication-tokens)
for details.

### Status Codes

With authentication enabled, an API request without a token is refused with
`403`, and one with a malformed, expired or signed-out token with `401`, as is
one whose user a proxy's `--proxy-auth-header` names differently than the
token. A role without the permission a route needs gets `403`, except on the
routes that answer `404` so as not to reveal another user's job, console or
capture token. An unknown path under `/api/v1` answers `404`, and a method a
path does not take `405`, before any authentication. An `OPTIONS` request
answers `204`, also before authentication, without running the route.

With `--jwt-signing-key proxy-jwt`, where the proxy in front of phēnix checks
the token, phēnix reads the token only from the `X-Phenix-Auth-Token` header,
answers `400` when it is missing or malformed, and `401` when its user is
unknown or no longer holds it. With authentication disabled, every request
acts as `global-admin`.

Behind a reverse proxy that serves phēnix under a base path (`--base-path`),
the proxy strips the base path from requests, and the `Location` headers of
new jobs and configs include it.

## Response Encoding

JSON, YAML, and plain-text responses are gzip-compressed for clients that send
`Accept-Encoding: gzip`, which makes a large list such as VMs, disks, or logs
noticeably faster to fetch over a slow link. Clients that do not advertise gzip
get the uncompressed response, so no change is needed to keep working.
Downloads (responses with `Content-Disposition: attachment`) are never
compressed.

## Downloads

Single-file downloads stream from disk and can be resumed: experiment files,
disk images, files in a mounted VM, tunneler builds, and saved captures in
WebShark. Each download route also answers `HEAD`, and every response carries
`Accept-Ranges: bytes`, a strong `ETag` and `Last-Modified`.

* `Range` asks for part of the file (`206`), such as `bytes=1048576-` for all
  but the first MiB; a range past the end of the file gives `416`.
* `If-Range` with an earlier response's `ETag` honors `Range` only while the
  file is unchanged, and sends a changed file whole (`200`). Browsers do this
  when you retry a download, so they never splice two versions of a file.
* `If-None-Match` with the `ETag` answers `304` while the file is unchanged.

`curl -C -` resumes with `Range` alone, so verify a download resumed that way
with a checksum. `GET /experiments/{name}/files/{file}/checksum?path=` and
`GET /disks/checksum?disk=` return the file's SHA-256, computed in the
background: `202` with `Retry-After` until it is known, then `200`. Downloads of
the file then also send it as `Repr-Digest` (RFC 9530), except a gzip-compressed
plain-text view.

The zip downloads of several experiment files or configs are built as they are
sent, so they cannot be resumed (`Accept-Ranges: none`).

!!! note
    A reverse proxy in front of phēnix must pass `Range`, `If-Range` and the
    `ETag` through unchanged, and must not compress downloads, for resuming to
    work.

## Revisions and Conditional Requests

Every stored config has a `metadata.revision` that changes on each write (see
[Configuration Files](configuration.md#revisions)). `GET /configs/{kind}/{name}`
returns it as a strong `ETag`, `"r<revision>"`, and as a plain number in
`X-Phenix-Revision`. `POST /configs` and `PUT /configs/{kind}/{name}` return the
new revision the same way. The ETag names the revision rather than the bytes,
so it stays strong when the response is gzip-compressed.

Send the ETag back in `If-Match` on `PUT` or `DELETE /configs/{kind}/{name}`
and the write runs only while the config is still at that revision. Otherwise
it is refused with `412 Precondition Failed`, whose `metadata.revision` holds
the current revision, so you can reload the config instead of overwriting
someone else's change. `If-Match: *` only requires the config to exist, and a
malformed `If-Match` is a `400`. `If-Match` is optional: a write without it
replaces the config whatever its revision. `If-None-Match` on a `GET` answers
`304` while the config is unchanged. A `GET` of a config that does not exist
answers `404`, whatever its headers; a `PUT` or `DELETE` of one answers `404`,
or `412` when it carries `If-Match`.

```bash
curl -si -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  http://localhost:3000/api/v1/configs/topology/demo | grep -i '^etag'
# ETag: "r42"

curl -s -X PUT -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  -H 'Content-Type: application/x-yaml' -H 'If-Match: "r42"' \
  --data-binary @demo.yaml http://localhost:3000/api/v1/configs/topology/demo
# 204 with ETag: "r43", or 412 if someone saved the config after you read it
```

`GET /configs` lists each config's `metadata.revision`, and returns
`{"configs": []}` rather than `null` when the role can see none. The
`Access-Control-Expose-Headers` of every API response include `ETag` and
`X-Phenix-Revision`, so a script on another origin can read them.

## Freshness

Every response of an API route that gets past authentication carries
`X-Phenix-Seq`, a number from one sequence that only increases within a
`phenix ui` process, taken as the request started. A `401` or `403` refusal
from authentication and an `OPTIONS` answer do not carry it.
Every message on the `/ws` websocket carries the same sequence in its `seq`
field, taken as the message was published, after the change it tells of. A
reply to a websocket request, such as an `experiment/vms` list, takes its
`seq` before it reads its data.

Use it to order what you receive: data tagged with seq S reflects every change
whose message carries a seq of S or less. Keep, for each item, the highest seq
you applied, and ignore item data tagged with a lower one. A list tagged L
leaves out items whose delete message has a seq above L, and keeps items
created with a seq above L that it lacks. Sequence numbers are only comparable
within one server process, so reset what you track when the websocket
reconnects.

Responses that serve runtime state read from minimega say how old it is.
`X-Phenix-Observed-At` is when the oldest data in the response was read (RFC
3339); compare it with the response's `Date` header rather than your own clock.
`X-Phenix-Stale: true` means the response serves data past its normal bound,
such as the VM states last read while minimega is busy starting or stopping an
experiment. `GET /hosts`, `GET /experiments`, `GET /experiments/{name}`, the
VM lists, `GET /experiments/{name}/files` and `GET /soh` send them. `GET
/experiments` also sends `X-Phenix-Stale: true` when it leaves out stored
experiments it could not read.

When minimega cannot be read, a response never reports it as empty:

* A VM list of a running experiment sets `runtime_unknown` (`vms_unknown` on an
  experiment) and lists the VMs minimega did not report as configured, with an
  empty state, instead of leaving them out.
* The state of health summary sets `vms.known` to `false`, and
  `vms_stale` with `vms_observed_at` when it shows earlier VM states.
* `GET /experiments/{name}/files` answers `502` when a cluster node's files
  could not be listed.
* `GET /experiments` and `GET /vms` answer `500` when the store cannot be read,
  and successful lists are `[]`, never `null`.

API responses are sent with `Cache-Control: no-cache, private`, unless the
endpoint sets its own.

## Streaming Endpoints

Three endpoints stream rather than return a document, and are easiest to use from
the command line:

* `GET /experiments/{exp}/vms/{name}/captures/{interface}/stream` streams a
  running packet capture as a pcap file, over plain HTTP or as websocket binary
  messages. See [WebShark](webshark.md#streaming-a-running-capture) for worked
  examples, or use `phenix vm capture stream`.
* `GET /webshark/streams/{token}`, outside `/api/v1`, streams the same pcap
  with a short-lived stream token from
  `POST /experiments/{exp}/capture-tokens` as its only credential, so a command
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

Long operations run as [background jobs](jobs.md): the route that starts one
answers `202 Accepted` with the job and a `Location: /api/v1/jobs/{id}`
header (under the base path, see [Status Codes](#status-codes)), and `GET /api/v1/jobs/{id}` follows it to the end. `GET /api/v1/jobs`
lists your jobs, `POST /api/v1/jobs/{id}/cancel` cancels one,
`DELETE /api/v1/jobs/{id}` dismisses a finished one, and
`GET /api/v1/jobs/{id}/artifact` downloads the file it produced, resumably.

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
