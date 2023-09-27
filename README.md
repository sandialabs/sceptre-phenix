# phenix ![status](https://img.shields.io/badge/status-alpha-red.svg) ![Docker Cloud Automated build](https://img.shields.io/docker/cloud/automated/activeshadow/phenix) ![Docker Package Build Status](https://github.com/sandialabs/sceptre-phenix/actions/workflows/docker.yml/badge.svg?branch=main)

Welcome to `phenix`!

## Building

### Local build
To build locally, you will need Golang v1.18, Node v14.2, Yarn 1.22, and Protoc 3.14 installed.

Once installed (if not already), simply run:
```bash
make bin/phenix
```

If you don't want to install Golang and/or Node locally, you can also use Docker to build phenix (assuming you have Docker installed). Simply run `./hack/build/docker-build.sh` and once built, the phenix binary will be available at `bin/phenix`. Run `./hack/build/docker-build.sh -h` for usage details.

### Docker build
Docker is the recommended way to build and deploy phenix.

The Docker image is updated automatically each time a commit is pushed to the
`main` branch. To pull the latest image:

```shell
docker pull ghcr.io/sandialabs/sceptre-phenix/phenix:main
```

> **NOTE**: currently the `main` image available on GHCR defaults to
> having UI authentication disabled. If you want to enable authentication,
> you'll need to build the image yourself, setting the `PHENIX_WEB_AUTH=enabled`
> Docker build argument. See issue #4 for additional details.

## Using

Please see the documentation at https://phenix.sceptre.dev for phenix usage
documentation.

## Git Workflow Support

phenix can now support a git workflow. The following assumes an intermediary
(e.g., a GitLab runner) exists to react to git push events.

The first example cURL command below should be run for each phenix topology or
scenario file present in the repository. These should be run prior to the
`Workflow Apply` request being executed.

Once all the phenix topology and scenario files have been added/updated, the
phenix workflow config file (likely to be a hidden file in the root of the
repository named `.phenix.yml`) should be applied. Applying the phenix workflow
config file will trigger existing experiments to be updated and restarted,
depending on settings within the workflow config file.

Optional tags can be passed as URL querries when the workflow config is applied.
These tags are stored as a string `key1=value1,key2=value2`.

### Add/Update Topology or Scenario Config

```bash
curl -XPOST -H "Content-Type: application/x-yaml" \
  --data-binary @{/path/to/config/file.yml} \
  http://localhost:3000/api/v1/workflow/configs/{branch name}
```

### Apply phenix Workflow Config

```bash
curl -XPOST -H "Content-Type: application/x-yaml" \
  --data-binary @{/path/to/config/file.yml} \
  http://localhost:3000/api/v1/workflow/apply/{branch name}[?tag=key1=value1&tag=key2=value2]
```

### phenix Workflow Config File Documentation

Below is an example phenix workflow config file.

```yaml
apiVersion: phenix.sandia.gov/v0
kind: Workflow
metadata: {}
spec:
  auto:
    create: {{BRANCH_NAME}}-<string>
    update: <bool>  # defaults to true
    restart: <bool> # defaults to true
  topology: {{BRANCH_NAME}}-<string>
  scenario: {{BRANCH_NAME}}-<string>
  vlans:
    <alias>: <int>
  schedules:
    <vm>: <string>
```

* `auto` - this group of settings is used to determine what events happen
  automatically when the workflow config is applied.
    * `create` - if set, this string will be used as the name of the new
      experiment to automatically be created for the current branch if one does
      not already exist. Omit this setting to prevent an experiment from
      automatically being created for the current branch.
    * `update` - if true (the default), an experiment for the current branch
      will be updated with its topology and scenario (if it has one). If the
      experiment is currently running, it will only be updated if `auto.restart`
      is also true.
    * `restart` - if true (the default), a running experiment for the current
      branch will be stopped, updated, and restarted when the workflow config is
      applied to phenix. If the existing experiment is already stopped, or a new
      experiment is created, it will be started. This setting has no affect if
      `auto.update` is disabled and an experiment exists for the current branch.
* `topology` - the name of the topology config to use for the experiment.
  Variable substation will be applied to the config name. This setting can be
  changed to force an existing experiment for the current branch to use a
  different topology.
* `scenario` - the name of the scenario config to use for the experiment.
  Variable substation will be applied to the config name. This setting can be
  changed to force an existing experiment for the current branch to use a
  different scenario.
* `vlans` - a map of VLAN alias to VLAN IDs to apply to the experiment for the
  current branch. This setting has no affect if `auto.update` is disabled, or if
  `auto.restart` is disabled and the experiment for the current branch is
  running.
* `schedules` - a map of VM hostnames to cluster hosts to apply to the
  experiment for the current branch. This setting has no affect if `auto.update`
  is disabled, or if `auto.restart` is disabled and the experiment for the
  current branch is running.
