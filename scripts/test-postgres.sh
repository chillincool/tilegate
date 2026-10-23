#!/usr/bin/env bash
# Runs the unit tests with a throwaway PostgreSQL container, so the store
# tests cover both SQLite and PostgreSQL.
set -euo pipefail
name=tilegate-test-postgres
port=${TILEGATE_TEST_POSTGRES_PORT:-55432}

docker rm -f "$name" > /dev/null 2>&1 || true
docker run -d --rm --name "$name" -e POSTGRES_PASSWORD=test -p "127.0.0.1:$port:5432" postgres:17-alpine > /dev/null
trap 'docker rm -f "$name" > /dev/null 2>&1 || true' EXIT

for _ in $(seq 60); do
  docker exec "$name" pg_isready -U postgres > /dev/null 2>&1 && break
  sleep 1
done

TILEGATE_TEST_POSTGRES_URL="postgres://postgres:test@127.0.0.1:$port/postgres?sslmode=disable" go test -race ./...
