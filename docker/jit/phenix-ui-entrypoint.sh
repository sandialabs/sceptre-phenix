#!/bin/bash

base=${PHENIX_BASE_PATH:-/}
auth=${PHENIX_WEB_AUTH:-disabled}

echo    "phenix web UI base path:      $base (set via PHENIX_BASE_PATH)"
echo -e "phenix web UI authentication: $auth (set via PHENIX_WEB_AUTH=[enabled|disabled|proxy])\n"

if [[ "$1" == *"help"* ]]; then
  exec phenix ui --help
fi

echo "building phenix UI (this could take a while...)"
start_time=$(date +%s)

pushd /usr/local/src/phenix/src/js &> /dev/null
VITE_BASE_PATH=$base VITE_AUTH=$auth npm run build &> /tmp/phenix-ui-build.log
res=$?

if [ $res -ne 0 ]; then
  echo -e "\nthere was an error building phenix UI\n"
  cat /tmp/phenix-ui-build.log
  exit $res
fi
popd &> /dev/null

end_time=$(date +%s)
echo -e "phenix UI took $((end_time - start_time)) seconds to build\n"

cd /opt/phenix
cp -a /usr/local/src/phenix/src/js/dist/* web/public

exec phenix ui --base-path "$base" --unbundled "$@"
