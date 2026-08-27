#!/bin/bash
# Installs custom CA certificates listed (comma-separated URLs) in the
# INSTALL_CERTS build arg/environment variable. No-op if unset.
set -euo pipefail

if [ -z "${INSTALL_CERTS:-}" ]; then
  exit 0
fi

IFS=',' read -r -a certs <<< "$INSTALL_CERTS"

for i in "${!certs[@]}"; do
  wget "${certs[$i]}" -e use_proxy=no -O "/usr/local/share/ca-certificates/custom${i}.crt"
done

update-ca-certificates
