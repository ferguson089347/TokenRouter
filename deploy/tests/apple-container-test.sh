#!/bin/bash

# apple-container-test.sh runs deployment lifecycle commands against an isolated
# container CLI fixture. The checks cover generated credentials, the repository's
# default image, persistent storage, and resource ownership while leaving host
# containers and deployment files outside the fixture untouched.

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/apple-container.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tokenrouter-apple-test.XXXXXX")"
STATE_DIR="${TEST_ROOT}/state"
ENV_FILE="${TEST_ROOT}/tokenrouter.env"

cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_exists() {
    [[ -e "$1" ]] || fail "Expected path to exist: $1"
}

assert_missing() {
    [[ ! -e "$1" ]] || fail "Expected path to be absent: $1"
}

export FAKE_CONTAINER_STATE="${STATE_DIR}"
export PATH="${TEST_DIR}/fixtures/bin:${PATH}"
export TOKENROUTER_ENV_FILE="${ENV_FILE}"

mkdir -p "${STATE_DIR}"

"${SCRIPT}" init
[[ "$(stat -f '%Lp' "${ENV_FILE}")" == "600" ]] || fail "init did not create a mode-600 env file"
grep -q '^POSTGRES_PASSWORD=change_this_secure_password$' "${ENV_FILE}" && fail "init retained the placeholder password"

chmod 644 "${ENV_FILE}"
if "${SCRIPT}" up >/dev/null 2>&1; then
    fail "up accepted an insecure env file"
fi
chmod 600 "${ENV_FILE}"

"${SCRIPT}" up
assert_exists "${STATE_DIR}/containers/tokenrouter-apple"
assert_exists "${STATE_DIR}/containers/tokenrouter-apple-postgres"
assert_exists "${STATE_DIR}/containers/tokenrouter-apple-redis"
assert_exists "${STATE_DIR}/running/tokenrouter-apple"
grep -Fq 'ghcr.io/kiroo92/tokenrouter:latest' "${STATE_DIR}/commands.log" || fail "up did not use the TokenRouter application image"
"${SCRIPT}" status >/dev/null

"${SCRIPT}" up --recreate
assert_exists "${STATE_DIR}/running/tokenrouter-apple"
"${SCRIPT}" down
assert_missing "${STATE_DIR}/running/tokenrouter-apple"
assert_missing "${STATE_DIR}/running/tokenrouter-apple-postgres"
assert_missing "${STATE_DIR}/running/tokenrouter-apple-redis"

"${SCRIPT}" destroy --yes
assert_missing "${STATE_DIR}/containers/tokenrouter-apple"
assert_missing "${STATE_DIR}/networks/tokenrouter-apple"
assert_exists "${STATE_DIR}/volumes/tokenrouter-apple-data"

"${SCRIPT}" up
"${SCRIPT}" destroy --volumes --yes
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-postgres-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-redis-data"

touch "${STATE_DIR}/system-running"
touch "${STATE_DIR}/containers/tokenrouter-apple"
touch "${STATE_DIR}/unowned/container/tokenrouter-apple"
if "${SCRIPT}" status >/dev/null 2>&1; then
    fail "status accepted an unowned same-name container"
fi

# 旧栈继续使用原卷和归属标签，旧变量也能指定环境文件。
rm -f "${STATE_DIR}/containers/tokenrouter-apple" "${STATE_DIR}/unowned/container/tokenrouter-apple"
touch "${STATE_DIR}/volumes/sub2api-apple-data" "${STATE_DIR}/volumes/sub2api-apple-postgres-data" "${STATE_DIR}/volumes/sub2api-apple-redis-data"
TOKENROUTER_ENV_FILE= SUB2API_ENV_FILE="${ENV_FILE}" "${SCRIPT}" up
assert_exists "${STATE_DIR}/containers/sub2api-apple"
"${SCRIPT}" logs app >/dev/null
assert_exists "${STATE_DIR}/volumes/sub2api-apple-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-data"
touch "${STATE_DIR}/volumes/tokenrouter-apple-data"
if "${SCRIPT}" up >/dev/null 2>&1; then fail "up accepted mixed old and new resources"; fi
rm -f "${STATE_DIR}/volumes/tokenrouter-apple-data"
"${SCRIPT}" destroy --volumes --yes

printf 'Apple container lifecycle tests passed.\n'
