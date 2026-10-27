#!/bin/sh
set -eu
ROOT_TOKEN="$(cat /run/secrets/inframill_root_token)"
test -n "$ROOT_TOKEN"
export ROOT_TOKEN
exec inframill server start --config /etc/inframill/inframill.yaml
