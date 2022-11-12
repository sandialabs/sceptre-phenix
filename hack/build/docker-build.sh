#!/bin/bash

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ROOT_DIR="$( cd ${SCRIPT_DIR}/../.. && pwd )"

usage="usage: $(basename "$0") [-b] [-d] [-h] [-v]

This script will build the phenix binary using a temporary Docker image to
avoid having to install build dependencies locally.

Note that the '-d' flag only disables authentication in the client-side UI
code when building it. To fully disable authentication, the 'phenix ui'
command must not be passed the '--jwt-signing-key' option at runtime.

If not provided, the '-v' flag will default to the hash of the current git
repository commit.

where:
    -b      base path for web UI deployment (defaults to '/')
    -d      disable phenix web UI authentication
    -h      show this help text
    -t      tag to use for phenix"


base=/
auth=enabled
commit=$(git log -1 --format="%h")
tag=$(git log -1 --format="%h")


# loop through positional options/arguments
while getopts ':b:dht:' option; do
    case "$option" in
        b)  base="$OPTARG"         ;;
        d)  auth=disabled          ;;
        h)  echo -e "$usage"; exit ;;
        t)  tag="$OPTARG"          ;;
        \?) echo -e "illegal option: -$OPTARG\n" >$2
            echo -e "$usage" >&2
            exit 1 ;;
    esac
done


echo    "phenix web UI base path:      $base"
echo    "phenix web UI authentication: $auth"
echo    "phenix commit:                $commit"
echo -e "phenix tag:                   $tag\n"


which docker &> /dev/null

if (( $? )); then
  echo "Docker must be installed (and in your PATH) to use this build script. Exiting."
  exit 1
fi


USER_UID=$(id -u)
USERNAME=builder


if (( $USER_UID == 0 )); then
  USERNAME=root
fi


docker build -t phenix:builder -f - . <<EOF
FROM ubuntu:20.04

RUN ["/bin/bash", "-c", "if (( $USER_UID != 0 )); then \
  groupadd --gid $USER_UID $USERNAME \
  && useradd -s /bin/bash --uid $USER_UID --gid $USER_UID -m $USERNAME; fi"]

RUN apt update && apt install -y curl gnupg2 make protobuf-compiler wget xz-utils

ENV GOLANG_VERSION 1.18.5

RUN wget -O go.tgz https://golang.org/dl/go\${GOLANG_VERSION}.linux-amd64.tar.gz \
  && tar -C /usr/local -xzf go.tgz && rm go.tgz

ENV GOPATH /go
ENV PATH \$GOPATH/bin:/usr/local/go/bin:\$PATH

RUN mkdir -p \$GOPATH/src \$GOPATH/bin \
  && chmod -R 777 \$GOPATH

ENV NODE_VERSION 12.18.3

RUN wget -O node.txz https://nodejs.org/dist/v\${NODE_VERSION}/node-v\${NODE_VERSION}-linux-x64.tar.xz \
  && tar -xJf node.txz -C /usr/local --strip-components=1 --no-same-owner && rm node.txz \
  && ln -s /usr/local/bin/node /usr/local/bin/nodejs

RUN npm install -g @vue/cli redoc-cli

RUN curl -sS https://dl.yarnpkg.com/debian/pubkey.gpg | apt-key add - \
  && echo "deb https://dl.yarnpkg.com/debian/ stable main" | tee /etc/apt/sources.list.d/yarn.list \
  && apt update && apt install -y yarn
EOF


echo BUILDING PHENIX...

docker run -it --rm \
  -v $ROOT_DIR:/phenix \
  -w /phenix \
  -u $USERNAME \
  phenix:builder make clean

docker run -it --rm \
  -v $ROOT_DIR:/phenix \
  -w /phenix \
  -u $USERNAME \
  -e VUE_APP_AUTH=$auth \
  -e VUE_BASE_PATH=$base \
  -e TAG=$tag \
  -e COMMIT=$commit \
  phenix:builder make bin/phenix

echo DONE BUILDING PHENIX
