ARG PHENIX_REPO=sandialabs/sceptre-phenix
ARG PHENIX_TAG=main

FROM ghcr.io/${PHENIX_REPO}/phenix:${PHENIX_TAG}

# https://docs.docker.com/engine/reference/builder/#understand-how-arg-and-from-interact
ARG PHENIX_REPO
ARG PHENIX_TAG

ENV NODE_VERSION 14.21.3

RUN wget -O node.txz https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz \
  && tar -xJf node.txz -C /usr/local --strip-components=1 --no-same-owner && rm node.txz \
  && ln -s /usr/local/bin/node /usr/local/bin/nodejs

RUN npm install -g @vue/cli redoc-cli yarn
RUN git clone --branch ${PHENIX_TAG} https://github.com/${PHENIX_REPO}.git /usr/local/src/phenix \
  && mkdir -p /opt/phenix/web \
  && cp -a /usr/local/src/phenix/src/go/web/public /opt/phenix/web

WORKDIR /usr/local/src/phenix/src/js
RUN yarn install
WORKDIR /opt/phenix/web/public
RUN npx redoc-cli build docs/openapi.yml -o docs/index.html --title 'phenix API'

COPY phenix-ui-entrypoint.sh /phenix-ui-entrypoint.sh
RUN  chmod +x /phenix-ui-entrypoint.sh

WORKDIR /opt/phenix

ENTRYPOINT ["/phenix-ui-entrypoint.sh"]
