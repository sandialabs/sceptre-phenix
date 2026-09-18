# Build Environment

## Requirements

Image builds are Linux-only, amd64-only, and need root/privileged execution
(loop devices, `kpartx`, mount, chroot).

| Requirement | Why |
|---|---|
| `vmdb2` in `$PATH` | phenix shells out to it; the phenix fork is <https://gitlab.com/glattercj/vmdb2> (upstream vmdb2 lacks the `.qc2` conversion and ramdisk behavior) |
| `debootstrap` | Bootstraps the rootfs |
| `qemu-utils` (`qemu-img`) | Creates the raw image and converts/compresses to qcow2/vmdk/vdi/vhdx |
| `parted`, `kpartx`, `e2fsprogs`, `psmisc` | Partition table, loop mapping, ext4, `fuser` for forced unmounts |
| `grub-pc`, `cpio`, `xz-utils`, `bash` | `grub: bios` step and ramdisk generation (`ramdisk` runs its pipeline through host `bash`) |
| `python3`, `python3-yaml`, `python3-jinja2` | vmdb2 itself |
| Privileged container / root | Mounting `/proc`, `/sys`, `/dev` into the chroot |
| Disk space in the output dir | Raw image of `--size` plus converted copy plus `<name>.tar` cache |
| Network access | debootstrap, apt, and script downloads |

The in-tree [`docker/Dockerfile`](../../../docker/Dockerfile) and
[`podman/Containerfile`](../../../podman/Containerfile) `apt-get install` the
full set:

```text
cmdtest cpio debootstrap git iproute2 iputils-ping kpartx locales nano parted
psmisc python3 python3-jinja2 python3-pip python3-yaml qemu-utils tshark vim
wget xz-utils zerofree guestfish
```

plus `git clone https://gitlab.com/glattercj/vmdb2 /opt/vmdb2` and
`ln -s /opt/vmdb2/vmdb2 /usr/bin/vmdb2`. The Debian package built by
[`scripts/build-deb.sh`](../../../scripts/build-deb.sh) declares the same
dependencies, so a host install of the phenix `.deb` is also a valid build host.

## Building through the container

Upstream `sceptre-phenix-images` runs everything through an already-running phenix
container, with the working directory bind-mounted so paths match inside and out:

```make
PHENIX = docker exec -t phenix phenix
PHENIX_IMAGE_BUILD = $(PHENIX) image build -o $(WORKDIR) -c -x $(@) || exit 1
INJECT_MINICCC = if test -f $(WORKDIR)/$(@).qc2; then \
    $(PHENIX) image inject-miniexe $(MINICCC) $(WORKDIR)/$(@).qc2; fi
```

Because the config stores overlay paths and script keys as the paths passed to
`--scripts`, those paths must resolve *inside* the container when configs are
created, updated, or built. Mounting the repo at the same path on both sides avoids
"opening script file: no such file or directory" and missing `copy-dir` overlays.

Each `make` target first checks that the image config does not already exist and
warns when a `<name>.tar` rootfs cache is present (reusing it skips debootstrap and
apt, so release/package changes are ignored). `make clean` removes
`*.log *.qc2 *.tar *.vmdb`.

CI (`.github/workflows/image-build.yml`) installs `qemu-utils` and `guestfs-tools` on
the runner, pulls `ghcr.io/sandialabs/sceptre-phenix/phenix:main` and the minimega
image, builds weekly, and pushes artifacts with `oras`. VyOS additionally needs
`sudo modprobe nbd` on the host and uses a bespoke `scripts/vyos/build-vyos.sh`
because it cannot be debootstrapped.

## Output artifacts

For `phenix image build -o <dir> <name>`:

| File | When |
|---|---|
| `<name>.qc2` | qcow2 format (renamed by vmdb2 during `qemu-img convert`; `--compress` adds `-c` but not a suffix) |
| `<name>` | raw format (no conversion step) |
| `<name>_c` | `vmdk`/`vdi`/`vhdx` with `--compress` — no extension, because phenix passes `--output <out>/<name>`; without `--compress` these formats convert onto the same path as the raw source and are not usable |
| `<name>.vmdb` | always — the rendered vmdb2 config |
| `<name>.tar` | written by `--cache` when absent; unpacked (and debootstrap/apt skipped) whenever present, `--cache` or not |
| `<name>.log` | with `-x` / `--very-verbose` |
| `<name>.kernel`, `<name>.initrd` | with `ramdisk: true` |

Use `--dry-run` to print the exact `vmdb2` invocation (and still render the `.vmdb`)
without building.

## Kali-specific host setup

Building Kali on a non-Kali Debian host requires the keyring and a debootstrap
script:

```bash
apt install ./kali-archive-keyring_*.deb
cd /usr/share/debootstrap/scripts
sed -e "s/debian-archive-keyring.gpg/kali-archive-keyring.gpg/g" sid > kali
ln -s kali kali-rolling
```

## Sanity checks after a build

```bash
qemu-img info foo.qc2                                     # format, virtual size, compression
guestfish --ro -a foo.qc2 -i ls /etc/phenix/startup       # phenix base scripts present (guestfish is in the container)
virt-ls -a foo.qc2 /usr/local/bin                         # host alternative; needs guestfs-tools
phenix image inject-miniexe /phenix/miniccc foo.qc2
```

`inject-miniexe` uses minimega's `disk inject`, so minimega must be reachable from
the phenix process running the command.
