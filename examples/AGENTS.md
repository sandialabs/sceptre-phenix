# Example App Instructions

Read root [`AGENTS.md`](../AGENTS.md) first, especially its app lifecycle and
stdout/stderr contracts. These rules apply under `examples/`.

## Scope

`go/` and `python/` are runnable reference implementations of phēnix apps.
Changes must remain clear examples of the production app contract, not introduce
example-only behavior that contradicts the platform.

## Setup and Commands

Python requires 3.12+. `make -C examples install-dev` creates `examples/.venv`;
set `SYSTEM_PYTHON` when the default interpreter is older.

| Purpose | Command |
|---|---|
| Build and test Go example | `make -C examples example-go` |
| Compile and test Python example | `make -C examples example-python` |
| Test both | `make -C examples test` |
| Non-fixing checks | `make -C examples check` |
| Format | `make -C examples format` |
| Run with sample input | `make -C examples run-go` / `run-python` |

## Conventions and Tests

- Go uses `gofmt`, `golangci-lint`, `log/slog`, and JSON logs on stderr.
- Python uses Black, Flake8, and `phenix_apps.common.logger`.
- Keep stdout exclusively for the resulting experiment JSON.
- Go tests use the subprocess pattern for arguments, streams, and exit codes.
- Python tests mock stdin and capture structured stderr.
- Update `README.md` and `TUTORIAL.md` when example behavior or workflows
  change.

`.github/workflows/examples.yml` builds/tests both examples with Go 1.24 and
Python 3.12. Keep workflow versions, dependencies, and Make targets aligned.
