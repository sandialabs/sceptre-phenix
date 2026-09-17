# Image Config Reference

Source of truth: [`src/go/types/version/v1/image.go`](../../../src/go/types/version/v1/image.go),
[`src/go/api/image/image.go`](../../../src/go/api/image/image.go),
[`src/go/cmd/image.go`](../../../src/go/cmd/image.go).

## Config document

Image configs live in the phenix store, not in files. Export/import with
`phenix config get image/<name> -p > foo.yml` and `phenix config create ./foo.yml`
(`cfg` is an alias of `config`).

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Image
metadata:
  name: foo
spec:
  variant: minbase            # minbase | mingui
  release: noble              # debootstrap release codename
  format: qcow2               # raw | qcow2 | vmdk | vdi | vhdx
  size: 10G                   # M or G suffix required
  mirror: http://us.archive.ubuntu.com/ubuntu
  compress: false             # qemu-img convert -c; ignored for raw
  ramdisk: false              # also emit <name>.kernel and <name>.initrd
  skip_default_packages: false
  no_virtuals: false          # skip /proc,/sys,/dev,/run mounts and the resolv.conf copy
  components: [main, restricted, universe, multiverse]
  packages: [curl, vim]
  overlays: [/phenix/sceptre-phenix-images/overlays/foo]   # absolute paths, copied onto /
  kernel: [net.ifnames=0]            # grub kernel params
  script_order:               # execution order (top to bottom)
    - POSTBUILD_APT_CLEANUP
    - POSTBUILD_NO_ROOT_PASSWD
    - POSTBUILD_PHENIX_HOSTNAME
    - POSTBUILD_PHENIX_BASE
    - /phenix/sceptre-phenix-images/scripts/foo.sh
  scripts:                    # key -> full inlined script body
    POSTBUILD_APT_CLEANUP: |
      apt clean || apt-get clean || echo "unable to clean apt cache"
    /phenix/sceptre-phenix-images/scripts/foo.sh: |
      set -ex
      ...
```

`scripts` keys are the literal path or URL given to `--scripts`; `phenix image update`
re-reads only the keys that still exist on disk. Older docs/OpenAPI mention
`deb_append` and `--debootstrap-append`; those are not in the current struct —
use `components` / `--components` instead.

## `phenix image create` flags

| Flag | Short | Default | Notes |
|---|---|---|---|
| `--size` | `-s` | `10G` | Must end in `M` or `G` |
| `--variant` | `-v` | `minbase` | Only `minbase` and `mingui` |
| `--release` | `-r` | `jammy` | debootstrap suite; only the exact Debian/Kali lists below get non-Ubuntu defaults |
| `--mirror` | `-m` | `http://us.archive.ubuntu.com/ubuntu` | Auto-switched only when left at default |
| `--components` | `-l` | variant-derived | Comma-separated apt components |
| `--format` | `-f` | `qcow2` | `raw`, `qcow2`, `vmdk`, `vdi`, `vhdx` |
| `--compress` | `-c` | `false` | No effect on `raw` |
| `--ramdisk` | `-R` | `false` | Adds `ramdisk` vmdb2 step |
| `--overlays` | `-O` | – | Comma-separated absolute dirs |
| `--packages` | `-P` | – | Comma-separated apt packages |
| `--scripts` | `-T` | – | Comma-separated paths or `http(s)://` URLs |
| `--skip-default-pkgs` | – | `false` | Drops the default package list |
| `--no-virtuals` | – | `false` | Scripts run without `/proc`, `/sys`, `/dev`, `/run`, cgroup2, or a copied `resolv.conf` (no DNS) |
| `--kernel-args` | `-k` | – | Comma-separated grub kernel params; replaces vmdb2's default `biosdevname=0 net.ifnames=0 consoleblank=0 rw` |

Multiple values must be comma-separated; space-separated values are parsed as extra
positional args and rejected with "must provide an image name as the only argument".

## Other subcommands

| Command | Behavior |
|---|---|
| `phenix image list [-f] [-c] [-m]` | Table of configs; optional format/compressed/mirror columns |
| `phenix image build <name\|file.vmdb>` | `-o` output dir (default cwd), `-c` write the rootfs tar, `-v`/`-x` verbosity, `--dry-run` prints the vmdb2 command. Any argument containing `.vmdb` is treated as a file; the image name becomes its basename without extension |
| `phenix image create-from <src> <new>` | Copy a config, optionally adding `-O/-P/-T` |
| `phenix image append <name>` | Add overlays/packages/scripts to an existing config (scripts are appended to `script_order`) |
| `phenix image remove <name>` | Remove overlays/packages/scripts from a config. Removing a script deletes its `scripts` entry but leaves the `script_order` entry, so a later `append` of the same path lists it twice |
| `phenix image update <name>` | Re-read on-disk scripts into the config; each refreshed key is removed from `script_order` and re-appended at the end, in map-iteration (random) order when several scripts are refreshed |
| `phenix image edit <name>` | Open the config in `$EDITOR` |
| `phenix image delete <name>` | Same as `phenix config delete image/<name>`; aliased as `del` |
| `phenix image inject-miniexe <exe> <disk[:part]>` | `--init-system systemd` (default) or `sysinitv` for Linux, `startup` for Windows; partition defaults to `1`. Linux: binary to `/usr/local/bin/<exe>` plus unit/init script and enable symlink. Windows: `.exe` to `C:\minimega\`, plus a Startup-folder scheduler for `miniccc.exe` when `startup` |

`build` runs: `vmdb2 <out>/<name>.vmdb --output <out>/<name> --rootfs-tarball <out>/<name>.tar`
(plus `-v` and `--log <out>/<name>.log` with `-x`). vmdb2 then converts and renames
qcow2 output to `<name>.qc2`. `--rootfs-tarball` is always passed: if the file
exists it is unpacked and `debootstrap`/`apt` are skipped; `-c` only decides
whether a missing tarball gets written.

## Defaults injected by `SetupImage`

Default packages (unless `--skip-default-pkgs`): `curl`, `ethtool`, `ncat`,
`net-tools`, `openssh-server`, `rsync`, `ssh`, `tcpdump`, `tmux`, `vim`, `wget`.

| Release family (exact match on `release`) | Extra `minbase` packages | Extra `mingui` packages |
|---|---|---|
| Ubuntu — any release not listed below (`xenial`, `bionic`, `focal`, `jammy`, `noble`, but also `trixie`, `kali`, typos) | `linux-image-generic`, `linux-headers-generic` | `wmctrl`, `xdotool`, `xubuntu-desktop` |
| Debian — `jessie`, `stretch`, `buster`, `bullseye`, `bookworm` | `dbus`, `gpg`, `initramfs-tools`, `linux-image-amd64`, `linux-headers-amd64`, `locales` | `wmctrl`, `xdotool`, `xfce4`, `xfce4-terminal` |
| Kali — `kali-dev`, `kali-rolling`, `kali-last-snapshot`, `kali-bleeding-edge` | `linux-image-amd64`, `linux-headers-amd64`, `default-jdk` | `kali-desktop-xfce`, `wmctrl`, `xdotool` |

The variant packages are added regardless of `--skip-default-pkgs`, which only
drops the default list above. `mingui` also appends `POSTBUILD_GUI`
(Ubuntu/Debian) or `POSTBUILD_KALI_GUI` (Kali) to the scripts.

Default components: `main restricted universe multiverse` (Ubuntu/Debian) or
`main contrib non-free non-free-firmware` (Kali). Mirror auto-selection when
`--mirror` is untouched: Debian → `http://ftp.us.debian.org/debian`,
Kali → `http://http.kali.org/kali`. `release: kali` is rewritten to `kali-rolling`
only for the debootstrap suite at build time; at create time bare `kali` is not in
the Kali list, so it still gets Ubuntu packages, components, and mirror.

Built-in postbuild scripts (see `constants.go`):

| Key | Effect |
|---|---|
| `POSTBUILD_GUI` / `POSTBUILD_KALI_GUI` | `mingui` only: lightdm root autologin, xfce theming, fixed resolution; purges `gdm3` on Debian/Ubuntu |
| `POSTBUILD_APT_CLEANUP` | `apt clean` |
| `POSTBUILD_NO_ROOT_PASSWD` | Blank root password, `PermitRootLogin yes`, `PermitEmptyPasswords yes` |
| `POSTBUILD_PHENIX_HOSTNAME` | Hostname `phenix`, `/etc/hosts`, phenix MOTD |
| `POSTBUILD_PHENIX_BASE` | `miniccc.service`, `phenix.service`, `/usr/local/bin/phenix-start.sh`, `/etc/phenix/startup/` |

## Rendered vmdb2 steps

From [`vmdb.tmpl`](../../../src/go/tmpl/templates/vmdb.tmpl), in order:

1. `mkimg` (size, format, compress) — always creates a raw file first, converts at teardown
2. `mklabel: msdos` → `mkpart: primary` (1M → 100%, tag `root`) → `kpartx` → `mkfs: ext4` → `mount` → `unpack-rootfs` (unpacks `<name>.tar` if it exists and sets `rootfs_unpacked`)
3. `debootstrap: <release>` with mirror and components, `unless: rootfs_unpacked` (when skipped vmdb2 still runs `apt-get update` in the chroot)
4. `virtual-filesystems: root` (omitted when `no_virtuals`) — mounts `/proc`, `/dev` (bind), `/dev/pts`, `/dev/shm`, `/run` (bind), `/sys`, `/sys/fs/cgroup` (cgroup2), and copies the host `/etc/resolv.conf` into the chroot for the rest of the build (the original is restored at teardown)
5. `apt: install` for `packages`, `unless: rootfs_unpacked` — vmdb2 runs `apt-get update`, installs `eatmydata`, installs the list *with* recommends, then `apt-get clean`
6. `cache-rootfs` when built with `--cache` (writes `<name>.tar` only if missing)
7. `copy-dir: /` for each overlay (overlays land before scripts run; files become `root:root`, mode = source mode `& ~022`)
8. `chroot: root` with every script concatenated into one `shell:` block, run as `sh -ec`
9. `fstab`, `grub: bios` (+ `kernel-params`; without them vmdb2 uses `biosdevname=0 net.ifnames=0 consoleblank=0 rw`), optional `ramdisk`

Because `debootstrap`/`apt` carry `unless: rootfs_unpacked`, an existing
`<name>.tar` skips both — release, mirror, and package changes require deleting the
tarball first, with or without `--cache`.

## Consuming built images

- Known image extensions in phenix: `.qcow2`, `.qc2`, `_rootfs.tgz`, `.hdd`, `.iso`
  (qcow2 and `.hdd` are VM images, `_rootfs.tgz` is a container image, `.iso` an ISO).
- Copy built images into the minimega files directory; phenix's base dir defaults to
  `/phenix` (`--base-dir.phenix`) with images conventionally under `/phenix/images`.
- Topologies reference images by file name:

```yaml
hardware:
  drives:
    - image: foo.qc2
      inject_partition: 1   # set to 0 for LVM images to skip injection
```
