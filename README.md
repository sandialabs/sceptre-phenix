# phenix ![status](https://img.shields.io/badge/status-alpha-red.svg) ![Docker Cloud Automated build](https://img.shields.io/docker/cloud/automated/activeshadow/phenix) ![Docker Package Build Status](https://github.com/sandia-minimega/phenix/actions/workflows/docker.yml/badge.svg?branch=main)

Welcome to `phenix`!

## Building

To build locally, you will need Golang v1.14, Node v14.2, Yarn 1.22, and Protoc
3.14 installed. Once installed (if not already), simply run `make bin/phenix`.

If you don't want to install Golang and/or Node locally, you can also use Docker
to build phenix (assuming you have Docker installed). Simply run
`./docker-build.sh` and once built, the phenix binary will be available at
`bin/phenix`. See `./docker-build.sh -h` for usage details.

A Docker image is also hosted in this repo under Packages and can be pulled via:

```
$> docker pull ghcr.io/sandia-minimega/phenix/phenix:main
```

The Docker image is updated automatically each time a commit is pushed to the
`main` branch.

> **NOTE**: currently the `main` image available on GHCR defaults to
> having UI authentication disabled. If you want to enable authentication,
> you'll need to build the image yourself, setting the `PHENIX_WEB_AUTH=enabled`
> Docker build argument. See issue #4 for additional details.

## Using

Please see the documentation at https://phenix.sceptre.dev for phenix usage
documentation.
