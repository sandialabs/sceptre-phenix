# Example Applications

The `sceptre-phenix` repository contains complete, runnable reference implementations for Go and Python applications.

These examples demonstrate best practices for:

* **Lifecycle Management**: Handling `configure`, `start`, `stop`, etc.
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

## Source Code

You can find the full source code, including unit tests and the `Makefile`, in the `examples/` directory of the main repository.
