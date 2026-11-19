# phēnix

phēnix provides a framework for defining, deploying, and managing complex cyber-physical experiments. It orchestrates virtual machines, networks, and applications to create realistic emulation environments. It runs the virtual machines and networks on [minimega](minimega.md).

## 🚀 Getting Started

The easiest way to get phēnix running is using Docker.

### Prerequisites

* Linux environment (Ubuntu 24.04+ recommended)
* Docker & Docker Compose

### Running with Docker

The phēnix repository includes a `docker-compose.yml` file in the `docker/` directory for running phēnix and its dependencies.

#### 1. Clone the Repository

```bash
git clone https://github.com/sandialabs/sceptre-phenix.git
cd sceptre-phenix/docker
```

#### 2. Start Services

The `docker-compose.yml` file allows you to either build from source or use a pre-built image.

##### Building from Source (Default)

This is the recommended method for development.

```bash
docker compose up -d --build
```

##### Using Pre-built Images

To use the pre-built image from the registry instead of building from source, run:

```bash
docker compose up -d --pull always
```

This will pull the latest images specified in the `image` key for each service (e.g., `ghcr.io/sandialabs/sceptre-phenix/phenix:main` and `ghcr.io/sandia-minimega/minimega:latest`) and skip the build step.

Each [GitHub release](https://github.com/sandialabs/sceptre-phenix/releases)
attaches SPDX JSON software bills of materials (SBOMs) of the phenix and JIT UI
images and of the Debian package, listing the packages and Go modules each one
contains.

To build the image with Podman instead, run `podman build -f docker/Dockerfile -t phenix .` from the repository root. See [`podman/README.md`](https://github.com/sandialabs/sceptre-phenix/blob/main/podman/README.md) for running phēnix and minimega as a Podman pod.

#### 3. Check Status

After starting the services with either method, view the logs to see the initialization progress:

```bash
docker compose logs -f
```

Once phēnix is up, `docker compose ps` shows the `phenix` container as
`(healthy)`. To also check that phēnix can reach minimega and its store, and
has enough disk space, run:

```bash
docker exec phenix phenix healthcheck --ready
```

See [Health Checks](health.md) for more. To check the configuration without the
server, run `docker exec phenix phenix doctor` (see [Doctor](doctor.md)).

!!! note
    The Docker image includes the user apps from the [sceptre-phenix-apps](https://github.com/sandialabs/sceptre-phenix-apps) repository.

#### 4. Open the Web UI

[`phenix ui`](cli/ui.md#phenix-ui) listens on port 3000 on all interfaces (`--listen-endpoint`, default
`0.0.0.0:3000`), and the Compose services use the host network. Open
`http://<host>:3000` in a browser, and see [Using the Web UI](web-ui.md).
The image that `docker-compose.yml` builds, and the image on GHCR, have web UI
authentication disabled. See [User Authn/Authz](user-administration.md) to
enable it.

### Installing Without Docker

To run phēnix without Docker, install the Debian package. Download the `.deb` from a [GitHub release](https://github.com/sandialabs/sceptre-phenix/releases), or build it with `./scripts/build-deb.sh` on a Debian or Ubuntu host (the build itself uses Docker, and puts the package in `build/deb/`). To build from source (Go 1.24+ and Node.js 24+), see the [repository README](https://github.com/sandialabs/sceptre-phenix#readme).

### Accessing the CLI & Shell Completion

With `phenix` running in a container, you can execute commands. For the best experience, including shell (tab) completion, you should install the provided wrapper script.

#### Using the Wrapper Script (Recommended)

The `phenix` repository includes a wrapper script that simplifies running `phenix` commands against the Docker container and is **required** for shell completion to work correctly.

1. **Install the wrapper:**
    From the root of the `sceptre-phenix` repository, run:

    ```bash
    # This command installs the `scripts/phenix-wrapper.sh` script to /usr/local/bin/phenix.
    sudo make install-wrapper
    ```

    !!! warning
        If you previously used a shell alias for `phenix` (e.g., `alias phenix="docker exec ..."`), you **must** remove it from your shell profile (e.g., `~/.bashrc`, `~/.zshrc`) for the wrapper and completion to work.

2. **Enable Shell Completion:**
    Add the completion script to your shell's startup file so it's available in new terminal sessions.

    ```bash
    # For Bash (requires the bash-completion package)
    echo 'source <(phenix completion bash)' >> ~/.bashrc

    # For Zsh
    echo 'source <(phenix completion zsh)' >> ~/.zshrc
    ```

    Zsh completion needs `compinit` to be loaded before the `source` line. Frameworks such as Oh My Zsh load it for you; otherwise, add `autoload -U compinit && compinit` to `~/.zshrc` above that line.

    Now, you can use <kbd>Tab</kbd> to autocomplete commands, flags, and even dynamic values like experiment and VM names. For more details, run `phenix completion --help`. `phenix completion` also supports Fish and PowerShell; see [phenix completion](cli/completion.md).

#### Manual Execution

You can also execute commands directly, but shell completion will not work.

```bash
docker exec -it phenix phenix <command>
```

The [CLI reference](cli/index.md) lists every command and flag.

## 📚 Documentation

* [**Using the Web UI**](web-ui.md): Learn the basics of the web UI.
* [**Workflow**](workflow.md): Deploy a topology directory with `phenix workflow apply`.
* [**Configuration**](configuration.md): Learn about Topologies, Scenarios, and Experiments.
* [**Settings**](settings.md): Configure the phēnix daemon (logging, storage, UI).
* [**Experiments**](experiments.md): Manage the lifecycle of your experiments.
* [**VMs**](vms.md): Use and interact with the VMs of an experiment.
* [**Disks**](disks.md): Manage the disk images on the cluster.
* [**Images**](image.md): Build virtual disk images for VMs.
* [**Builder**](builder/index.md): Draw topology diagrams and publish them as Topology and Experiment configs.
* [**Apps**](apps.md): Extend functionality with Apps.
* [**Scorch**](scorch.md): Orchestrate scenarios and collect experiment data.
* [**State of Health**](state-of-health.md): Check that the VMs of an experiment are up and reachable.
* [**Packet Captures**](packet-captures.md): Capture the traffic of an experiment.
* [**Logging**](logging.md): Learn about the logging facilities in phēnix and how to increase verbosity
* [**API**](api.md): Explore the interactive Swagger/OpenAPI docs served by phēnix and integrate with the API.
* [**User Administration**](user-administration.md): Enable authentication and manage users and roles.
* [**Doctor**](doctor.md): Check the configuration of a phēnix installation.
* [**CLI Reference**](cli/index.md): Every `phenix` command and flag.
* [**Troubleshooting**](troubleshooting.md): Solutions to common issues.
* [**Accessibility**](accessibility.md): Use the web UI with a keyboard or a screen reader.
