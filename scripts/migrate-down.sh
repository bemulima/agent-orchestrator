#!/bin/sh
set -eu

compose_command=${COMPOSE_COMMAND:-docker compose}
db_container=${DB_CONTAINER:-postgres}
db_user=${DB_USER:-postgres}
db_name=${DB_NAME:-course_dev_orchestrator}

run_psql() {
    # shellcheck disable=SC2086
    $compose_command exec -T "$db_container" psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$db_name" "$@"
}

version=$(run_psql -tAc "SELECT version FROM schema_migrations ORDER BY applied_at DESC, version DESC LIMIT 1;" | tr -d '[:space:]')
if [ -z "$version" ]; then
    echo "No applied migration to roll back"
    exit 0
fi

migration="db/migrations/$version.down.sql"
if [ ! -f "$migration" ]; then
    echo "ERROR: missing down migration $migration" >&2
    exit 1
fi

echo "Rolling back $migration"
{
    cat "$migration"
    printf "\nDELETE FROM schema_migrations WHERE version = '%s';\n" "$version"
} | run_psql --single-transaction
