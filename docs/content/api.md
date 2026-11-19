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

    The raw spec is at `/docs/openapi.yml`, with `info.version` set to the
    server's build, which authenticated `/api/v1` responses also carry in an
    `X-Phenix-Version` header.

The API docs are organized by tag (`Configs`, `Experiments`, `Virtual
Machines`, `Hosts`, `Applications`, `Topologies`, `Disks`, `Users`,
`Workflow`, `Jobs`, `Events`, etc.) and document every route, including request
parameters, response schemas and status codes, including the websockets and
the routes outside `/api/v1` (health, features, version and `/webshark/`).

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
auth token generated from the `Users` page (in the `Admin` menu) of the web
UI, passed as the `X-Phenix-Auth-Token` header, which must carry the token as `Bearer <token>`:

```http
X-Phenix-Auth-Token: Bearer ******
```

A token in an `Authorization` header is ignored.

A browser link can pass the token in a `token` query parameter instead, without
`Bearer`. An API request without a token gets 403, and a request with a
malformed, expired or signed-out token gets 401. The API reference introduction
gives the full rules.

See [Generating User Authentication Tokens](user-administration.md#generating-user-authentication-tokens)
for details.

```bash
curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" http://localhost:3000/api/v1/experiments
```

### Status Codes

A role without the permission a route needs gets `403` (or `404` on a few
routes, so they don't reveal another user's job, console or capture token). An
unknown path under `/api/v1` gets `404`, and a wrong method gets `405`. With
authentication disabled, every request acts as `global-admin`. With
`--base-path`, the `Location` headers of new jobs and configs include the base
path.

## Errors

Errors are JSON with a `message`, the underlying `cause`, and a `code` (match
on the `code`, since the message may change). Some add a `metadata` or
`details` object.

| `code` | Statuses | Meaning |
|---|---|---|
| `invalid_request` | `400`, `405`, `413`, `415`, `416`, `422` | The request cannot be acted on as sent. |
| `unauthorized` | `401` | No valid session. |
| `forbidden` | `403` | The role may not make the request. |
| `not_found` | `404`, `410` | The thing does not exist, or is hidden from the role. |
| `conflict` | `409`, `412`, `423` | At odds with the current state, such as a stale revision. |
| `rate_limited` | `429` | Too many requests. `Retry-After` says when to try again. |
| `unavailable` | `502`, `503`, `504` | The server or minimega cannot serve it now. Try again later. |
| `internal` | other `5xx` | The request failed in the server. |
| `broken_disks` | `400` | A disk or CD-ROM image of a VM is missing or has a broken backing chain, so the start was refused. `details.disks` lists them. See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |

```json
{"message": "job not found", "cause": "", "code": "not_found"}
```

The exceptions are the health routes (`application/health+json`), the
`/webshark/` routes (WebShark's own format), and a failed websocket handshake
(plain text).

## Response Encoding

JSON, YAML and plain-text responses are gzip-compressed when the client sends
`Accept-Encoding: gzip`. Downloads are never compressed.

## Paging

The experiment, VM, disk, job, experiment VM and experiment file lists take
`pageNum` (from 1) and `perPage` together, and return that page with a
`total` that counts the whole list. Without both, they return everything.

## Query Parameters

Boolean and enum query parameters are strict. A boolean takes `true` or
`false` (also `1`, `t`, `0` and `f`), and an enum only the values the API docs
list. Anything else, even an empty value or a bare `?name`, gets `400`
(`invalid_request`) with the accepted values. Leave a parameter out to get its
default.

## Downloads

Single-file downloads (experiment files, disk images, files in a mounted VM,
tunneler builds, saved captures and the server's log files) can be resumed.
Each download route also answers `HEAD`, and every response carries
`Accept-Ranges: bytes`, a strong `ETag` and `Last-Modified`, so `Range`,
`If-Range` and `If-None-Match` work as usual (`206`, `200` for a changed file,
`304`).

`curl -C -` resumes with `Range` alone, so check such a download against its
checksum. Experiment files and disk images have a `checksum` route that
returns the SHA-256 (`202` until it's computed).

```bash
URL='http://localhost:3000/api/v1/experiments/my-exp/files/router_eth0.pcap'
curl -C - -o router_eth0.pcap -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "$URL?path=captures/router_eth0.pcap"
curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "$URL/checksum?path=captures/router_eth0.pcap"
```

Zip downloads that phēnix builds as it sends them, and the CSV and JSONL
downloads of a netflow capture, can't be resumed. The file of a finished
[background job](jobs.md), such as its zip of experiment files, can
(`GET /api/v1/jobs/{id}/artifact`). A reverse proxy must pass `Range`,
`If-Range` and the `ETag` through unchanged and must not compress downloads,
or resuming won't work.

## Revisions and Conditional Requests

Every stored config has a `metadata.revision` that changes on each write (see
[Revisions](configuration.md#revisions)). `GET`, `POST` and `PUT` on a config
return it as a strong `ETag`, `"r<revision>"`, and as a number in
`X-Phenix-Revision`. Send the ETag back in `If-Match` on a `PUT` or `DELETE`
to write only if nobody changed the config since you read it; otherwise you
get `412` with the current revision in `metadata.revision`. Without
`If-Match`, a write replaces the config whatever its revision.
`If-None-Match` on a `GET` answers `304` while the config is unchanged.

```bash
curl -si -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  http://localhost:3000/api/v1/configs/topology/demo | grep -i '^etag'
# ETag: "r42"

curl -s -X PUT -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  -H 'Content-Type: application/x-yaml' -H 'If-Match: "r42"' \
  --data-binary @demo.yaml http://localhost:3000/api/v1/configs/topology/demo
# 204 with ETag: "r43", or 412 if someone saved the config after you read it
```

## Freshness

Every authenticated response carries `X-Phenix-Seq`, and every message on the
`/ws` websocket carries the same number in `seq`. It only increases within one
`phenix ui` process, so a client can use it to order lists and live updates.
Responses with state read from minimega also carry `X-Phenix-Observed-At`
(when it was read), and `X-Phenix-Stale: true` when it is older than normal,
such as while an experiment starts or stops.

## Streaming Endpoints

* `GET /experiments/{exp}/vms/{name}/captures/{interface}/stream` streams a
  [running packet capture](webshark.md#streaming-a-running-capture) as pcap,
  over HTTP or a websocket.
* `GET /webshark/streams/{token}`, outside `/api/v1`, streams the same pcap
  with a [capture token](webshark.md#capture-tokens) as its only credential.
* `GET /console/{pid}/ws` attaches a websocket to a
  [minimega console](minimega.md#web-ui).

The server closes a `/ws` client that falls too far behind with close code
1013 and the reason `client too slow`; reconnect and reload, as the web UI
does. When `phenix ui` [shuts down](settings.md#stopping-the-server), it
closes every websocket with close code 1001, and new websockets, consoles and
jobs get `503` with `Retry-After` until it stops.

## Background Jobs

Long operations run as [background jobs](jobs.md). The route that starts one
answers `202` with the job and a `Location: /api/v1/jobs/{id}` header to
follow. The experiment name `all` in `POST /api/v1/experiments/all/start` and
`POST /api/v1/experiments/all/trigger` starts such a job for every experiment
you may start or trigger.

Some of these routes take an `Idempotency-Key` header, so a retry returns the
first job instead of starting another (see
[Retrying a request that starts a job](jobs.md#retrying-a-request-that-starts-a-job)).

## Health Endpoints

`GET /healthz` and `GET /readyz`, at the server root and without credentials,
report the server's health. See [Health Checks](health.md).

## Integrations

The phēnix API can be used to integrate phēnix with external systems. Known
integrations include:

* [Workflow](workflow.md) - deploy a topology directory with
  `phenix workflow apply`, or drive experiment topology/scenario updates from
  a git-based CI/CD pipeline reacting to push events.
* [Builder](builder/details/administration.md#api) - create, edit and publish
  drafts, convert legacy diagrams, and manage packages, icons and templates
  under `/api/v1/builder/`.

## See Also

* [phenix admin](cli/admin.md): create users, roles and API tokens.
