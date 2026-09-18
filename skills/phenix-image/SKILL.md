---
name: phenix-image
description: 'Build and debug phenix/phēnix VM disk images (phenix image create/build/update/append/inject-miniexe, vmdb2 configs, image build scripts, overlays, .qc2 qcow2 output, the sceptre-phenix-images repo). Use when writing or fixing image build scripts, authoring Image configs (kind: Image), adding packages/overlays, choosing a release/variant, injecting miniccc/minirouter/protonuke, or troubleshooting vmdb2/debootstrap/chroot build failures.'
license: GPL-3.0-only
---

# phenix Image Building

phenix builds Debian-family VM disk images by rendering an `Image` config into a
`.vmdb` file and shelling out to [vmdb2](https://gitlab.com/glattercj/vmdb2)
(a phenix-specific fork), which wraps `qemu-img`, `parted`, `kpartx`,
`debootstrap`, and `chroot`.

Chain: `phenix image build` → `vmdb.tmpl` → `<name>.vmdb` → `vmdb2` → `<name>.qc2`
→ `phenix image inject-miniexe` (the build alone yields a VM that never checks in).

## When to Use This Skill

- Writing or reviewing an image build script (`scripts/*.sh` in `sceptre-phenix-images`)
- Creating/editing an `Image` config (`kind: Image`, `phenix.sandia.gov/v1`)
- Choosing release, variant, size, format, packages, components, overlays, kernel args
- Injecting `miniccc` / `minirouter` / `protonuke` into an already-built disk
- Debugging build failures in debootstrap, apt, chroot scripts, grub, or kpartx
- Explaining where images live and how topologies reference them

## Task Routing

| Task | Do | Read |
|---|---|---|
| New image from scratch | Core Workflow below | — |
| Add packages/scripts/overlays to an existing config | `phenix image append -P/-T/-O <name>`, `rm <out>/<name>.tar`, rebuild | Gotchas: Config lifecycle |
| Script edited on disk, rebuild picks up old copy | `phenix image update <name>`, check `script_order`, rebuild | Gotchas: Config lifecycle |
| Clean rebuild | `phenix image delete <name>; rm -f <out>/<name>.{tar,vmdb,log,qc2}`, re-create | — |
| Build failed | Rebuild with `-x`, read `<out>/<name>.log`, then Troubleshooting | Troubleshooting |
| Write or review a build script | Writing Build Scripts | [references/scripts.md](./references/scripts.md) |
| Pick release/variant/mirror/format, see defaults | Gotchas: Release and packages | [references/image-config.md](./references/image-config.md) |
| Host or container setup, CI, Kali host prep | — | [references/environment.md](./references/environment.md) |
| Reference the image from a topology | `hardware.drives[].image: <name>.qc2` in `/phenix/images` | [references/image-config.md](./references/image-config.md) "Consuming built images" |

## Core Workflow

All commands run inside the phenix container in a normal deployment; from the
host, prefix them with `docker exec phenix`. Script, overlay, and `-o` paths must
resolve *inside* that container, and the container's working directory
(`/opt/phenix`) is **not** bind-mounted — always pass an absolute `-o` under
`/phenix` or the artifacts stay inside the container.

```bash
# from the host: docker exec phenix phenix image ...

# 1. create the config (scripts are inlined; overlay paths are stored)
phenix image create -r noble -v mingui -s 20G --compress \
  -P nginx,jq -O /phenix/sceptre-phenix-images/overlays/foo -T /phenix/sceptre-phenix-images/scripts/foo.sh foo

# 2. build (vmdb2 must be in $PATH; needs the privileged container or root)
phenix image build -o /phenix/images -c -x foo   # -c write foo.tar rootfs cache, -x very verbose + foo.log

# 3. inject the minimega agent (needs minimega running)
#    miniccc ships in the minimega container: docker cp minimega:/opt/minimega/bin/miniccc /phenix/
phenix image inject-miniexe /phenix/miniccc /phenix/images/foo.qc2

# 4. foo.qc2 is now in the minimega files dir (MM_FILEPATH, /phenix/images in docker-compose);
#    move foo.tar/foo.vmdb/foo.log elsewhere if you want the files listing clean
```

Expect 10–40 minutes per build depending on packages and network. Without `-v`
vmdb2 prints almost nothing until it fails, so use `-x` and tail
`<out>/<name>.log` rather than assuming a hang. Free space needed: up to `--size`
for the raw image plus the converted copy plus `<name>.tar`.

Image names become file names (`<name>.qc2`); keep them to `[A-Za-z0-9_-]`, and
never include `.vmdb` — `build` treats any argument containing `.vmdb` as a file
path.

Alternate inputs: import an exported config (`phenix config get image/foo -p > foo.yml`,
later `phenix config create ./foo.yml`; `cfg` is an alias of `config`), or build a
hand-edited `.vmdb` directly (`phenix image build -o /phenix/images -c -x ./foo.vmdb`)
when the template's msdos/ext4/BIOS layout is not enough — e.g. UEFI needs a
`grub: uefi` step with an EFI partition, which only vmdb2's own docs cover.

## Inspect and Verify

```bash
phenix image list -f -c -m                              # configs with format/compressed/mirror
phenix config get image/foo -p                          # scripts, script_order, packages, overlays
phenix image build --dry-run -o /phenix/images foo      # renders foo.vmdb and prints the vmdb2 command
qemu-img info /phenix/images/foo.qc2                    # format, virtual size, compressed?
guestfish --ro -a /phenix/images/foo.qc2 -i ls /etc/phenix/startup   # phenix base present (guestfish is in the container)
guestfish --ro -a /phenix/images/foo.qc2 -i ls /usr/local/bin        # miniccc present after inject-miniexe
```

Reference material — read only what the current task needs:

- [references/image-config.md](./references/image-config.md) — full `Image` spec fields, CLI flags, defaults, default package lists, vmdb2 step mapping
- [references/scripts.md](./references/scripts.md) — script execution model, ordering, overlays, working examples
- [references/environment.md](./references/environment.md) — required tooling/privileges, container and CI setup, output artifacts, Kali host prep, post-build checks

## Gotchas

### Config lifecycle

- **Scripts are snapshotted into the config at create time**, not read from disk at
  build time. After editing a script on disk, run `phenix image update <name>` or
  the rebuild silently uses the stale copy. `update` only refreshes script keys
  that still resolve to an existing file path, and it **moves each refreshed script
  to the end of `script_order`** (in map-iteration order, so the relative order of
  several refreshed scripts is random) — re-check ordering after updating. Overlay
  directories are different: the config stores their paths, and `vmdb.tmpl`
  reads/copies them from disk during each build.
- **`remove -T x` then `append -T x` runs `x` twice.** `remove` deletes the
  `scripts` entry but leaves its `script_order` entry; `append` adds another. Fix
  with `phenix image edit <name>` and delete the duplicate `script_order` line.
- **An existing `<name>.tar` in the output dir is always reused**, with or without
  `-c`. `build` always passes `--rootfs-tarball <out>/<name>.tar`; if the file
  exists, vmdb2 unpacks it and skips `debootstrap` and `apt` (release, mirror, and
  package changes are ignored). `-c` only controls *writing* the tarball; delete it
  to force a fresh bootstrap. (`-c` is `--compress` on `create`, `--cache` on
  `build`, and "show compressed column" on `list`.)
- **`phenix image create` fails if the config name already exists** — delete it first
  (`phenix image delete <name>`); it does not overwrite.
- **Script and overlay paths are resolved where the phenix process runs.** With the
  container deployment, `create`, `update`, and `build` open them inside the
  container, so bind-mount the images repo at the same path on both sides
  (`opening script file: no such file or directory` otherwise). `inject-miniexe`
  additionally needs a reachable minimega, since it uses `disk inject`.

### Script execution

- **All scripts run as ONE `sh -ec` invocation inside the chroot**, concatenated in
  `script_order`. Consequences: shebangs are inert comments, **bashisms fail**
  (`sh` is dash: no `[[ ]]`, arrays, `source`, `pipefail`), `set -e` is already
  active so any failing command aborts the whole build, `exit 0` skips every later
  script, and `cd`/exported vars leak into the scripts that follow. phenix also
  **drops every blank line** when it inlines scripts into the `.vmdb`, so heredocs
  that need an empty line must write it another way (`printf '\n'`).
- **User scripts run LAST, after phenix's `POSTBUILD_*` scripts.** `SetupImage` appends
  `POSTBUILD_GUI` (mingui only), `POSTBUILD_APT_CLEANUP`, `POSTBUILD_NO_ROOT_PASSWD`,
  `POSTBUILD_PHENIX_HOSTNAME`, `POSTBUILD_PHENIX_BASE` before `--scripts` entries.
  So user scripts can override the phenix hostname, root password policy, and
  systemd units — and apt caches created by user scripts are *not* cleaned, since
  `POSTBUILD_APT_CLEANUP` already ran.

### Release and packages

- **Release detection is an exact list, not a prefix match.** Debian =
  `jessie stretch buster bullseye bookworm`; Kali = `kali-dev kali-rolling
  kali-last-snapshot kali-bleeding-edge`; everything else (including `trixie` and
  bare `kali`) is treated as Ubuntu and gets the Ubuntu mirror, components, and
  `linux-image-generic`, so debootstrap or apt fails. Use `kali-rolling`, not
  `kali`. For a release outside the lists, `create` with explicit `--mirror` and
  `--components`, then swap the kernel packages (`--skip-default-pkgs` does not
  affect them): `phenix image remove -P linux-image-generic,linux-headers-generic
  <name>` and `phenix image append -P linux-image-amd64,linux-headers-amd64 <name>`.
- **The mirror default only auto-switches when left untouched**: passing an explicit
  `--mirror` with a Debian/Kali release keeps the Ubuntu URL and debootstrap fails.
- **Only `minbase` and `mingui` variants exist**; anything else fails with
  `variant <x> is not implemented`.
- **`--kernel-args` replaces vmdb2's default kernel parameters** (`biosdevname=0
  net.ifnames=0 consoleblank=0 rw`) rather than appending; re-list the ones you
  still want (`-k net.ifnames=0,biosdevname=0,consoleblank=0,rw,foo=bar`).
- **`--size` requires an `M` or `G` suffix** (`10G`, `500M`); anything else is
  rejected before the config is stored. Default is `10G`, which fills fast for
  GUI/Kali builds (upstream uses 50–80G for those).

### Output and formats

- **`phenix image build` output is `<name>.qc2` for qcow2**, not `<name>` or
  `<name>.qcow2`; raw is left as `<name>`. Build also drops `<name>.vmdb` and
  `<name>.tar` in `-o`. `--compress` is qemu-img's `-c` convert flag: no suffix
  change for qcow2, no effect on raw. `vmdk`/`vdi`/`vhdx` are accepted but
  effectively broken: compressed output is `<name>_c` with no extension, and
  uncompressed output converts onto the same path as the raw source. Build qcow2
  and run `qemu-img convert` afterwards if another format is needed.
- **Linux/Debian-family/amd64 only.** debootstrap supports Ubuntu, Debian, and Kali
  releases; the template hardcodes msdos label, single ext4 partition, and
  `grub: bios`. Windows and RPM-based images cannot be built — they must be
  produced externally and copied into the images directory.
- **Image building is CLI-only.** The web UI lists disk files but cannot build images.

### Injection

- **`inject-miniexe` is required for miniccc**; the build only writes a
  `miniccc.service` unit pointing at `/opt/minimega/bin/miniccc` (via
  `POSTBUILD_PHENIX_BASE`), not the binary. `inject-miniexe` places the binary at
  `/usr/local/bin/miniccc` and overwrites that unit with one that matches.
  `--init-system` is `systemd` (default) or `sysinitv` for Linux; `startup` is
  Windows-only (adds a Startup-folder scheduler for `miniccc.exe`). Injecting
  `minirouter` auto-injects the `miniccc` found next to it; Windows disks only
  accept `miniccc.exe` and `protonuke.exe`, installed under `C:\minimega\`.

## Writing Build Scripts

Start from this header (the shebang is inert but keeps shellcheck honest):

```sh
#!/bin/sh
set -ex
export DEBIAN_FRONTEND=noninteractive LC_ALL=C PIP_DISABLE_PIP_VERSION_CHECK=1
apt-get update
apt-get install -y --no-install-recommends <packages>
```

- Target POSIX `sh`; `DEBIAN_FRONTEND` and `LC_ALL` are not set for you.
- `/proc`, `/sys`, `/dev`, `/run`, and `/sys/fs/cgroup` are mounted in the chroot
  and the host's `/etc/resolv.conf` is copied in (unless `--no-virtuals`, which
  also leaves the chroot without DNS), so `apt`, downloads, and services such as
  `dockerd` work during the build.
- Prefer `--packages` for plain apt installs; use scripts for third-party repos,
  `.deb`/pip installs, service configuration, and user creation.
- Keep scripts non-interactive, and append `|| true` to commands that may legitimately
  fail (e.g. `useradd` for a user a package already created), since `set -e` aborts
  the build.
- Ship files with an overlay directory (`-O`) instead of heredocs when adding more
  than a couple of config files. vmdb2 copies overlays as `root:root` with the
  source mode bits masked by `022`; the `chown -R root:root /etc` seen in upstream
  scripts is a historical safety net, not a requirement.
- Runtime (not build-time) logic belongs in `/etc/phenix/startup/`; `phenix.service`
  runs every file there on boot.

## Troubleshooting

| Symptom | Cause / Fix |
|---|---|
| `vmdb2 app does not exist in your path` | Run inside the phenix container, or install the fork and symlink `/opt/vmdb2/vmdb2 → /usr/bin/vmdb2` |
| `getting image config <name> from store` | No config by that name — check `phenix image list`; `create` first |
| `config already exists` on `create` | `phenix image delete <name>` first; `create` never overwrites |
| `must provide a valid unit for disk size option` | `--size` needs an `M` or `G` suffix |
| `must provide an image name as the only argument` | A multi-value flag was space-separated; use commas |
| `opening script file: no such file or directory` | `-T` path does not resolve where phenix runs (inside the container) |
| `scheme <x> not supported for scripts` | `-T` accepts local paths and `http(s)://` URLs only |
| Script edits not showing up in the built image | Config holds an inlined copy — run `phenix image update <name>` |
| `E: Invalid Release file` / debootstrap 404 | Release/mirror mismatch (Debian or Kali release with the Ubuntu mirror default) |
| Kali build fails on a non-Kali host (`E: No such script: /usr/share/debootstrap/scripts/kali-rolling`) | Install `kali-archive-keyring` and create the debootstrap script — see "Kali-specific host setup" in [references/environment.md](./references/environment.md) |
| `[[: not found`, `Syntax error: "(" unexpected` | Bashism in a build script — rewrite for dash |
| Build stops midway with no error from your script | An earlier concatenated script failed under `set -e`; rebuild with `-x` and read `<name>.log` |
| kpartx/loop or mount failures | Build container lacks privileges / loop devices; run privileged with `/dev` access |
| Built `foo.qc2` is nowhere on the host | `-o .` resolved to the container's `/opt/phenix`; use an absolute `-o` under `/phenix` |
| Image builds but VM never checks in to minimega | `miniccc` binary was never injected — run `phenix image inject-miniexe` |
| `only miniccc, protonuke, and minirouter are supported for Linux` / `... for Windows` | `inject-miniexe` decides by basename and `.exe`; rename or point at the real binary |
| `unknown service <x> specified` | `--init-system` must be `systemd` or `sysinitv` for Linux disks (`startup` is Windows-only) |
| Rebuild ignores release/mirror/package changes | A `<name>.tar` in the output dir is unpacked and skips debootstrap/apt even without `-c`; delete it |
| `apt-get update` fails with `Temporary failure resolving` inside the build | Host `/etc/resolv.conf` is empty/unusable or the build ran with `--no-virtuals` (no resolv.conf copy) |
| A user script runs twice | `script_order` has a duplicate key (from `remove` then `append`); fix with `phenix image edit` |
| `linux-image-generic` has no installation candidate | Release not in the Debian/Kali lists (e.g. `trixie`, bare `kali`) — see Gotchas |
| VM boots with `ens3`-style NIC names or a blanking console | `--kernel-args` replaced vmdb2's defaults; add back `net.ifnames=0,biosdevname=0,consoleblank=0,rw` |

## Related Resources

- Sibling skill [`phenix`](../phenix/SKILL.md) — the wider CLI/API surface
  (topology, scenario, experiment, config) that consumes these images
- [`AGENTS.md`](../../AGENTS.md#documentation-and-references) — repository
  documentation map and companion phēnix repositories
- Upstream image scripts, overlays, and Makefile targets: <https://github.com/sandialabs/sceptre-phenix-images>
- Pre-built images (requires `oras`): `oras pull ghcr.io/sandialabs/sceptre-phenix-images/bennu.qc2:latest`
- In-tree implementation: [image.go](../../src/go/api/image/image.go),
  [constants.go](../../src/go/api/image/constants.go),
  [cmd/image.go](../../src/go/cmd/image.go),
  [vmdb.tmpl](../../src/go/tmpl/templates/vmdb.tmpl)
