# Changelog fragments

Add one Changie fragment per pull request instead of editing `CHANGELOG.md`.

Use the interactive prompt:

```bash
bin/tools/changie new
```

Or create a fragment non-interactively:

```bash
bin/tools/changie new --kind Added --body "Added experiment import validation."
bin/tools/changie new --kind Fixed --body "Fixed UI reconnect handling."
```

Supported kinds are `Security`, `Removed`, `Deprecated`, `Added`, `Changed`, and `Fixed`.

Changie auto-generates fragment filenames such as `added-1788918200000000000.yaml`.

Run `make changelog-check` before opening a pull request.

Release managers generate `CHANGELOG.md` with:

```bash
make changelog RELEASE_VERSION=1.1.0
```
