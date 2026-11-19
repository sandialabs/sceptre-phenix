# Health Checks

A running `phenix ui` server answers two health endpoints that need no
credentials, so Docker, systemd, Kubernetes or a load balancer can tell
whether phēnix is up.

```shell
# Liveness: is the phēnix process up?
curl -s http://localhost:3000/healthz

# Readiness: can phēnix run experiments right now?
curl -s http://localhost:3000/readyz
```

`phenix healthcheck` does the same from the command line. It exits `0` when
the server answers `200`, and `1` otherwise:

```shell
phenix healthcheck                 # liveness, prints e.g. "pass (200)"
phenix healthcheck --ready         # readiness, listing each check that is not passing
phenix healthcheck --ready -o json # the whole response body
phenix healthcheck --url https://phenix.example.com/readyz --timeout 10s
```

`phenix healthcheck` builds the URL from the `ui.listen-endpoint`, `ui.tls-key`
and `ui.tls-cert` settings, so it can't see a value you only gave as a flag to
`phenix ui`. In that case, pass `--url` or set the matching `PHENIX_UI_*`
environment variables. If `phenix ui` won't start at all, use
[doctor](doctor.md) instead.

## The Endpoints

* `/healthz` (liveness) checks that the server is up and the store can be
  read. Restarting phēnix may fix a failure, so the Docker and Podman health
  checks use it.
* `/readyz` (readiness) also checks minimega, the mesh, `qemu-img`, WebShark,
  free disk space and the websocket broker. A failure usually means something
  phēnix depends on is down.

Both answer `GET` and `HEAD` at the server root (or `<base-path>/healthz` with
`--base-path`), not under `/api/v1`. A proxy that requires its own sign-in on
every path must exempt them, or your monitors must probe the phēnix port
directly.

Each endpoint answers `200` with a status of `pass` or `warn` (something needs
a look, but phēnix works), and `503` with `fail`. The body is
[`application/health+json`](https://datatracker.ietf.org/doc/html/draft-inadarei-api-health-check-06),
with each result in `checks` keyed `component:measurement`, such as
`disk:utilization`.

## Checks

| Check | Endpoints | Warns when | Fails when |
| :--- | :--- | :--- | :--- |
| `uptime` | both | never | never |
| `store` | both | listing configs takes over a second | the store cannot be read |
| `minimega` | `/readyz` | minimega takes over a second to answer, the namespaces cannot be listed, or the mesh has fewer nodes than it has had since phēnix started | minimega does not answer |
| `qemu-img` | `/readyz` | `qemu-img` is missing or broken | never |
| `webshark` | `/readyz`, only when WebShark is installed | the WebShark build is gone | never |
| `disk` | `/readyz` | a filesystem is at least 90% full | a filesystem is at least 98% full |
| `websocket` | `/readyz` | the broadcast queue is at least 75% full | never |
| `go` | `/readyz` | never | never |

`disk` checks the minimega files directory, the phēnix base directory and the
directory that holds the store. To leave checks out, pass their names to
`?exclude=`, repeated or comma-separated:

```bash
curl -s 'http://localhost:3000/readyz?exclude=disk&exclude=qemu-img' | jq .status
```

Anyone who can reach the server sees each check's status and measurements.
The `output` fields, which name paths, hosts and errors, are only in answers
to [authenticated](api.md#authentication) requests. A probe that cannot set a
header can pass `?token=<token>`, but proxies and access logs often record the
full URL, token included.

## Docker and Podman

The `phenix` service in `docker/docker-compose.yml` runs `phenix healthcheck`
every 30 seconds, and only starts once the `minimega` service is healthy.
`podman/pod.yml` gives the phenix container the same check as a liveness
probe.

```bash
docker compose ps                              # the STATUS column shows (healthy)
docker exec phenix phenix healthcheck --ready  # minimega, store, disks, ...
```

The images don't set a `HEALTHCHECK` themselves. If you start your own
container to run `phenix ui`, add the check yourself:

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

## Kubernetes and Podman Probes

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

## See Also

* [phenix healthcheck](cli/healthcheck.md): every flag of the command.
* [Troubleshooting](troubleshooting.md): fixes for common problems.
