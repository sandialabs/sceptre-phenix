ARG PHENIX_REPO=sandialabs/sceptre-phenix
ARG PHENIX_BRANCH=main
ARG PHENIX_IMG=ghcr.io/${PHENIX_REPO}/phenix:${PHENIX_BRANCH}

FROM $PHENIX_IMG

# https://docs.docker.com/reference/dockerfile/#understand-how-arg-and-from-interact
ARG PHENIX_REPO
ARG PHENIX_BRANCH

# Install Node.js
ENV NODE_VERSION=24.16.0
RUN wget --progress=dot:giga -O node.txz https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz \
  && tar -xJf node.txz -C /usr/local --strip-components=1 --no-same-owner && rm node.txz \
  && ln -s /usr/local/bin/node /usr/local/bin/nodejs

RUN git clone --branch ${PHENIX_BRANCH} https://github.com/${PHENIX_REPO}.git /usr/local/src/phenix \
  && mkdir -p /opt/phenix/web \
  && cp -a /usr/local/src/phenix/src/go/web/public /opt/phenix/web

WORKDIR /usr/local/src/phenix/src/js
RUN npm ci --no-fund --no-audit && npm cache clean --force

# API documentation, rendered with the Redocly version pinned in the cloned
# branch's scripts/api-docs.sh
WORKDIR /opt/phenix/web/public
RUN /usr/local/src/phenix/scripts/api-docs.sh build docs/openapi.yml docs/index.html \
  && npm cache clean --force

COPY phenix-ui-entrypoint.sh /phenix-ui-entrypoint.sh
RUN  chmod +x /phenix-ui-entrypoint.sh

WORKDIR /opt/phenix

ENTRYPOINT ["/phenix-ui-entrypoint.sh"]
