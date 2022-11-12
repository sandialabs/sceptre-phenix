#!/bin/bash

which docker &> /dev/null

if (( $? )); then
  echo "Docker must be installed (and in your PATH) to use this build script. Exiting."
  exit 1
fi

which packer &> /dev/null

if (( $? )); then
  echo "Packer must be installed (and in your PATH) to use this build script. Exiting."
  exit 1
fi

if [[ -f vyos-build/build/live-image-amd64.hybrid.iso ]]; then
  echo "VyOS ISO file already exists, so not rebuilding"
  echo "If you want to rebuild the ISO, please delete the 'vyos-build/build' directory"
else
  git clone -b equuleus --single-branch https://github.com/vyos/vyos-build

  docker run -it --rm \
    -v $(pwd):/vyos \
    -w /vyos/vyos-build \
    --privileged \
    -e GOSU_UID=$(id -u) \
    -e GOSU_GID=$(id -g) \
    vyos/vyos-build:equuleus /bin/sh -c "./configure --architecture amd64 && sudo make iso"
fi

export ISO_IMAGE=vyos-build/build/live-image-amd64.hybrid.iso
export ISO_MD5SUM="$(md5sum ${ISO_IMAGE} | awk '{print $1}')"

packer build packer.json
