# phēnix

phēnix provides a framework for defining, deploying, and managing complex cyber-physical experiments. It orchestrates virtual machines, networks, and applications to create realistic emulation environments.

## 🚀 Getting Started

The easiest way to get phēnix running is using Docker.

### Prerequisites

* Linux environment (Ubuntu 22.04+ recommended)
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

This will pull the latest images specified in the `image` key for each service (e.g., `ghcr.io/sandialabs/sceptre-phenix/phenix:main` and `ghcr.io/sandia-minimega/minimega:master`) and skip the build step.

#### 3. Check Status

After starting the services with either method, view the logs to see the initialization progress:

```bash
docker compose logs -f
```

!!! note
    The Docker image includes the user apps from the [sceptre-phenix-apps](https://github.com/sandialabs/sceptre-phenix-apps) repository.

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
    # For Bash
    echo 'source <(phenix completion bash)' >> ~/.bashrc

    # For Zsh
    echo 'source <(phenix completion zsh)' >> ~/.zshrc
    ```

   Now, you can use <kbd>Tab</kbd> to autocomplete commands, flags, and even dynamic values like experiment and VM names. For more details, run `phenix completion --help`.

#### Manual Execution

You can also execute commands directly, but shell completion will not work.

```bash
docker exec -it phenix phenix <command>
```

## 📚 Documentation

* [**Configuration**](configuration.md): Learn about Topologies, Scenarios, and Experiments.
* [**Settings**](settings.md): Configure the phēnix daemon (logging, storage, UI).
* [**Experiments**](experiments.md): Manage the lifecycle of your experiments.
* [**Apps**](apps.md): Extend functionality with Apps.
* [**Logging**](logging.md): Learn about the logging facilities in phēnix and how to increase verbosity
* [**API**](api.md): Explore the interactive Swagger/OpenAPI docs served by phēnix and integrate with the API.
