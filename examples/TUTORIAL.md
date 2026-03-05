# Tutorial: Using the Example Applications

This tutorial walks you through running, modifying, and testing the features of the new Go and Python example applications.

## Prerequisites

*   **Go**: Version 1.24+ (for the Go example)
*   **Python**: Version 3.12+ (for the Python example). On Debian/Ubuntu, ensure `python3-venv` is installed.
*   **Make**: To run the automated build targets.

## 1. Setup

First, create a dummy input file. phēnix apps expect the experiment configuration via `STDIN`.
> **Note**: The `make run-*` commands used below generate this input automatically, but creating this file is useful for manual testing.

```bash
echo '{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "spec": {"experimentName": "tutorial-exp", "baseDir": "/tmp/tutorial", "scenario": {"apps": [{"name": "example", "hosts": []}]}}}' > input.json
```

## 2. Running the Python Example

The Python example uses the `phenix_apps` library.

1.  **Install Dependencies**:
    ```bash
    make install-dev
    ```

2.  **Run the App**:
    We will simulate the `running` stage.
    ```bash
    make -C examples run-python
    ```

3.  **Observe Output**:
    *   **STDOUT**: You should see the JSON configuration printed back.
    *   **STDERR**: You should see a JSON log line indicating the app is running, followed by an expected ERROR log (demonstrating error capture).
    *   **Note**: The Python example modifies the experiment in the `configure` stage, so for this `running` stage simulation, the output JSON is identical to the input.

4.  **Test Panic Recovery**:
    The example app has a built-in trigger to simulate a crash.
    ```bash
    export SIMULATE_PANIC=1
    make -C examples run-python
    ```
    **Result**: Instead of a messy Python traceback, you will see a structured JSON log on `stderr` with `"level": "ERROR"` and a `"traceback"` field. This ensures phēnix can parse the error.

    *Clean up:*
    ```bash
    unset SIMULATE_PANIC
    ```

## 3. Running the Go Example

The Go example demonstrates how to use the core `phenix` packages without external frameworks.

1.  **Build and Run the App**:
    ```bash
    make -C examples run-go
    ```

2.  **Observe Output**:
    Similar to Python, `STDOUT` contains the experiment JSON, and `STDERR` contains structured logs.
    *   **Note**: Unlike the Python example, the Go app logic modifies the experiment during the `running` stage. You should see `"example-go-processed": "true"` in the `metadata.annotations` of the output JSON.

3.  **Dynamic Log Levels**:
    By default, the app runs at `INFO` level. Let's enable `DEBUG` logs using the standard environment variable.

    ```bash
    export PHENIX_LOG_LEVEL=DEBUG
    make -C examples run-go
    ```

    **Result**: You will see an extra log line on `stderr`:
    ```json
    {"time":"...","level":"DEBUG","msg":"detailed debug info","iteration":1,"status":"active"}
    ```

## 4. Modifying the App

Let's modify the Go app to add a new log message.

1.  Open `examples/go/main.go`.
2.  Find the `running` case in the `executeStage` function.
3.  Add a new log line:
    ```go
    logger.Info("Hello from the tutorial!", "user", os.Getenv("USER"))
    ```
4.  Rebuild and run it again (the `run-go` target automatically rebuilds the binary):
    ```bash
    make -C examples run-go
    ```
5.  You should see your new structured log message appear in the output!