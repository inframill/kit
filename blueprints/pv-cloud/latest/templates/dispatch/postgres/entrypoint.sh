#!/bin/sh
set -eu

# Compose file secrets are root-owned. Stage a private, ephemeral copy readable
# by the image's postgres user, without depending on a numeric image UID.
install -d -m 0700 -o postgres -g postgres /run/pv-postgres-tls
install -m 0600 -o postgres -g postgres /run/secrets/tls_private_key /run/pv-postgres-tls/server.key
install -m 0644 -o postgres -g postgres /etc/pv-postgres/runtime/fullchain.pem /run/pv-postgres-tls/server.crt
exec /usr/local/bin/docker-entrypoint.sh "$@"
