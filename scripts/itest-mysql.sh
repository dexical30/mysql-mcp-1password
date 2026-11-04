#!/bin/sh
# Starts (or with "stop", stops) the throwaway MySQL used by integration tests.
set -eu
NAME=mysql-mcp-1password-itest
if [ "${1:-}" = "stop" ]; then
  docker stop "$NAME" >/dev/null 2>&1 || true
  exit 0
fi
if ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  docker run -d --rm --name "$NAME" -e MYSQL_ROOT_PASSWORD=itest -e MYSQL_DATABASE=itest -p 13306:3306 mysql:8.4 >/dev/null
fi
i=0
until docker exec "$NAME" mysql -uroot -pitest -e 'SELECT 1' >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -gt 60 ] && { echo "MySQL did not become ready" >&2; exit 1; }
  sleep 2
done
