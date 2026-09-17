# Build Scripts and Overlays

## Execution model

Every script in `spec.scripts` is concatenated, in `spec.script_order`, into a single
vmdb2 step:

```yaml
- chroot: root
  shell: |
    <script 1>
    <script 2>
    ...
```

vmdb2 executes that block as `chroot <mountpoint> sh -ec "<block>"`. Practical
consequences:

- The interpreter is `/bin/sh` (dash on Debian/Ubuntu). Shebang lines are comments.
  Bashisms — `[[ ]]`, arrays, `source`, `function`, `set -o pipefail`, `${x^^}` —
  are syntax errors that abort the build.
- `-e` is already in effect for the whole block; the first non-zero exit kills the
  build, even in a script you did not write. Guard expected failures with
  `|| true`.
- `exit` terminates the entire block, silently skipping all later scripts.
- State is shared: `cd`, shell variables, exported env, and started daemons persist
  from one script into the next.
- phenix strips every blank line when it inlines a script into the `.vmdb` (see
  `Image.PostBuild`). Heredoc bodies lose their empty lines; emit them with
  `printf '\n' >> file` if a config format needs them.
- The chroot is the target rootfs, running as root, after debootstrap, after apt
  package installation, and after overlays are copied.
- `/proc`, `/dev`, `/dev/pts`, `/dev/shm`, `/run`, `/sys`, and `/sys/fs/cgroup`
  are mounted (unless `no_virtuals: true`), so `systemctl`, `service`, and even
  `dockerd` work during the build.
- The host's `/etc/resolv.conf` is copied into the chroot for the duration of the
  build (skipped with `no_virtuals`), so outbound network access and DNS follow the
  build host; upstream scripts `wget`/`curl`/`pip` freely.
- No phenix metadata is exposed: there is no experiment name, VM name, or topology
  at build time. Only environment variables you set yourself (upstream convention:
  `UBUNTU_MIRROR`, `DEBIAN_FRONTEND`, `LC_ALL`, `PIP_DISABLE_PIP_VERSION_CHECK`).

## Ordering

`SetupImage` builds `script_order` as:

1. `POSTBUILD_GUI` or `POSTBUILD_KALI_GUI` (mingui variant only)
2. `POSTBUILD_APT_CLEANUP`
3. `POSTBUILD_NO_ROOT_PASSWD`
4. `POSTBUILD_PHENIX_HOSTNAME`
5. `POSTBUILD_PHENIX_BASE`
6. every `--scripts` entry, left to right

`append` / `create-from` push additional scripts onto the end, so user scripts always
run after the phenix defaults and can override them (hostname, root password, systemd
units, MOTD). Nothing cleans apt caches after user scripts — do it yourself if size
matters.

`update` re-appends each refreshed script at the end of `script_order`, iterating a
map, so refreshing several scripts at once leaves their relative order random.
`remove -T` deletes the script body but not its `script_order` entry; a following
`append -T` of the same path adds a second entry and the script runs twice. Check
`script_order` with `phenix config get image/<name>` after either command.

Scripts may be local paths or `http://` / `https://` URLs; any other scheme errors.
Content is downloaded/read at `create` time and inlined into the config.

## Overlays

`--overlays` entries are absolute directory paths, each rendered as a
`copy-dir: /` step and copied onto the image root *before* the chroot scripts run.
Use them for config files, systemd drop-ins, and static assets. vmdb2's `copy-dir`
writes every file and directory as `root:root` with the source mode bits masked by
`022`, so host ownership does not leak in; the `chown -R root:root /etc` at the end
of upstream scripts is a leftover safety net. Executable bits and `0600` files are
preserved, but setuid/sgid and group-writable bits are not.

Upstream layout (`sandialabs/sceptre-phenix-images`):

```text
overlays/
  bennu/etc/collectd/...
  brash/etc/{issue,pam.d,sceptre/brash}/...
  minirouter/etc/systemd/system/miniccc.service.d/override.conf
scripts/
  atomic/{docker.sh,proxy.sh,ubuntu-user.sh}   # small reusable fragments
  bennu.sh  minirouter.sh  ot-sim.sh  soaptools.sh  kali-harmonie.sh
  vyos/build-vyos.sh                            # non-debootstrap special case
```

## Script patterns

Header used by upstream scripts:

```sh
#!/bin/sh
set -ex
export DEBIAN_FRONTEND=noninteractive
export LC_ALL=C
export PIP_DISABLE_PIP_VERSION_CHECK=1
```

Idempotent user creation (`scripts/atomic/ubuntu-user.sh`):

```sh
useradd --create-home --shell /bin/bash ubuntu || true
usermod -aG sudo ubuntu
echo 'ubuntu   ALL=(ALL:ALL) NOPASSWD: ALL' > /etc/sudoers.d/ubuntu
echo 'ubuntu:ubuntu' | chpasswd
```

Service tweaks for minimega routing (`scripts/minirouter.sh`) — the actual binaries
arrive later via `inject-miniexe`:

```sh
apt remove --purge -y systemd-resolved   # conflicts with dnsmasq
systemctl disable dnsmasq
systemctl disable bird
echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/11-ip-forwarding.conf
```

Running a daemon during the build (`scripts/atomic/docker.sh`):

```sh
curl -fsSL get.docker.com | bash
sed -i -e 's/ulimit -Hn/ulimit -n/g' /etc/init.d/docker
DOCKER_RAMDISK=true /etc/init.d/docker start
while [ ! -S /var/run/docker.sock ]; do sleep 1; done
# docker pull/build now works inside the image build
```

Installing releases and fixing overlay ownership (`scripts/bennu.sh`):

```sh
export UBUNTU_MIRROR="${UBUNTU_MIRROR:-"http://archive.ubuntu.com/ubuntu/"}"
apt-get install -y --no-install-recommends libzmq5-dev collectd python3-pip socat
wget https://github.com/sandialabs/sceptre-bennu/releases/latest/download/bennu.deb -O /tmp/bennu.deb
apt-get install -y /tmp/bennu.deb
adduser sceptre --UID 1001 --gecos "" --shell /usr/bin/bennu-brash --disabled-login || true
echo "sceptre:sceptre" | chpasswd
chown -R root:root /etc
```

Behind a proxy, adapt `scripts/atomic/proxy.sh` (placeholders must be filled in) and
list it first so later scripts inherit the exported proxy vars and CA certificates.

## Build-time vs runtime

`POSTBUILD_PHENIX_BASE` installs `phenix.service`, which runs every file in
`/etc/phenix/startup/` on each boot via `/usr/local/bin/phenix-start.sh`, and
`miniccc.service`, which talks to minimega over `/dev/virtio-ports/cc`. Anything that
depends on the experiment (hostname, interfaces, injected files) belongs in
`/etc/phenix/startup/` — written by topology/scenario apps at launch — not in a build
script.

When disk injection is bypassed or unavailable, the startup app instead delivers
those scripts over minimega C2 and runs them from `/tmp/miniccc/files`, rather than
writing to `/etc/phenix/startup/`. That path is taken when the node is annotated
`phenix/startup-via-cc` or when the first drive sets `inject_partition: 0`
(typical for LVM images). Such images still need a working `miniccc` — one more
reason `inject-miniexe` is mandatory.
