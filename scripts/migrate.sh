#!/bin/sh
set -eu

compose_command=${COMPOSE_COMMAND:-docker compose}
db_container=${DB_CONTAINER:-postgres}
db_user=${DB_USER:-postgres}
db_name=${DB_NAME:-course_dev_orchestrator}

run_psql() {
    # COMPOSE_COMMAND intentionally supports "docker compose" and legacy
    # "docker-compose" as configuration values.
    # shellcheck disable=SC2086
    $compose_command exec -T "$db_container" psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$db_name" "$@"
}

run_psql -c "CREATE TABLE IF NOT EXISTS schema_migrations (version varchar(255) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());"

for migration in db/migrations/*.up.sql; do
    [ -f "$migration" ] || continue
    filename=$(basename "$migration")
    version=${filename%.up.sql}
    applied=$(run_psql -tAc "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '$version');")
    if [ "$applied" = "t" ]; then
        echo "Skipping $migration (already applied)"
        continue
    fi

    echo "Applying $migration"
    {
        cat "$migration"
        printf "\nINSERT INTO schema_migrations (version) VALUES ('%s');\n" "$version"
    } | run_psql --single-transaction
done
