# API

The phēnix web UI is built entirely on top of a REST API served by the phēnix
daemon. That same API is available for external tooling and automation, such
as CI/CD pipelines (see [Git Workflow](git-workflow.md)) or custom scripts.

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
Machines`, `Hosts`, `Applications`, `Topologies`, `Disks`, `Users`, etc.) and
document most endpoints, including request parameters and response schemas.
Some routes are not documented there yet, among them `/ws`, `/logs`, `/roles`,
`/settings`, the netflow routes, and the `/webshark/` routes that WebShark
itself calls.

!!! info
    The underlying OpenAPI spec is maintained at
    [`src/go/web/public/docs/openapi.yml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/web/public/docs/openapi.yml)
    in the [sceptre-phenix](https://github.com/sandialabs/sceptre-phenix)
    repository, and is rendered to static HTML with
    [Redoc](https://github.com/Redocly/redoc) as part of the phēnix build.

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

## Response Encoding

JSON, YAML, and plain-text responses are gzip-compressed for clients that send
`Accept-Encoding: gzip`, which makes a large list such as VMs, disks, or logs
noticeably faster to fetch over a slow link. Clients that do not advertise gzip
get the uncompressed response, so no change is needed to keep working.

## Streaming Endpoints

Two endpoints stream rather than return a document, and are easiest to use from
the command line:

* `GET /experiments/{exp}/vms/{name}/captures/{interface}/stream` streams a
  running packet capture as a pcap file, over plain HTTP or as websocket binary
  messages. See [WebShark](webshark.md#streaming-a-running-capture) for worked
  examples, or use `phenix vm capture stream`.
* `GET /console/{pid}/ws` attaches a websocket to a minimega console the server
  is running. See [minimega](minimega.md#from-the-web-ui).

## Integrations

The phēnix API can be used to integrate phēnix with external systems. Known
integrations include:

* [Git Workflow](git-workflow.md) - drive experiment topology/scenario
  updates from a git-based CI/CD pipeline (e.g. a GitLab runner) reacting to
  push events.
