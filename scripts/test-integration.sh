#!/bin/sh
set -eu

compose_command=${COMPOSE_COMMAND:-docker compose}
db_container=${DB_CONTAINER:-postgres}
db_user=${POSTGRES_USER:-${DB_USER:-postgres}}
db_name=${INTEGRATION_DB_NAME:-course_dev_orchestrator_test}
db_password=${POSTGRES_PASSWORD:-postgres}
db_port=${POSTGRES_PORT:-5434}
base_database=${POSTGRES_DB:-course_dev_orchestrator}
random_suffix=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
compose_project="cdo-integration-${random_suffix}-$$"

case "$db_name" in
    *_test) ;;
    *)
        echo "Refusing to use integration database without _test suffix: $db_name" >&2
        exit 2
        ;;
esac

case "$compose_project" in
    cdo-integration-[a-f0-9]*-[0-9]*) ;;
    *)
        echo "Refusing Compose project outside the cdo-integration-* namespace: $compose_project" >&2
        exit 2
        ;;
esac

export COMPOSE_PROJECT_NAME="$compose_project"
export POSTGRES_USER="$db_user"
export POSTGRES_PASSWORD="$db_password"
export POSTGRES_DB="$base_database"
export POSTGRES_PORT="$db_port"

run_compose() {
    # COMPOSE_COMMAND intentionally supports "docker compose" and legacy
    # "docker-compose" as configuration values.
    # shellcheck disable=SC2086
    COMPOSE_PROJECT_NAME="$compose_project" $compose_command "$@"
}

cleanup() {
    status=$?
    trap - EXIT INT TERM
    cleanup_status=0
    if [ "${compose_started:-false}" = true ]; then
        run_compose down --volumes --remove-orphans || cleanup_status=$?
        if [ "$cleanup_status" -eq 0 ]; then
            printf 'Disposable PostgreSQL cleanup PASS project=%s\n' "$compose_project"
        fi
    fi
    if [ "$status" -ne 0 ]; then
        exit "$status"
    fi
    exit "$cleanup_status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
compose_started=true
printf 'Disposable PostgreSQL provisioner project=%s database=%s\n' "$compose_project" "$base_database"
run_compose up -d --wait "$db_container"
ready_database=$(run_compose exec -T "$db_container" psql -U "$db_user" -d "$base_database" -v ON_ERROR_STOP=1 -Atc 'SELECT current_database()')
if [ "$ready_database" != "$base_database" ]; then
    echo "Disposable PostgreSQL identity mismatch: $ready_database" >&2
    exit 2
fi
printf 'Disposable PostgreSQL readiness PASS database=%s\n' "$ready_database"
run_compose exec -T "$db_container" dropdb --if-exists -U "$db_user" "$db_name" >/dev/null
run_compose exec -T "$db_container" createdb -U "$db_user" "$db_name"
COMPOSE_COMMAND="$compose_command" DB_CONTAINER="$db_container" DB_USER="$db_user" DB_NAME="$db_name" ./scripts/migrate.sh

DATABASE_URL="postgres://$db_user:$db_password@localhost:$db_port/$db_name?sslmode=disable" \
    go test -count=1 -tags=integration ./test/integration/...
