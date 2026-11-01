# Podman Usage

This directory contains a Containerfile and a Kubernetes pod configuration that
can be used to run phenix and minimega using Podman.

## Usage

First, build the phenix image with Podman.

> NOTE: If you don't have `/etc/sub*id` configured for your user to run Podman
> in rootless mode, then you will need to run the following command as root.

```text
podman build -f Containerfile -t phenix ..
```

Next, run the `headnode` pod with Podman.

> NOTE: The following command must be run as root since the pod is configured to
> run privileged containers and use host networking.

```text
podman play kube pod.yml
```

## Health Checks

`pod.yml` gives the phenix container a liveness probe, `phenix healthcheck`,
which Podman runs as the container's health check:

```text
podman healthcheck run headnode-phenix && echo healthy
podman exec headnode-phenix phenix healthcheck --ready
```

The Containerfile sets no `HEALTHCHECK`, so containers that run other phenix
commands have no check to turn unhealthy. See the
[health checks documentation](https://phenix.sceptre.dev/latest/health/).

## Aliases

Aliases for `phenix` and `mm` can be created similar to how they were done for
Docker.

```text
alias mm="podman exec -it headnode-minimega /opt/minimega/bin/minimega -e"
alias phenix="podman exec -it headnode-phenix phenix"
```
