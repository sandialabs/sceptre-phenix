# Update GitHub workflow config to change the minimum version of minimega
# required for use with phenix. This build stage is needed for making sure the
# latest version of the minimega Python module is installed.
ARG  MM_MIN_REV=2bd71c0
FROM ghcr.io/activeshadow/minimega/minimega:${MM_MIN_REV} AS minimega


FROM node:14.21.3 AS jsbuilder

ARG INSTALL_CERTS=
RUN ["/bin/bash", "-c", "if [ -n $INSTALL_CERTS ]; then \
  IFS=',' read -r -a arr <<< $INSTALL_CERTS; \
  for i in ${!arr[@]}; do \
    wget ${arr[$i]} -e use_proxy=no \
      -O /usr/local/share/ca-certificates/custom$i.crt; \
  done && \
  update-ca-certificates; \ 
  yarn config set cafile /etc/ssl/certs/ca-certificates.crt; fi"]

RUN npm install -g @vue/cli redoc-cli

COPY ./src/js /phenix/src/js

WORKDIR /phenix/src/js

ARG PHENIX_WEB_AUTH=disabled
ARG PHENIX_BASE_PATH=/

ENV VUE_APP_AUTH ${PHENIX_WEB_AUTH}
ENV VUE_BASE_PATH ${PHENIX_BASE_PATH}

RUN make dist/index.html

COPY ./src/go/web/public/docs/openapi.yml /phenix/src/go/web/public/docs/openapi.yml

WORKDIR /phenix/src/go/web/public/docs

RUN npx redoc-cli build openapi.yml -o index.html --title 'phenix API'


FROM golang:1.20.7 AS gobuilder

RUN apt update \
  && apt install -y protobuf-compiler xz-utils

COPY .git       /phenix/.git
COPY ./Makefile /phenix/Makefile
COPY ./src/go   /phenix/src/go

WORKDIR /phenix

COPY --from=jsbuilder /phenix/src/js /phenix/src/js
COPY --from=jsbuilder /phenix/src/go/web/public/docs/index.html /phenix/src/go/web/public/docs/index.html

ARG INSTALL_CERTS=
RUN ["/bin/bash", "-c", "if [ -n $INSTALL_CERTS ]; then \
  IFS=',' read -r -a arr <<< $INSTALL_CERTS; \
  for i in ${!arr[@]}; do \
    wget ${arr[$i]} -e use_proxy=no \
      -O /usr/local/share/ca-certificates/custom$i.crt; \
  done && \
  update-ca-certificates; fi"]

ARG PHENIX_COMMIT
ARG PHENIX_TAG

RUN COMMIT=${PHENIX_COMMIT} TAG=${PHENIX_TAG} make bin/phenix
RUN make -C src/go phenix-tunneler

ARG APPS_REPO=sandialabs/sceptre-phenix-apps
ARG APPS_BRANCH=main

RUN git clone --branch ${APPS_BRANCH} https://github.com/${APPS_REPO}.git /phenix-apps

WORKDIR /phenix-apps/src/go

RUN CGO_ENABLED=0 GOOS=linux go install -trimpath ./...


FROM ubuntu:22.04

ENV TZ=Etc/UTC
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# tshark needed for scorch tcpdump component
RUN apt update \
  && echo 'wireshark-common wireshark-common/install-setuid boolean false' | debconf-set-selections \
  && apt install -y cpio debootstrap git iproute2 iputils-ping locales nano pigz python3-pip tshark vim vmdb2 \
  && locale-gen en_US.UTF-8 \
  && apt autoremove -y \
  && apt clean -y \
  && rm -rf /var/lib/apt/lists/* \
  && rm -rf /var/cache/apt/archives/*

ENV LANG   en_US.UTF-8
ENV LC_ALL en_US.UTF-8

ARG INSTALL_CERTS=
RUN ["/bin/bash", "-c", "if [ -n $INSTALL_CERTS ]; then \
  IFS=',' read -r -a arr <<< $INSTALL_CERTS; \
  for i in ${!arr[@]}; do \
    wget ${arr[$i]} -e use_proxy=no \
      -O /usr/local/share/ca-certificates/custom$i.crt; \
  done && \
  update-ca-certificates; \
  fi"]

RUN wget https://github.com/glattercj/vmdb2/releases/download/v1.1/vmdb2 -O /usr/bin/vmdb2 \
  && chmod +x /usr/bin/vmdb2

# do this before installing phenix-apps so minimega package is latest version
COPY --from=minimega /opt/minimega/lib /tmp/minimega
RUN  python3 -m pip install /tmp/minimega

ARG APPS_REPO=sandialabs/sceptre-phenix-apps
ARG APPS_BRANCH=main

RUN python3 -m pip install \
  --trusted-host pypi.org \
  --trusted-host files.pythonhosted.org \
  "git+https://github.com/${APPS_REPO}.git@${APPS_BRANCH}#egg=phenix-apps&subdirectory=src/python"

# needed to build Kali images with phenix
RUN wget -O kali.deb https://http.kali.org/kali/pool/main/k/kali-archive-keyring/kali-archive-keyring_2022.1_all.deb \
	&& dpkg -i kali.deb && rm kali.deb

# used by scorch
RUN wget -O glow.tgz https://github.com/charmbracelet/glow/releases/download/v1.5.0/glow_1.5.0_Linux_x86_64.tar.gz \
  && tar -xzf glow.tgz glow && mv glow /usr/local/bin/glow && rm glow.tgz

# used by scorch
RUN wget -O filebeat.deb https://artifacts.elastic.co/downloads/beats/filebeat/filebeat-7.17.5-amd64.deb \
	&& dpkg -i filebeat.deb && rm filebeat.deb

COPY --from=gobuilder /phenix/bin/phenix   /usr/local/bin/phenix
COPY --from=gobuilder /go/bin/phenix-app-* /usr/local/bin

COPY --from=gobuilder /phenix/src/go/bin/phenix-tunneler-* /opt/phenix/downloads/tunneler/

# Update GitHub workflow config to change the minimum version of minimega
# required for use with phenix.
ARG   MM_MIN_REV=2b2b341
LABEL gov.sandia.phenix.minimega-min-revision="ghcr.io/sandialabs/sceptre-phenix/minimega:${MM_MIN_REV}"

WORKDIR /opt/phenix

CMD ["phenix", "help"]
