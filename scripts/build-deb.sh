#!/bin/bash
set -e

# This script builds the phenix docker image and extracts all artifacts
# needed to build a native .deb package.

# Ensure we're in the project root
cd "$(dirname "$0")/.."
PROJECT_ROOT=$(pwd)

if ! command -v dpkg-deb &> /dev/null; then
    echo "Error: dpkg-deb is not installed. Are you on a Debian/Ubuntu system?"
    echo "You can install it with: sudo apt-get install dpkg"
    exit 1
fi

BUILD_DIR=${PROJECT_ROOT}/build/deb
ARTIFACTS_DIR=${BUILD_DIR}/artifacts
DEBIAN_DIR=${BUILD_DIR}/debian

echo "Starting .deb package build process..."

BUILD_IMAGE=${PHENIX_BUILD_IMAGE:-phenix-build-env}

if [ -z "${PHENIX_BUILD_IMAGE}" ]; then
    # 1. Build the phenix docker image
    # This image will contain all the binaries, scripts, and dependencies.
    echo "Building phenix docker image..."
    docker build -t ${BUILD_IMAGE} -f docker/Dockerfile .
else
    echo "Using provided docker image: ${BUILD_IMAGE}..."
fi

# 2. Create the artifact and debian packaging directories
echo "Creating packaging directories..."
rm -rf ${ARTIFACTS_DIR} ${DEBIAN_DIR}
mkdir -p ${ARTIFACTS_DIR}
mkdir -p ${DEBIAN_DIR}

# 3. Extract artifacts from the docker image
echo "Extracting artifacts from the docker image..."
CONTAINER_ID=$(docker create ${BUILD_IMAGE})

# Function to copy artifacts and handle errors
copy_artifact() {
    echo "  - Copying $1..."
    docker cp ${CONTAINER_ID}:$1 $2 || echo "  - WARNING: $1 not found."
}

# Binaries
copy_artifact "/usr/local/bin/phenix" "${ARTIFACTS_DIR}/phenix"
copy_artifact "/usr/local/bin/glow" "${ARTIFACTS_DIR}/glow"

# Tunneler binaries
mkdir -p ${ARTIFACTS_DIR}/tunneler
copy_artifact "/opt/phenix/downloads/tunneler/." "${ARTIFACTS_DIR}/tunneler/"

# phenix-app-* binaries
APPS=$(docker run --rm ${BUILD_IMAGE} find /usr/local/bin -name "phenix-app-*")
for app in $APPS; do
    copy_artifact "$app" "${ARTIFACTS_DIR}/"
done

# Filebeat
copy_artifact "/etc/filebeat" "${ARTIFACTS_DIR}/filebeat_etc"
copy_artifact "/usr/share/filebeat" "${ARTIFACTS_DIR}/filebeat_usr_share"
copy_artifact "/usr/bin/filebeat" "${ARTIFACTS_DIR}/filebeat_bin" || true

# Python packages
SITE_PACKAGES=$(docker run --rm ${BUILD_IMAGE} python3 -c "import site; print(site.getsitepackages()[0])")
mkdir -p "${ARTIFACTS_DIR}/python_pkgs"
copy_artifact "${SITE_PACKAGES}/phenix_apps" "${ARTIFACTS_DIR}/python_pkgs/phenix_apps"
copy_artifact "${SITE_PACKAGES}/minimega.py" "${ARTIFACTS_DIR}/python_pkgs/minimega.py"

# Also copy the git repository for vmdb2
copy_artifact "/opt/vmdb2" "${ARTIFACTS_DIR}/vmdb2_repo"

# 4. Clean up the container
echo "Cleaning up docker container..."
docker rm ${CONTAINER_ID}

echo "Artifacts extracted to ${ARTIFACTS_DIR}"

# Make sure the main phenix binary is executable
chmod +x ${ARTIFACTS_DIR}/phenix

# 5. Build the debian package directory structure
echo "Building debian package structure..."
mkdir -p ${DEBIAN_DIR}/DEBIAN
mkdir -p ${DEBIAN_DIR}/usr/local/bin
mkdir -p ${DEBIAN_DIR}/usr/bin
mkdir -p ${DEBIAN_DIR}/opt/phenix/downloads/tunneler
mkdir -p ${DEBIAN_DIR}/etc/filebeat
mkdir -p ${DEBIAN_DIR}/usr/share/filebeat
mkdir -p ${DEBIAN_DIR}/usr/lib/python3/dist-packages
mkdir -p ${DEBIAN_DIR}/opt/vmdb2

# Copy artifacts into the debian package structure
cp ${ARTIFACTS_DIR}/phenix ${DEBIAN_DIR}/usr/local/bin/
cp ${ARTIFACTS_DIR}/glow ${DEBIAN_DIR}/usr/local/bin/
cp ${ARTIFACTS_DIR}/phenix-app-* ${DEBIAN_DIR}/usr/local/bin/ 2>/dev/null || true
cp -a ${ARTIFACTS_DIR}/tunneler/. ${DEBIAN_DIR}/opt/phenix/downloads/tunneler/

cp -a ${ARTIFACTS_DIR}/filebeat_etc/. ${DEBIAN_DIR}/etc/filebeat/
cp -a ${ARTIFACTS_DIR}/filebeat_usr_share/. ${DEBIAN_DIR}/usr/share/filebeat/

if [ -f "${ARTIFACTS_DIR}/filebeat_bin" ]; then
    cp ${ARTIFACTS_DIR}/filebeat_bin ${DEBIAN_DIR}/usr/bin/filebeat
    chmod 755 ${DEBIAN_DIR}/usr/bin/filebeat
fi

cp -a ${ARTIFACTS_DIR}/python_pkgs/. ${DEBIAN_DIR}/usr/lib/python3/dist-packages/

cp -a ${ARTIFACTS_DIR}/vmdb2_repo/. ${DEBIAN_DIR}/opt/vmdb2/
ln -s /opt/vmdb2/vmdb2 ${DEBIAN_DIR}/usr/bin/vmdb2

# Create DEBIAN/control file
VERSION=$(docker run --rm ${BUILD_IMAGE} phenix version 2>/dev/null | grep "Version:" | awk '{print $2}' || echo "1.0.0")
VERSION=${VERSION:-1.0.0}
# Strip leading 'v' if present
VERSION=${VERSION#v}

# If we are in a git repository, append the short commit hash to the version
# to distinguish builds, especially useful for forks and CI/CD.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    SHORT_SHA=$(git rev-parse --short HEAD)
    # Use ~ to denote a pre-release or + for build metadata in debian versions
    VERSION="${VERSION}+git${SHORT_SHA}"
fi

cat <<EOF > ${DEBIAN_DIR}/DEBIAN/control
Package: phenix
Version: ${VERSION}
Architecture: amd64
Maintainer: Sandia National Laboratories <emulytics@sandia.gov>
Description: phēnix is an automated experimentation, emulation, and orchestration platform.
Depends: wireshark-common, cmdtest, cpio, debootstrap, git, iproute2, iputils-ping, kpartx, parted, psmisc, python3, python3-jinja2, python3-pip, python3-yaml, qemu-utils, tshark, wget, xz-utils, zerofree, guestfish
EOF

chmod 755 ${DEBIAN_DIR}/DEBIAN
chmod 644 ${DEBIAN_DIR}/DEBIAN/control

# 6. Build the .deb package
echo "Building .deb package..."
dpkg-deb --build ${DEBIAN_DIR} ${BUILD_DIR}/phenix_${VERSION}_amd64.deb

echo "Done! The .deb package is located at ${BUILD_DIR}/phenix_${VERSION}_amd64.deb"
