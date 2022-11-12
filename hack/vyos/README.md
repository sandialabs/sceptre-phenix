# VyOS Image Builder

The `packer-build.sh` script can be used to easily build a `vyos.qc2` VM image
using the source code from the latest stable branch (currently `v1.3.x` -
Equuleus).

The script first uses Docker to build the ISO from source, then uses Packer to
build a qcow2 VM image from the ISO. When building the VM image, the Packer
config uses `vyos` as image name, which is important when phenix injects the
boot config into the image since the image name is part of the injection path.
The VyOS source code includes its own Packer template and a make target for
building a qcow2 VM image using Packer, but the Packer template included here
makes a few important changes (like using `vyos` for the image name as described
above).

The `packer.json` config in this directory was inspired by both
[vyos.pkr.hcl](https://github.com/camjjack/hyper-v-packer-templates/blob/master/vyos.pkr.hcl)
and the Packer config present in the official `vyos-build` repository.

## Requirements

* git
* QEMU/KVM
* Docker
* Packer

While the `packer-build.sh` script checks for Docker and Packer, it doesn't
check for additional required dependencies (mainly, git and QEMU/KVM).

> While not tested, it's likely that building the VyOS image on macOS will not
> work.

## miniccc Agent

The Packer config includes a provisioner script for installing the miniccc agent
into the VyOS VM image. In order for this script to be applied successfully, you
must place a copy of the `miniccc` agent executable in the `http` directory
prior to running the build script. If you don't want to install the miniccc
agent then you can remove the `{{template_dir}}/scripts/miniccc.sh` line from
the Packer config.

> Using `phenix image inject-miniexe` will not work with the VyOS image as built
> by this script due to the differences in VyOS's image filesystem layout.
