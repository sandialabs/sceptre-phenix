# Virtual Disk Images Management

This is only available from the command line binary at this time.

## Listing disk images

```shell
phenix image list
```

## Creating a disk image

The [`vmdb2` utility](https://gitlab.com/glattercj/vmdb2) is required -- in path
-- to create the disk images. This utility is included with the phenix Docker image.

```shell
phenix image create <image name>
```

The `phenix image create --help` will output:

```text
Create a disk image configuration

  Used to create a virtual disk image configuration from which to build
	an image.

	When specifying the --size option, the following units can be used:

	M - Megabytes
	G - Gigabytes

Usage:
  phenix image create <image name> [flags]

Examples:

  phenix image create <image name>
  phenix image create --size 2G --variant mingui --release noble --compress --overlays foobar --packages foo --scripts bar <image name>

Flags:
  -l, --components string    List of components from the mirror to download packages from (separated by comma)
  -c, --compress             Compress image after creation (does not apply to raw image)
  -f, --format string        Format of disk image (default "qcow2")
  -h, --help                 help for create
  -k, --kernel-args string   List of parameters which grub will pass to the Linux kernel (e.g. 'net.ifnames=0','consoleblank=0'); separated by comma)
  -m, --mirror string        Debootstrap mirror (must match release) (default "http://us.archive.ubuntu.com/ubuntu")
      --no-virtuals          Don't add virtual filesystem mounts to chroot before executing scripts when running vmdb2 (default is 'false')
  -O, --overlays string      List of overlay names (include full path; separated by comma)
  -P, --packages string      List of packages to include in addition to those provided by variant (separated by comma)
  -R, --ramdisk              Create a kernel/initrd pair in addition to a disk image
  -r, --release string       OS release codename (default "jammy")
  -T, --scripts string       List of scripts to include in addition to the defaults (include full path; separated by comma)
  -s, --size string          Image size to use (default "10G")
      --skip-default-pkgs    Skip default packages typically included in all builds
  -v, --variant string       Image variant to use (default "minbase")
```

The `vmdb2` configuration file can be read by running the following command:

```shell
phenix cfg get image/<image name>
```

## Building a disk image

Building a disk image requires an existing configuration in the store
(i.e., the `create` command should be run first to create a configuration);
running `phenix image build --help` will output:

```text
Build a virtual disk image

  Used to build a new virtual disk using an existing configuration; vmdb2 must
  be in path.

Usage:
  phenix image build <configuration name> [flags]

Examples:

  phenix image build <configuration name>
  phenix image build --very-verbose --output </path/to/dir/>

Flags:
  -c, --cache           Cache rootfs as tar archive
      --dry-run         Do everything but actually call out to vmdb2
  -h, --help            help for build
  -o, --output string   Specify the output directory for the disk image to be saved to
  -v, --verbose         Enable verbose output
  -x, --very-verbose    Enable very verbose output, additionally writes output log file to <image name>.log
```

## Miscellaneous Commands

### append

The disk image management tool will allow you to add packages, overlays,
and scripts to existing configurations using the `append` command.
Command usage is:

```shell
phenix image append <configuration name> [flags]
```

Flags are for the overlays, packages, and scripts that you want to append.

### create-from

Run this command if you have an existing configuration that you would like
to use as the base to create a new configuration from. The usage involves
referencing the existing configuration, the new configuration name, and
then additional packages, overlays, and scripts.

```shell
phenix image create-from <existing configuration> <new configuration> [flags]
```

Flags are for the overlays, packages, and scripts that you want to add to
the new configuration.

### delete

```shell
phenix image delete <image name>
```

An alternative could be to use the configuration management tool.

```shell
phenix cfg delete image/<image name>
```

### remove

The `remove` command will allow you to remove any packages, overlays,
and scripts from an existing image configuration.

```shell
phenix image remove <configuration name> [flags]
```

Flags are for the overlays, packages, and scripts that you want to remove.

### update

This `update` command is used to update the script on an existing image
configuration. The path to a script is tracked in the code. The image
configuration gets updated with the script in the path; if no changes were
made no harm. If the path no longer exists, phenix will leave the
configuration alone.

```shell
phenix image update <configuration name>
```

## Kali Image

The Docker image for phenix includes everything needed to build a Kali image. If
phenix is installed locally, the following will be needed to create and build a
Kali image.

To build a Kali release on a non-Kali (but still Debian-based) operating system,
the following steps must be taken to prepare the host (Debian-based) OS first.
These steps are based on the official Kali documentation located at:

<https://www.kali.org/tutorials/build-kali-with-live-build-on-debian-based-systems/>.

1. Download and install the latest version of the Kali archive keyring package.
   At time of writing, the latest version was 2020.2.

```shell
wget http://http.kali.org/kali/pool/main/k/kali-archive-keyring/kali-archive-keyring_2020.2_all.deb
sudo dpkg -i kali-archive-keyring_2020.2_all.deb
```

2. Next, create the `debootstrap` build script for Kali, based entirely off the
   existing Debian Sid build script. Note that the following commands will
   likely need to be run as root.

```shell
cd /usr/share/debootstrap/scripts
sed -e "s/debian-archive-keyring.gpg/kali-archive-keyring.gpg/g" sid > kali
ln -s kali kali-rolling
```

At this point, you should be able to build a Kali release with `phenix image`.
