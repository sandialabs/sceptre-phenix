#!/bin/sh

mkdir -p /tmp/minimega/bin

cp /opt/minimega/bin/minimega /tmp/minimega/bin/minimega

exec "$@"
