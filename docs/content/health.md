# Health Checks

A running `phenix ui` server answers two health endpoints, following the
Kubernetes `livez`/`readyz` convention:

* **`/healthz` (liveness)**: is the phēnix process up and working? It checks
  the server's uptime and that the store can be read. A failing liveness
  check means restarting phēnix may help, so the Docker and Podman health
  checks run it.
* **`/readyz` (readiness)**: can phēnix run experiments right now? It checks
  minimega, the mesh, the store, `qemu-img`, WebShark, free disk space and
  the websocket broker. A failing readiness check usually means something
  phēnix depends on is down, which restarting phēnix will not fix.

Both answer `GET` and `HEAD`, need no credentials, and are served from the
server root rather than under `/api/v1`:

```text
http://<phenix-host>:3000/healthz
http://<phenix-host>:3000/readyz
```

Behind a reverse proxy that serves phēnix under a base path (`--base-path`),
they are at `<base-path>/healthz` and `<base-path>/readyz`. A proxy that
requires its own sign-in on every path must exempt them, or monitors must
probe the phēnix port directly.

!!! note
    Health is available through the endpoints and
    [`phenix healthcheck`](#the-phenix-healthcheck-command), not the web UI.

## Status Codes and Format

| Status | HTTP code | Meaning |
| :--- | :--- | :--- |
| `pass` | `200` | Every check passed. |
| `warn` | `200` | Something needs a look, but phēnix works. |
| `fail` | `503` | At least one check failed. |

The body uses the `application/health+json` format of the IETF draft
[Health Check Response Format for HTTP APIs](https://datatracker.ietf.org/doc/html/draft-inadarei-api-health-check-06).
The top-level `status` is the worst status of any check, and `checks` holds
the results keyed `component:measurement`. A trimmed `/readyz` answer:

```json
{
  "status": "warn",
  "version": "v1.2.0",
  "releaseId": "059cd25b",
  "serviceId": "phenix",
  "description": "phēnix readiness",
  "checks": {
    "minimega:responseTime": [
      {
        "componentId": "minimega",
        "componentType": "component",
        "observedValue": 1.27,
        "observedUnit": "ms",
        "status": "pass",
        "time": "2026-10-13T18:00:00Z"
      }
    ],
    "disk:utilization": [
      {
        "componentId": "files-dir",
        "componentType": "system",
        "observedValue": 91.4,
        "observedUnit": "percent",
        "status": "warn",
        "time": "2026-10-13T18:00:00Z",
        "output": "/phenix/images: 41234567168 bytes free"
      }
    ]
  }
}
```

A check that could not observe a value, such as when minimega is down, leaves
`observedValue` out.

## Checks

The **Check** column is the name `?exclude=` takes.

| Key | Check | Endpoints | Warns when | Fails when |
| :--- | :--- | :--- | :--- | :--- |
| `uptime` | `uptime` | both | never | never |
| `store:responseTime` | `store` | both | listing configs takes over a second | the store cannot be read |
| `minimega:responseTime`, `minimega:version`, `minimega:namespaces` | `minimega` | `/readyz` | minimega takes over a second to answer, or the namespaces cannot be listed | minimega does not answer |
| `mesh:nodes` | `minimega` | `/readyz` | the mesh has fewer nodes than it has had since phēnix started | minimega does not answer |
| `qemu-img:version` | `qemu-img` | `/readyz` | `qemu-img` is missing or broken | never |
| `webshark:files` | `webshark` | `/readyz`, only when WebShark is installed | the WebShark build is gone | never |
| `disk:utilization` | `disk` | `/readyz` | a filesystem is at least 90% full | a filesystem is at least 98% full |
| `websocket:clients`, `websocket:queue` | `websocket` | `/readyz` | the broadcast queue is at least 75% full | never |
| `go:goroutines`, `go:heapObjectsBytes`, `go:gcPauseP99` | `go` | `/readyz` | never | never |

`disk:utilization` has one result per filesystem: the minimega files
directory (`files-dir`), the phēnix base directory (`phenix-base`) and the
directory holding the store (`store`).

`?exclude=` leaves checks out, by the names above. Repeat it or separate the
names with commas, for example to ignore disks phēnix does not own:

```bash
curl -s 'http://localhost:3000/readyz?exclude=disk&exclude=qemu-img' | jq .status
```

## Caching and Timeouts

Each check reuses its last result for a few seconds and has its own timeout,
so probing often is cheap and a hung dependency cannot hold up a probe. A
check that times out is reported as failed until it answers again.

## Who Sees What

Anyone who can reach the server sees each check's status, observed value,
version and timing. The `output` fields, which name paths, hosts and errors,
are only in answers to authenticated requests: pass a token in the
`X-Phenix-Auth-Token: Bearer <token>` header (see
[Authentication](api.md#authentication)). With authentication disabled,
every request sees `output`.

!!! warning
    A probe that cannot set a header can pass the token as `?token=<token>`.
    Use this only as a last resort: proxies, access logs and monitoring tools
    often record the full URL, token included.

## The `phenix healthcheck` Command

`phenix healthcheck` asks the local server for its health and exits `0` when
it answers `200`, and `1` otherwise:

```bash
phenix healthcheck                 # liveness, prints e.g. "pass (200)"
phenix healthcheck --ready         # readiness, listing each check that is not passing
phenix healthcheck --ready -o json # the whole response body
phenix healthcheck --url https://phenix.example.com/readyz --timeout 10s
```

It builds the URL from the `ui.listen-endpoint`, `ui.tls-key` and
`ui.tls-cert` settings, so a value given only as a flag on the `phenix ui`
command line is not visible to it. Pass `--url` then, or set the matching
`PHENIX_UI_*` environment variables. It opens no store, socket or log file,
so it is cheap to run often.

## Docker and Podman

The `phenix` service in `docker/docker-compose.yml` runs `phenix healthcheck`
as its health check every 30 seconds. It starts only once the `minimega`
service is healthy, so `phenix ui` never starts without minimega.
`podman/pod.yml` gives the phenix container the same check as a liveness
probe.

```bash
docker compose ps                              # the STATUS column shows (healthy)
docker inspect --format '{{.State.Health.Status}}' phenix
docker exec phenix phenix healthcheck --ready  # minimega, store, disks, ...
```

The images themselves set no `HEALTHCHECK`, since most commands run from
them serve no UI. A container you start yourself to run `phenix ui` needs the
check added, for example in a compose file:

```yaml
services:
  phenix:
    image: ghcr.io/sandialabs/sceptre-phenix/phenix:main
    command: ["phenix", "ui"]
    healthcheck:
      test: ["CMD", "phenix", "healthcheck"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s
```

## Monitoring Examples

The examples use `PHENIX_TOKEN` for a token from the `Users` page. Leave the
header out to monitor without `output` details.

### Elastic Heartbeat

```yaml
heartbeat.monitors:
  - type: http
    id: phenix-liveness
    name: phēnix liveness
    schedule: '@every 30s'
    urls: ["http://phenix.example.com:3000/healthz"]
    check.response.status: [200]

  - type: http
    id: phenix-readiness
    name: phēnix readiness
    schedule: '@every 30s'
    urls: ["http://phenix.example.com:3000/readyz"]
    response.include_body: on_error
    check.request.headers:
      X-Phenix-Auth-Token: "Bearer ${PHENIX_TOKEN}"
    check.response:
      status: [200]
      json:
        - description: phēnix is not failing
          expression: 'status != "fail"'
```

Heartbeat releases without `expression` take a condition instead:
`condition: {not: {equals: {status: fail}}}`.

### Kubernetes and Podman Probes

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 3000
  initialDelaySeconds: 60
  periodSeconds: 30
  timeoutSeconds: 10
readinessProbe:
  httpGet:
    path: /readyz
    port: 3000
  periodSeconds: 30
  timeoutSeconds: 10
```

With TLS, add `scheme: HTTPS`. To probe from inside the container instead,
use `exec: {command: [phenix, healthcheck]}`, as `podman/pod.yml` does.
