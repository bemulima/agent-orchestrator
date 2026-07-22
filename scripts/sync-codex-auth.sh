#!/bin/sh
set -eu

auth_file=${1:-}
compose_command=${COMPOSE_COMMAND:-docker compose}

if [ -z "$auth_file" ]; then
    echo "usage: sync-codex-auth.sh /absolute/path/to/auth.json" >&2
    exit 2
fi
case "$auth_file" in
    /*) ;;
    *)
        echo "Codex auth path must be absolute" >&2
        exit 2
        ;;
esac
if [ ! -f "$auth_file" ]; then
    echo "Codex CLI login not found at $auth_file; run 'codex login' first" >&2
    exit 1
fi

# Copy only the CLI credential file into the worker's private durable volume.
# Do not mount the complete host CODEX_HOME: it also contains local histories,
# configuration, caches, and other data unrelated to orchestrated execution.
# COMPOSE_COMMAND intentionally supports both "docker compose" and
# "docker-compose" values.
# shellcheck disable=SC2086
$compose_command run --rm --no-deps --entrypoint /bin/sh \
    --volume "$auth_file:/run/codex-host-auth.json:ro" \
    worker -c '
        set -eu
        umask 077
        mkdir -p /data/codex
        cp /run/codex-host-auth.json /data/codex/auth.json.tmp
        mv /data/codex/auth.json.tmp /data/codex/auth.json
        chmod 600 /data/codex/auth.json
    '

echo "Codex CLI ChatGPT login synchronized with the worker"
