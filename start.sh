#!/usr/bin/env bash
set -Eeuo pipefail

BACKEND_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$BACKEND_ROOT"

fail() {
  printf 'Startup failed: %s\n' "$*" >&2
  exit 1
}

for command in docker go od tr; do
  command -v "$command" >/dev/null 2>&1 || fail "'$command' is required; install it and try again."
done

docker compose version >/dev/null 2>&1 || fail 'Docker Compose is unavailable. Start Docker Desktop or install the Compose plugin.'

GO_VERSION="$(go env GOVERSION 2>/dev/null)" || fail 'Go is unavailable.'
if [[ ! "$GO_VERSION" =~ ^go([0-9]+)\.([0-9]+) ]]; then
  fail "Could not read the Go version: $GO_VERSION"
fi
GO_MAJOR="${BASH_REMATCH[1]}"
GO_MINOR="${BASH_REMATCH[2]}"
if (( GO_MAJOR < 1 || (GO_MAJOR == 1 && GO_MINOR < 26) )); then
  fail "Go 1.26 or newer is required; found $GO_VERSION."
fi

TOKEN_BYTES="$(od -An -N32 -tx1 /dev/urandom | tr -d '[:space:]')" || fail 'Could not generate a development token.'
if [[ ! "$TOKEN_BYTES" =~ ^[a-f0-9]{64}$ ]]; then
  fail 'Development token generation returned unexpected data.'
fi

# These settings apply to this script and its child processes only.
export APP_ENV=development
export HTTP_ADDR=127.0.0.1:8080
export DATABASE_URL='postgres://community:community_local@127.0.0.1:5432/community?sslmode=disable'
export AUTH_MODE=development
export DEV_AUTH_TOKEN="$TOKEN_BYTES"
unset OIDC_ISSUER OIDC_AUDIENCE

printf 'Starting PostgreSQL and waiting for its health check...\n'
docker compose up --detach --wait --wait-timeout 90 postgres

printf 'Applying database migrations...\n'
go run ./cmd/community migrate

printf 'Preparing the local development administrator...\n'
go run ./cmd/community seed-dev

printf '\nLocal development bootstrap bearer token (Mindorigin administrator):\n%s\n' "$DEV_AUTH_TOKEN"
printf 'Sign in with one of the operator/admin email and password pairs above. Add employee accounts from the platform admin panel.\n'
printf 'Starting the API at http://127.0.0.1:8080. Press Ctrl+C to stop it.\n'
printf 'PostgreSQL stays running with its data. Run "docker compose stop postgres" to stop it.\n\n'

go run ./cmd/community serve
