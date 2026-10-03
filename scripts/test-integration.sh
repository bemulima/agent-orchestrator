#!/bin/sh
set -eu

compose_command=${COMPOSE_COMMAND:-docker compose}
db_container=${DB_CONTAINER:-postgres}
db_user=${POSTGRES_USER:-${DB_USER:-postgres}}
db_name=${INTEGRATION_DB_NAME:-course_dev_orchestrator_test}
db_password=${POSTGRES_PASSWORD:-postgres}
db_port=${POSTGRES_PORT:-5434}
base_database=${POSTGRES_DB:-course_dev_orchestrator}

case "$db_name" in
    *_test) ;;
    *)
        echo "Refusing to use integration database without _test suffix: $db_name" >&2
        exit 2
        ;;
esac

if [ "$#" -eq 0 ]; then
    random_suffix=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
    compose_project="cdo-integration-${random_suffix}-$$"
else
    compose_project=${COMPOSE_PROJECT_NAME:-}
fi

# Every registered phase, including external cleanup, requires the generated
# namespace. Explicit Compose arguments prevent ambient COMPOSE_FILE/project
# settings from redirecting resource creation or deletion.
project_suffix=${compose_project#cdo-integration-}
project_random=${project_suffix%-*}
project_pid=${project_suffix##*-}
case "$compose_project:$project_random:$project_pid" in
    cdo-integration-*:*:*) ;;
    *) echo "Refusing Compose project outside the cdo-integration namespace" >&2; exit 2 ;;
esac
case "$project_random" in
    ''|*[!a-f0-9]*) echo "Invalid disposable Compose project token" >&2; exit 2 ;;
esac
case "$project_pid" in
    ''|*[!0-9]*) echo "Invalid disposable Compose project process identity" >&2; exit 2 ;;
esac
[ "${#project_random}" -eq 16 ] || { echo "Invalid disposable Compose project token length" >&2; exit 2; }
[ "$db_container" = postgres ] || { echo "Disposable provisioner owns only the postgres service" >&2; exit 2; }

export COMPOSE_PROJECT_NAME="$compose_project"
export POSTGRES_USER="$db_user"
export POSTGRES_PASSWORD="$db_password"
export POSTGRES_DB="$base_database"
export POSTGRES_PORT="$db_port"

run_compose() {
    # COMPOSE_COMMAND supports "docker compose" and legacy "docker-compose".
    # shellcheck disable=SC2086
    $compose_command --project-name "$compose_project" --file docker-compose.yml \
        --env-file /dev/null "$@"
}

case "${1:-}" in
    --start)
        run_compose up -d --no-deps "$db_container"
        exit
        ;;
    --readiness)
        # SQL execution, rather than socket availability, establishes readiness.
        # The parent enforces the descriptor's deadline across every retry.
        while ! ready_database=$(run_compose exec -T "$db_container" psql -U "$db_user" \
            -d "$base_database" -v ON_ERROR_STOP=1 -Atc 'SELECT current_database()'); do
            sleep 1
        done
        if [ "$ready_database" != "$base_database" ]; then
            echo "Disposable PostgreSQL identity mismatch: $ready_database" >&2
            exit 2
        fi
        printf 'Disposable PostgreSQL readiness PASS database=%s\n' "$ready_database"
        exit
        ;;
    --cleanup)
        run_compose down --volumes --remove-orphans
        exit
        ;;
    '') ;;
    *) echo "Unknown disposable provisioner phase" >&2; exit 2 ;;
esac

# Python's standard library supplies process-group deadlines on macOS and Linux,
# where GNU timeout is not consistently available. Read the registered phase
# command and limit directly from the checksum-bound provisioner descriptor.
run_phase() {
    python3 - "$1" <<'PYTHON' &
import json
import os
import signal
import subprocess
import sys
import time

phase = sys.argv[1]
with open(".ai/testing/disposable-postgres-provisioner.v1.json", encoding="utf-8") as source:
    descriptor = json.load(source)
config = descriptor["lifecycle"][phase]
limit = config["timeout_seconds"]
if type(limit) is not int or not 1 <= limit <= 3600:
    sys.exit("Invalid disposable provisioner phase timeout")
process = None


def stop_group():
    global process
    owned = process
    process = None
    if owned is not None:
        try:
            os.killpg(owned.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        finally:
            owned.wait()


def interrupted(signum, _frame):
    stop_group()
    sys.exit(128 + signum)


signal.signal(signal.SIGINT, interrupted)
signal.signal(signal.SIGTERM, interrupted)
started = time.monotonic()
try:
    process = subprocess.Popen(config["invoke"], cwd=config["working_directory"], start_new_session=True)
    code = process.wait(timeout=max(0.001, limit - (time.monotonic() - started)))
except subprocess.TimeoutExpired:
    stop_group()
    print(f"Disposable PostgreSQL {phase} TIMEOUT limit={limit}s", file=sys.stderr)
    sys.exit(124)
except OSError as error:
    print(f"Disposable PostgreSQL {phase} could not run: {error}", file=sys.stderr)
    sys.exit(2)
finally:
    # Also reap descendants left behind by a completed phase command.
    stop_group()
sys.exit(code if code >= 0 else 128 - code)
PYTHON
    phase_pid=$!
    phase_status=0
    wait "$phase_pid" || phase_status=$?
    phase_pid=
    return "$phase_status"
}

abort_phase() {
    if [ -n "${phase_pid:-}" ]; then
        kill -TERM "$phase_pid" 2>/dev/null || true
        wait "$phase_pid" 2>/dev/null || true
        phase_pid=
    fi
}

cleanup() {
    status=$?
    trap - EXIT INT TERM
    abort_phase
    cleanup_status=0
    if [ "${compose_started:-false}" = true ]; then
        run_phase cleanup || cleanup_status=$?
        if [ "$cleanup_status" -eq 0 ]; then
            printf 'Disposable PostgreSQL cleanup PASS project=%s\n' "$compose_project"
        else
            printf 'Disposable PostgreSQL cleanup FAIL project=%s exit=%s\n' "$compose_project" "$cleanup_status" >&2
        fi
    fi
    if [ "$status" -ne 0 ]; then
        exit "$status"
    fi
    exit "$cleanup_status"
}

trap cleanup EXIT
trap 'abort_phase; exit 130' INT
trap 'abort_phase; exit 143' TERM
# Register ownership before start: partially successful provisioning must clean up.
compose_started=true
printf 'Disposable PostgreSQL provisioner project=%s database=%s\n' "$compose_project" "$base_database"
run_phase start
run_phase readiness
run_compose exec -T "$db_container" dropdb --if-exists -U "$db_user" "$db_name" >/dev/null
run_compose exec -T "$db_container" createdb -U "$db_user" "$db_name"
COMPOSE_COMMAND="$compose_command --project-name $compose_project --file docker-compose.yml --env-file /dev/null" \
    DB_CONTAINER="$db_container" DB_USER="$db_user" DB_NAME="$db_name" ./scripts/migrate.sh

DATABASE_URL="postgres://$db_user:$db_password@localhost:$db_port/$db_name?sslmode=disable" \
    go test -count=1 -tags=integration ./test/integration/...
