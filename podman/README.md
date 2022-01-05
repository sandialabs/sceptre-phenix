# Podman Usage

This directory contains a Containerfile and a Kubernetes pod configuration that
can be used to run phenix and minimega using Podman.

## Usage

First, build the phenix image with Podman.

> NOTE: If you don't have `/etc/sub*id` configured for your user to run Podman
> in rootless mode, then you will need to run the following command as root.

```
podman build -f Containerfile -t phenix ..
```

Next, run the `headnode` pod with Podman.

> NOTE: The following command must be run as root since the pod is configured to
> run privileged containers and use host networking.

```
podman play kube pod.yml
```

## Aliases

Aliases for `phenix` and `mm` can be created similar to how they were done for
Docker.

```
alias mm="podman exec -it headnode-minimega /opt/minimega/bin/minimega -e"
alias phenix="podman exec -it headnode-phenix phenix"
```
