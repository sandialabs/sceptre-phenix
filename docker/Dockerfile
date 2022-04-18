FROM node:12.18.3 AS jsbuilder

RUN npm install -g @vue/cli redoc-cli

COPY ./src/js /phenix/src/js

WORKDIR /phenix/src/js

ARG PHENIX_WEB_AUTH=disabled
ARG PHENIX_BASE_PATH=/

ENV VUE_APP_AUTH ${PHENIX_WEB_AUTH}
ENV VUE_BASE_PATH ${PHENIX_BASE_PATH}

RUN npm install \
  && npm run build

COPY ./src/go/web/public/docs/openapi.yml /phenix/src/go/web/public/docs/openapi.yml

WORKDIR /phenix/src/go/web/public/docs

RUN npx redoc-cli bundle openapi.yml -o index.html --title 'phenix API'


FROM golang:1.18 AS gobuilder

RUN apt update \
  && apt install -y protobuf-compiler xz-utils

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

ARG PHENIX_VERSION
ARG PHENIX_COMMIT

RUN VER=${PHENIX_VERSION} COMMIT=${PHENIX_COMMIT} make bin/phenix

RUN wget https://github.com/glattercj/vmdb2/releases/download/v1.0/vmdb2 -P bin/ \
  && chmod +x bin/vmdb2

RUN git clone --branch main https://github.com/sandia-minimega/phenix-apps.git /phenix-apps

WORKDIR /phenix-apps/src/go

RUN CGO_ENABLED=0 GOOS=linux go install -trimpath ./...


FROM ubuntu:20.04

ENV TZ=Etc/UTC
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# tshark needed for scorch tcpdump component
RUN apt update \
  && echo 'wireshark-common wireshark-common/install-setuid boolean false' | debconf-set-selections \
  && apt install -y cpio debootstrap git locales nano python3-pip tshark vim vmdb2 \
  && locale-gen en_US.UTF-8 \
  && apt autoremove -y \
  && apt clean -y \
  && rm -rf /var/lib/apt/lists/* \
  && rm -rf /var/cache/apt/archives/*

ENV LANG   en_US.UTF-8
ENV LC_ALL en_US.UTF-8

RUN python3 -m pip install --trusted-host pypi.org --trusted-host files.pythonhosted.org "git+https://github.com/sandia-minimega/phenix-apps.git@main#egg=phenix-apps&subdirectory=src/python"

# needed to build Kali images with phenix
RUN wget -O kali.deb https://http.kali.org/kali/pool/main/k/kali-archive-keyring/kali-archive-keyring_2022.1_all.deb \
	&& dpkg -i kali.deb && rm kali.deb

# used by scorch
RUN wget -O filebeat.deb https://artifacts.elastic.co/downloads/beats/filebeat/filebeat-7.14.2-amd64.deb \
	&& dpkg -i filebeat.deb && rm filebeat.deb

COPY --from=gobuilder /phenix/bin/phenix   /usr/local/bin/phenix
COPY --from=gobuilder /phenix/bin/vmdb2    /usr/bin/vmdb2
COPY --from=gobuilder /go/bin/phenix-app-* /usr/local/bin

CMD ["phenix", "help"]
