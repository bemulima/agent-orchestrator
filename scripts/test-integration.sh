#!/bin/sh
set -eu

compose_command=${COMPOSE_COMMAND:-docker compose}
db_container=${DB_CONTAINER:-postgres}
db_user=${DB_USER:-postgres}
db_name=${INTEGRATION_DB_NAME:-course_dev_orchestrator_test}
db_password=${POSTGRES_PASSWORD:-postgres}
db_port=${POSTGRES_PORT:-5434}

case "$db_name" in
    *_test) ;;
    *)
        echo "Refusing to use integration database without _test suffix: $db_name" >&2
        exit 2
        ;;
esac

run_compose() {
    # COMPOSE_COMMAND intentionally supports "docker compose" and legacy
    # "docker-compose" as configuration values.
    # shellcheck disable=SC2086
    $compose_command "$@"
}

cleanup() {
    run_compose exec -T "$db_container" dropdb --if-exists -U "$db_user" "$db_name" >/dev/null
}

trap cleanup EXIT INT TERM
cleanup
run_compose exec -T "$db_container" createdb -U "$db_user" "$db_name"
COMPOSE_COMMAND="$compose_command" DB_CONTAINER="$db_container" DB_USER="$db_user" DB_NAME="$db_name" ./scripts/migrate.sh

DATABASE_URL="postgres://$db_user:$db_password@localhost:$db_port/$db_name?sslmode=disable" \
    go test -count=1 -tags=integration ./test/integration/...
