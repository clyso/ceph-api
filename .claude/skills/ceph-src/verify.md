# verify.md — probe a live dashboard

For when source-reading is ambiguous, you need a real response body, or
you want to see which commands the dashboard issues for a given route.

Image: `arttor/ceph-test` on GitHub (`ghcr.io/arttor/ceph-test:v19`).
Single-node, multi-arch, all-in-one Ceph cluster pre-baked at build
time. Daemons inside the container: `mon.demo`, `mgr.demo`, `osd.0`,
`client.rgw.demo`.

## Bring it up

```sh
eval "$(./.claude/skills/ceph-src/scripts/up.sh)"
# prints URL=…  USER=…  PASS=…
```

Idempotent. Tear down: `docker rm -f ceph-dev`.

## Auth

```sh
TOKEN=$(curl -sk -X POST "$URL/api/auth" \
  -H 'Accept: application/vnd.ceph.api.v1.0+json' \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | jq -r .token)
```

## Call an endpoint

```sh
curl -sk -H "Authorization: Bearer $TOKEN" \
  -H 'Accept: application/vnd.ceph.api.v0.1+json' \
  "$URL/api/cluster" | jq
```

**The `Accept` version is per-endpoint.** Look it up in
`third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml`. A `415` with
body `"Incorrect version: endpoint is 'X.Y', client requested 'Z.W'"`
is the mismatch signal.

## Logs that exist (`/var/log/ceph/` inside the container)

- `ceph.audit.log` — **every command dispatched to the monitor**, as
  `cmd=[{"prefix": "...", ...}]: dispatch` lines. Best place to see
  what mon commands the dashboard issues for a route.
- `ceph-mgr.demo.log` — mgr daemon, including the dashboard module's
  Python logger. Dashboard request lines:
  `[dashboard INFO request] [<client>] [<METHOD>] [<status>] [<elapsed>] [<user>] [<bytes>] <route>`.
- `ceph-mon.demo.log`, `ceph-osd.0.log`, `ceph-client.rgw.demo.log` —
  daemon-specific.
- `ceph.log`, `ceph.audit.log` — cluster-wide events.

Many dashboard endpoints don't fire mon commands — they read mgr's
cached cluster map. If audit shows nothing for your call, the
dashboard isn't going to the monitor for that data.

## Knobs to turn up when probing

Runtime: `docker exec ceph-dev ceph config set <who> <key> <value>`.
Persistent: pass `-e CEPH_EXTRA_CONF=...` to `docker run`, or mount a
file into `/etc/ceph/ceph.conf.d/*.conf` (both get appended to
`ceph.conf` at startup).

- `debug_mgr 20` — mgr daemon verbosity.
- `mgr/dashboard/log_level debug` — dashboard Python logger
  (allowed: `info|debug|critical|error|warning`). Every mgr module
  inherits a `log_level` option — declared in
  `src/pybind/mgr/mgr_module.py`.
- `mgr/<module>/log_level` — same knob for other mgr modules.
- `debug_ms 5` — messenger / wire-protocol verbosity.

Canonical option list with defaults and types:
`third_party/ceph/src/common/options/*.yaml.in`. File-vs-store
semantics and which options accept runtime changes:
`third_party/ceph/doc/rados/configuration/ceph-conf.rst`
([repo-map.md](./repo-map.md) → Configuration options row).

## Sanity-check command shape directly

```sh
docker exec ceph-dev ceph -f json <command>   # raw JSON, no dashboard
docker exec ceph-dev ceph -h <command>        # syntax + flags
```

When dashboard output diverges from raw command output, the dashboard
is post-processing in Python — read the controller in
`src/pybind/mgr/dashboard/controllers/<resource>.py`.

## When to write a Go probe instead

Curl wins for one-off "what does X return". Extend the parity harness
(`test/parity/`) for multi-step flows with parity-style comparison or
a permanent regression test. See `test/setup_cgo_test.go` for the
in-process auth bootstrap and `test/parity/client.go` for `Login` /
`Do` helpers.
