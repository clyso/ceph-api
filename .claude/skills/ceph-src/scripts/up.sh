#!/usr/bin/env bash
# Brings up arttor/ceph-test with dashboard enabled and an admin user.
# Idempotent: kills any existing ceph-dev container first.
# Tear down: docker rm -f ceph-dev
set -euo pipefail

NAME=ceph-dev
PORT=8443
IMAGE=ghcr.io/arttor/ceph-test:v19
USER=admin
PASS=devpass-1234

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --rm --name "$NAME" -p "${PORT}:8443" "$IMAGE" >/dev/null

echo "waiting for ceph health..." >&2
until docker exec "$NAME" ceph health 2>/dev/null | grep -qE 'HEALTH_(OK|WARN)'; do sleep 1; done

echo "waiting for mgr to load modules..." >&2
until docker exec "$NAME" ceph mgr module ls 2>/dev/null | grep -q dashboard; do sleep 1; done

docker exec "$NAME" ceph mgr module enable dashboard >/dev/null
docker exec "$NAME" ceph dashboard create-self-signed-cert >/dev/null
printf '%s' "$PASS" | docker exec -i "$NAME" tee /tmp/p >/dev/null
docker exec "$NAME" ceph dashboard ac-user-create --enabled --force-password "$USER" -i /tmp/p administrator >/dev/null

echo "waiting for dashboard listener..." >&2
until curl -sk -o /dev/null -w '%{http_code}\n' "https://localhost:${PORT}/" | grep -qE '^(200|301|302|403)'; do sleep 1; done

cat <<EOF
URL=https://localhost:${PORT}
USER=${USER}
PASS=${PASS}
EOF
