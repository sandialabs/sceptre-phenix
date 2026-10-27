# check=skip=SecretsUsedInArgOrEnv

# Builds the phenix image with Docker or Podman. Base images are fully
# qualified so Podman doesn't need short-name resolution.
#
# Steps are ordered so the layer cache survives a normal code change: tools,
# dependencies and external downloads come before the phenix source, and
# per-commit build args are declared as late as possible (an ARG invalidates
# every RUN that follows it when its value changes).

# Update GitHub workflow config to change the minimum version of minimega
# required for use with phenix. This build stage is needed for making sure the
# latest version of the minimega Python module is installed.
ARG  MM_MIN_REV=master
FROM ghcr.io/sandia-minimega/minimega:${MM_MIN_REV} AS minimega


# ** jsbuilder **
FROM docker.io/library/node:24.22.1 AS jsbuilder

ARG INSTALL_CERTS=
COPY docker/install-certs.sh /tmp/install-certs.sh
RUN /tmp/install-certs.sh && \
  npm config set cafile /etc/ssl/certs/ca-certificates.crt

RUN npm install -g @redocly/cli@2.62.1

WORKDIR /phenix/src/js

COPY src/js/package.json src/js/package-lock.json ./
RUN npm ci --no-fund --no-audit

COPY src/js ./

ARG PHENIX_WEB_AUTH=disabled
ARG PHENIX_BASE_PATH=/

ENV VITE_AUTH=${PHENIX_WEB_AUTH}
ENV VITE_BASE_PATH=${PHENIX_BASE_PATH}

RUN npm run build

COPY src/go/web/public/docs/openapi.yml /phenix/src/go/web/public/docs/openapi.yml

WORKDIR /phenix/src/go/web/public/docs

RUN redocly build-docs openapi.yml -o index.html --title 'phenix API'


# ** appsbuilder **
FROM docker.io/library/golang:1.24.13 AS appsbuilder

ARG INSTALL_CERTS=
COPY docker/install-certs.sh /tmp/install-certs.sh
RUN /tmp/install-certs.sh

# Allow installing from a fork of sceptre-phenix-apps that could
# exist on GitHub or elsewhere.
# We can specify one APPS_REPO and APPS_BRANCH for both Go and Python
ARG APPS_REPO=github.com/sandialabs/sceptre-phenix-apps
ARG APPS_BRANCH=main
# Or the user can override Go and Python apps repos/branches separately
ARG GO_APPS_REPO=${APPS_REPO}
ARG GO_APPS_BRANCH=${APPS_BRANCH}
# Optional commit to build instead of the branch head. CI passes the resolved
# head so the cached layer is rebuilt when the branch moves.
ARG APPS_REF=
ARG GO_APPS_REF=${APPS_REF}

RUN git clone --branch ${GO_APPS_BRANCH} https://${GO_APPS_REPO}.git /phenix-apps \
  && git -C /phenix-apps checkout --detach "${GO_APPS_REF:-HEAD}"

WORKDIR /phenix-apps/src/go

RUN CGO_ENABLED=0 GOOS=linux go install -trimpath ./...


# ** gobuilder **
FROM docker.io/library/golang:1.24.13 AS gobuilder

RUN apt-get update \
  && apt-get install -y protobuf-compiler xz-utils

ARG INSTALL_CERTS=
COPY docker/install-certs.sh /tmp/install-certs.sh
RUN /tmp/install-certs.sh

# The build reads version info from the bind-mounted .git, whose owner
# differs from the build user.
RUN git config --global --add safe.directory '*'

WORKDIR /phenix/src/go

COPY src/go/go.mod src/go/go.sum ./
RUN go mod download

WORKDIR /phenix

COPY Makefile ./
COPY src/go ./src/go

RUN make generate

COPY --from=jsbuilder /phenix/src/js/dist ./src/js/dist
COPY --from=jsbuilder /phenix/src/go/web/public/docs/index.html ./src/go/web/public/docs/index.html

# Fall back to the bind-mounted .git for any of these left unset. A git
# worktree's .git is only a pointer file, so pass all three when building from one.
ARG PHENIX_COMMIT
ARG PHENIX_TAG
ARG PHENIX_REPO

# Build phenix binary (skip UI build since artifacts are already copied)
RUN --mount=type=bind,source=.git,target=/phenix/.git \
  COMMIT=${PHENIX_COMMIT} TAG=${PHENIX_TAG} REPO=${PHENIX_REPO} make bin/phenix SKIP_UI=1
RUN --mount=type=bind,source=.git,target=/phenix/.git \
  COMMIT=${PHENIX_COMMIT} make -C src/go phenix-tunneler


# ** Build the phenix image **
FROM docker.io/library/ubuntu:24.04

# General image metadata
# Reference: https://github.com/opencontainers/image-spec/blob/main/annotations.md
LABEL org.opencontainers.image.authors="Sandia National Laboratories <emulytics@sandia.gov>, Bryan Richardson <bryan@activeshadow.com>" \
  org.opencontainers.image.documentation="https://phenix.sceptre.dev/" \
  org.opencontainers.image.source="https://github.com/sandialabs/sceptre-phenix" \
  org.opencontainers.image.vendor="Sandia National Laboratories" \
  org.opencontainers.image.licenses="GPL-3.0-only" \
  org.opencontainers.image.title="phenix" \
  org.opencontainers.image.description="phenix is an orchestration tool and GUI for Sandia's minimega platform"

# Set timezone
ENV TZ="Etc/UTC"
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# Use bash with pipefail so a failed command earlier in a pipe isn't masked
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# tshark needed for scorch tcpdump component
RUN apt-get update \
  && echo 'wireshark-common wireshark-common/install-setuid boolean false' | debconf-set-selections \
  && apt-get install -y cmdtest cpio debootstrap git iproute2 iputils-ping kpartx locales nano parted psmisc python3 python3-jinja2 python3-pip python3-yaml qemu-utils tshark vim wget xz-utils zerofree guestfish \
  && locale-gen en_US.UTF-8 \
  && apt-get autoremove -y \
  && apt-get clean -y \
  && rm -rf /var/lib/apt/lists/* \
  && rm -rf /var/cache/apt/archives/*

ENV LANG="en_US.UTF-8" \
  LC_ALL="en_US.UTF-8" \
  PIP_DISABLE_PIP_VERSION_CHECK=1

ARG INSTALL_CERTS=
COPY docker/install-certs.sh /tmp/install-certs.sh
RUN /tmp/install-certs.sh

# used by scorch
ARG GLOW_VERSION=3.0.0
ARG GLOW_SHA256=13e05e4b2acc18d2aee44291aefe6325b077ec321b631a0cfa780e8e3bc33f78
RUN wget --progress=dot:giga -O glow.tgz \
  "https://github.com/charmbracelet/glow/releases/download/v${GLOW_VERSION}/glow_${GLOW_VERSION}_Linux_x86_64.tar.gz" \
  && echo "${GLOW_SHA256}  glow.tgz" | sha256sum -c - \
  && tar -xzf glow.tgz --strip-components=1 "glow_${GLOW_VERSION}_Linux_x86_64/glow" \
  && mv glow /usr/local/bin/glow \
  && rm glow.tgz

# used by scorch
ARG FILEBEAT_VERSION=7.17.29
ARG FILEBEAT_SHA512=9dedc942dd2c8cc1ae2b3728b130e68d9bf82e91b1815f25d4bf3249ddba3916ebb8c0dc0b7f6e9d9d65edb3f9ece0dba166b7d9e61ec625e71430aafede544d
RUN wget --progress=dot:giga -O filebeat.deb \
  "https://artifacts.elastic.co/downloads/beats/filebeat/filebeat-${FILEBEAT_VERSION}-amd64.deb" \
  && echo "${FILEBEAT_SHA512}  filebeat.deb" | sha512sum -c - \
  && dpkg -i filebeat.deb \
  && rm filebeat.deb

# needed to build Kali images with phenix
RUN wget --progress=dot:giga https://archive.kali.org/archive-keyring.gpg -O /usr/share/keyrings/kali-archive-keyring.gpg

# vmdb2 runs as root during image builds, so it is pinned to a reviewed commit
# that an upstream force-push cannot change. Override VMDB2_REF to try another.
ARG VMDB2_REF=255da2eb0767bf82e7736be869ea315b4619e6c6
RUN git clone https://gitlab.com/glattercj/vmdb2 /opt/vmdb2 \
  && git -C /opt/vmdb2 checkout --quiet --detach "${VMDB2_REF}" \
  && ln -s /opt/vmdb2/vmdb2 /usr/bin/vmdb2

# Allow the use of PyPI mirrors via build args
ARG PIP_INDEX="https://pypi.org/"
ARG PIP_INDEX_URL="https://pypi.org/simple"

# Do this before installing phenix-apps so minimega package is latest version
COPY --from=minimega /opt/minimega/lib /tmp/minimega
RUN python3 -m pip install --break-system-packages --no-cache-dir /tmp/minimega

# Allow installing from a fork of sceptre-phenix-apps that could
# exist on GitHub or elsewhere.
# We can specify one APPS_REPO and APPS_BRANCH for both Go and Python
ARG APPS_REPO=github.com/sandialabs/sceptre-phenix-apps
ARG APPS_BRANCH=main
# Or the user can override Go and Python apps repos/branches separately
ARG PY_APPS_REPO=${APPS_REPO}
ARG PY_APPS_BRANCH=${APPS_BRANCH}
# Optional commit to install instead of the branch head (see appsbuilder)
ARG APPS_REF=
ARG PY_APPS_REF=${APPS_REF}

# Install phenix user apps
RUN python3 -m pip install --break-system-packages --ignore-installed --no-cache-dir \
  "git+https://${PY_APPS_REPO}.git@${PY_APPS_REF:-${PY_APPS_BRANCH}}#egg=phenix-apps&subdirectory=src/python"

# Copy binaries from the build stages
COPY --from=appsbuilder /go/bin/phenix-app-* /usr/local/bin
COPY --from=gobuilder /phenix/bin/phenix   /usr/local/bin/phenix

COPY --from=gobuilder /phenix/src/go/bin/phenix-tunneler-* /opt/phenix/downloads/tunneler/

# Update GitHub workflow config to change the minimum version of minimega
# required for use with phenix.
ARG   MM_MIN_REV=master
ARG   PHENIX_COMMIT
LABEL gov.sandia.phenix.minimega-min-revision="ghcr.io/sandia-minimega/minimega:${MM_MIN_REV}" \
  org.opencontainers.image.revision="${PHENIX_COMMIT}"

WORKDIR /opt/phenix

CMD ["phenix", "help"]
