# Health Checks

A running `phenix ui` server answers two health endpoints, following the
Kubernetes `livez`/`readyz` convention:

* **`/healthz` (liveness)**: is the phēnix process up and working? It checks
  the server's uptime and that the store can be read. A failing liveness check
  means restarting phēnix may help, so this is what the Docker and Podman
  health checks run.
* **`/readyz` (readiness)**: can phēnix run experiments right now? It checks
  minimega, the mesh, the store, `qemu-img`, WebShark, free disk space and the
  websocket broker. A failing readiness check usually means something phēnix
  depends on is down, which restarting phēnix will not fix.

Both answer `GET` and `HEAD`, need no credentials, and are served from the
server root rather than under `/api/v1`:

```text
http://<phenix-host>:3000/healthz
http://<phenix-host>:3000/readyz
```

Behind a reverse proxy that serves phēnix under a base path (`--base-path`),
they are at `<base-path>/healthz` and `<base-path>/readyz`. A proxy that
requires its own sign-in on every path must exempt them, or monitors should
probe the phēnix port directly.

!!! note
    The web UI does not show health yet. Use the endpoints, or
    [`phenix healthcheck`](#the-phenix-healthcheck-command).

For trends over time, phēnix also serves [Prometheus metrics](#prometheus-metrics).

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

Each check key keeps one JSON type for its `observedValue` across runs: a
check that could not observe a value (minimega down, the store unreadable)
leaves `observedValue` out rather than reporting a string. Fractional values,
such as milliseconds and percentages, always carry a decimal point, even when
they happen to be whole (`42.0`, not `42`). That keeps dynamically mapped
fields in tools such as Elasticsearch from conflicting, and maps fractional
values as floats from the first document, so later values are not truncated.

## Checks

The **Check** column is the name `?exclude=` takes.

| Key | Check | Endpoints | Unit | Warns when | Fails when |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `uptime` | `uptime` | both | `s` | never | never |
| `store:responseTime` | `store` | both | `ms` | listing configs takes over a second | the store cannot be read |
| `minimega:responseTime` | `minimega` | `/readyz` | `ms` | minimega takes over a second to give its version | minimega does not answer |
| `minimega:version` | `minimega` | `/readyz` | (version) | never | minimega does not answer |
| `minimega:namespaces` | `minimega` | `/readyz` | `namespaces` | the namespaces cannot be listed | minimega does not answer |
| `mesh:nodes` | `minimega` | `/readyz` | `nodes` | the mesh has fewer nodes than it has had since phēnix started | minimega does not answer |
| `qemu-img:version` | `qemu-img` | `/readyz` | (version) | `qemu-img` is missing or broken (phēnix then inspects disks through minimega) | never |
| `webshark:files` | `webshark` | `/readyz`, only when WebShark is installed | | the WebShark build is gone | never |
| `disk:utilization` | `disk` | `/readyz` | `percent` | a filesystem is at least 90% full | a filesystem is at least 98% full |
| `websocket:clients` | `websocket` | `/readyz` | `clients` | never | never |
| `websocket:queue` | `websocket` | `/readyz` | `percent` | the broker's broadcast queue is at least 75% full | never |
| `go:goroutines`, `go:heapObjectsBytes`, `go:gcPauseP99` | `go` | `/readyz` | `goroutines`, `bytes`, `ms` | never | never |

`disk:utilization` has one result per filesystem, for the minimega files
directory (`files-dir`) and the phēnix base directory (`phenix-base`); two
directories on one filesystem are reported once, as the first.

`?exclude=` leaves checks out, by the names above. Repeat it or separate the
names with commas, for example to ignore disks phēnix does not own:

```bash
curl -s 'http://localhost:3000/readyz?exclude=disk&exclude=qemu-img' | jq .status
```

## Caching and Timeouts

Each check reuses its last result for a short time and has its own timeout,
so probing often is cheap and a hung dependency cannot hold a probe:

| Check | Reused for | Timeout |
| :--- | :--- | :--- |
| `minimega` | 5 seconds | 3 seconds |
| `store` | 2 seconds | 3 seconds |
| `disk` | 10 seconds | 2 seconds |
| `webshark` | 30 seconds | 1 second |
| `qemu-img` | 1 minute | 2 seconds |
| `uptime`, `go`, `websocket` | measured on every request | |

However often and however many monitors probe, phēnix sends minimega at most
three commands (`version`, `namespace` and `mesh status`) every 5 seconds, each
on a connection of its own, so probes never wait behind experiment work. A
check that times out is reported as failed (`timed out after 3s`) until it
finally answers.

## Who Sees What

Anyone who can reach the server sees each check's status, observed value,
version and timing. The `output` fields, which name paths, hosts and errors,
are only in answers to authenticated requests: with auth enabled, pass a token
in the `X-Phenix-Auth-Token: Bearer <token>` header or as `?token=<token>`
(see [API Authentication](api.md#authentication)). Without one, the answer
carries the note `output fields omitted: request not authenticated`. With auth
disabled, every request sees `output`. Probes without a token are not logged;
an invalid token is logged as the API logs it.

## The `phenix healthcheck` Command

`phenix healthcheck` asks the local server for its health and exits `0` when
it answers `200`, and `1` otherwise (including when nothing answers):

```bash
phenix healthcheck                 # liveness, prints e.g. "pass (200)"
phenix healthcheck --ready         # readiness, listing each check that is not passing
phenix healthcheck --ready -o json # the whole response body
phenix healthcheck --url https://phenix.example.com/readyz --timeout 10s
```

It builds the URL from the `ui.listen-endpoint`, `ui.tls-key` and `ui.tls-cert`
settings (the config file or `PHENIX_UI_*` environment variables), probing
`127.0.0.1` (or `::1`) when the server listens on every address, and `https`
when TLS is configured; it does not verify a loopback server's certificate. A
`--listen-endpoint`, `--tls-key` or `--tls-cert` given only on the `phenix ui`
command line is not visible to it, so pass `--url` or set
`PHENIX_UI_LISTEN_ENDPOINT`, `PHENIX_UI_TLS_KEY` and `PHENIX_UI_TLS_CERT`
then. Unlike other
subcommands it opens no store, unix socket or log file, so it is cheap to run
often.

## Docker and Podman

The phēnix images run `phenix healthcheck` as their `HEALTHCHECK` every 30
seconds (10 second timeout, 3 retries, after a 60 second start period), and so
does the `phenix` service in `docker/docker-compose.yml`:

```bash
docker compose ps                              # the STATUS column shows (healthy)
docker inspect --format '{{.State.Health.Status}}' phenix
docker exec phenix phenix healthcheck --ready  # minimega, store, disks, ...
```

The image is also used for one-off commands, which run no UI server and would
turn unhealthy. Start those without the health check:

```bash
docker run --rm --no-healthcheck ghcr.io/sandialabs/sceptre-phenix/phenix:main phenix version
```

or, in a compose file:

```yaml
services:
  phenix-cli:
    image: ghcr.io/sandialabs/sceptre-phenix/phenix:main
    healthcheck:
      disable: true
```

`podman/pod.yml` gives the phenix container the same check as a liveness
probe, which `podman kube play` runs as its health check. Podman keeps the
Containerfile's `HEALTHCHECK` only in images built with `--format docker`.

## Monitoring Examples

The examples use `PHENIX_TOKEN` for a token from the Users page; leave the
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

### Metricbeat

The `http` module's `json` metricset stores each `/readyz` answer, so check
values can be graphed in Kibana:

```yaml
metricbeat.modules:
  - module: http
    metricsets: ["json"]
    period: 30s
    hosts: ["http://phenix.example.com:3000"]
    path: "/readyz"
    namespace: "phenix_health"
    headers:
      X-Phenix-Auth-Token: "Bearer ${PHENIX_TOKEN}"
```

The answers land under `http.phenix_health`. Because each key's
`observedValue` keeps one type, dynamic mapping does not conflict when a
check fails.

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

With TLS, add `scheme: HTTPS`. To probe from inside the container instead, use
`exec: {command: [phenix, healthcheck]}`, as `podman/pod.yml` does.

## Prometheus Metrics

`phenix ui` also serves metrics in the Prometheus text format at
`/api/v1/metrics` (`<base-path>/api/v1/metrics` behind a proxy). Unlike the
health endpoints, it needs credentials: a token of a user whose role has the
`metrics` `get` permission. Global Admin and Global Viewer have it; for a
scraper, create a role with only that permission and a user with a token:

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Role
metadata:
  name: metrics-reader
spec:
  roleName: Metrics Reader
  policies:
  - resources:
    - metrics
    verbs:
    - get
```

Without a token the endpoint answers `403`, with a malformed, expired or
signed-out token `401`, and with auth disabled it answers everyone. Set
`ui.metrics` to `false` (or run `phenix ui --metrics=false`) to stop serving
it; see [Settings](settings.md).

| Metric | Type | Labels | What it counts |
| :--- | :--- | :--- | :--- |
| `phenix_http_requests_total` | counter | `route`, `method`, `code` | Requests answered, by route template (such as `/api/v1/experiments/{name}`, `ui` for a UI page, or `unmatched`), method and status class (`2xx`). |
| `phenix_http_request_duration_seconds` | histogram | `route`, `method` | How long requests took to answer; websockets are left out. |
| `phenix_http_requests_in_flight` | gauge | | Requests being answered, open websockets included. |
| `phenix_minimega_commands_total` | counter | `command`, `outcome` | minimega commands, by name (such as `vm info`, or `other`) and outcome: `ok`, `error` (minimega answered with an error), `timeout`, `lost` (the connection failed) or `not_sent` (gave up waiting for the connection). |
| `phenix_minimega_command_duration_seconds` | histogram | `command` | How long minimega took to answer the commands sent to it. |
| `phenix_minimega_connection_wait_seconds` | histogram | | How long commands waited for the shared minimega connection. |
| `phenix_flight_calls_total` | counter | `group`, `outcome` | Calls of a shared minimega read (`vm-info`, `cluster-hosts`, `experiment-files`) that ran it or joined one already running. |
| `phenix_cache_lookups_total` | counter | `cache`, `result` | Lookups that found a reusable value (`hit`) or not (`miss`) in the `experiment-files`, `cluster-hosts` and `web` (screenshots, topology searches) caches. |
| `phenix_websocket_clients` | gauge | | Websocket clients connected to the broker. |
| `phenix_websocket_queue_length`, `phenix_websocket_queue_capacity` | gauge | | Broadcasts waiting for the broker, and how many fit. |
| `phenix_websocket_slow_clients_dropped_total` | counter | | Websocket clients dropped for being too slow. |
| `phenix_websocket_log_lines_dropped_total` | counter | | Log lines dropped from the live log stream. |
| `phenix_jobs_running`, `phenix_jobs_queued` | gauge | | [Background jobs](jobs.md) running and waiting to run. |
| `phenix_jobs_finished_total` | counter | `kind`, `status` | Background jobs finished, by kind and final status. |
| `phenix_store_operation_duration_seconds` | histogram | `operation`, `result` | How long BoltDB store operations (`list`, `get`, `create`, `update`, `delete`) took; `result` is `error` only when the store failed, not for a missing config. |
| `go_*`, `process_*` | | | The Go runtime and the phēnix process (memory, goroutines, garbage collection, CPU, open files). |

Labels never name an experiment, VM, user, host or file, so the number of
series stays the same however many experiments run.

A Prometheus scrape config, with the token in the `token` query parameter
(phēnix leaves it out of its request logs):

```yaml
scrape_configs:
  - job_name: phenix
    metrics_path: /api/v1/metrics
    params:
      token: ["<token>"]
    static_configs:
      - targets: ["phenix.example.com:3000"]
```

Prometheus versions whose scrape configs take `http_headers` can send the
header instead of the parameter:

```yaml
    http_headers:
      X-Phenix-Auth-Token:
        secrets: ["Bearer <token>"]
```
