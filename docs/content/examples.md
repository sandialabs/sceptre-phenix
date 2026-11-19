# Example Applications

The `sceptre-phenix` repository contains complete, runnable reference implementations for Go and Python applications.

These examples demonstrate best practices for:

* **Lifecycle Management**: Handling `configure`, `pre-start`, `post-start`, etc.
* **Configuration**: Parsing the experiment JSON from `STDIN`.
* **Logging**: Implementing the App Contract (JSON on `stderr`).
* **Error Handling**: Capturing panics and exceptions as structured logs.

## Setup

To run the examples, you first need to clone the main repository and install the development dependencies:

```bash
# 1. Clone the main repository and navigate into it
git clone https://github.com/sandialabs/sceptre-phenix.git
cd sceptre-phenix

# 2. Install dependencies for Go and Python examples
make install-dev
```

## 🐍 Python Example

The Python example demonstrates how to use the `phenix_apps` library to build robust applications with structured logging and error handling.

### Usage

```bash
# Run the app (simulating the 'running' stage)
make -C examples run-python
```

## 🐹 Go Example

The Go example demonstrates how to build a lightweight app using the Go standard library and core `phenix` packages.

### Usage

```bash
# Build and run the app (simulating the 'running' stage)
make -C examples run-go
```

## A Minimal User App

Below is a very contrived example of a simple _user app_ that changes the disk
image used for every node in the experiment topology. Assuming the name of the
executable for this app as `phenix-app-image-changer`, it could be applied to a
topology by including a scenario in an experiment that includes an experiment
app named `image-changer`.
This example demonstrates best practices, including using the `phenix_apps` logger to produce structured JSON logs on `stderr`.

Unlike the runnable Python example, this app does not use the `AppBase` class of the `phenix_apps` library. It reads the lifecycle stage and the experiment JSON itself, as [User Apps](apps.md#user-apps) describes, and it calls `configure_logging()` itself. phēnix sets `PHENIX_LOG_FILE=stderr` when it runs a user app (see [phēnix Apps Environment Variables](settings.md#phenix-apps-environment-variables)), and `configure_logging()` then writes the logs to `stderr` as JSON.

```python
import json
import sys
from phenix_apps.common.logger import configure_logging, logger

def main():
    # Write JSON logs to the place that PHENIX_LOG_FILE names.
    # phēnix sets it to 'stderr' when it runs the app.
    configure_logging()

    # The first argument is the lifecycle stage (e.g., 'pre-start')
    try:
        stage = sys.argv[1]
    except IndexError:
        logger.error("Lifecycle stage argument not provided.")
        # Raising an exception is preferred over sys.exit()
        # phēnix will capture the non-zero exit code.
        raise ValueError("Missing stage argument")

    # Read the experiment JSON from stdin
    try:
        experiment = json.load(sys.stdin)
    except json.JSONDecodeError:
        logger.error("Failed to decode experiment JSON from stdin.")
        raise

    logger.info(f"Executing stage: {stage}")

    # Only modify the spec in the 'pre-start' or 'configure' stage
    if stage in ["pre-start", "configure"]:
        try:
            spec = experiment['spec']
            for node in spec['topology']['nodes']:
                hostname = node.get("general", {}).get("hostname", "unknown")
                logger.info("Processing node", hostname=hostname)
                for drive in node['hardware']['drives']:
                    original_image = drive['image']
                    drive['image'] = 'm$.qc2'
                    logger.debug(
                        "Changed image for node",
                        hostname=hostname,
                        from_image=original_image,
                        to_image=drive['image'],
                    )
        except (KeyError, TypeError) as e:
            logger.exception("Failed to parse experiment spec to change image.")
            raise

    # Print the (potentially modified) experiment JSON to stdout
    # This is how the app returns its changes to phēnix.
    print(json.dumps(experiment))

if __name__ == "__main__":
    try:
        main()
    except Exception:
        # phēnix will capture the non-zero exit code from the raised exception
        # and log any messages written to stderr.
        sys.exit(1)
```

## Source Code

You can find the full source code, including unit tests and the `Makefile`, in the `examples/` directory of the main repository.

## See Also

* [Go example](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/go/main.go): the app (`main.go`), with its tests in [main_test.go](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/go/main_test.go).
* [Python example](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/python/app.py): the app (`app.py`), with its tests in [test_app.py](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/python/test_app.py).
* [Makefile](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/Makefile): the `run-go` and `run-python` targets.
* [Tutorial](https://github.com/sandialabs/sceptre-phenix/blob/main/examples/TUTORIAL.md): run, change and test the examples step by step.
* [Apps](apps.md#user-apps): how phēnix finds and calls a user app.
* [phenix util app-json](cli/util.md#phenix-util-app-json): print the JSON that a user app reads.
