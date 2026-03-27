# Infeasible & Constrained Operations in ceph-api

This document provides a deep-dive into every operation that ceph-api **cannot implement** with its current architecture (go-ceph v0.26.0 + librados), explaining the internal Ceph mechanisms that block each one, exact message flows, and any available workarounds.

---

## Table of Contents

1. [Classification Overview](#1-classification-overview)
2. [In-process MGR Perf Counter Pipeline (Hard Block)](#2-in-process-mgr-perf-counter-pipeline)
3. [Orchestrator-Dependent Operations (Conditional)](#3-orchestrator-dependent-operations)
4. [RGW S3 Protocol Operations (Soft Block)](#4-rgw-s3-protocol-operations)
5. [CGO / go-ceph Runtime Constraints (Architectural)](#5-cgo--go-ceph-runtime-constraints)
6. [Real-time Event Log Streaming (Soft Block)](#6-real-time-event-log-streaming)
7. [MGR Module Private KV Store (Partial Access)](#7-mgr-module-private-kv-store)
8. [Workaround Summary](#8-workaround-summary)

---

## 1. Classification Overview

Operations are classified into five blocking categories:

| Symbol | Category | Meaning |
|--------|----------|---------|
| 🔴 | **Hard Block** | Technically impossible without changing ceph/mgr source |
| 🟡 | **Conditional** | Works via mgr_command IF the prerequisite is configured in the cluster |
| 🟠 | **Soft Block** | Implementable in Go but requires new code not in go-ceph |
| 🟤 | **Architectural** | Works functionally but has runtime safety constraints from CGO |
| 🟣 | **Partial Access** | Accessible via undocumented/internal API paths |

### Master Classification Table

| Operation | Dashboard endpoint | Block type | Workaround |
|-----------|-------------------|------------|------------|
| Per-OSD I/O rate (ops/sec, kB/s) | `GET /api/osd` stats | 🔴 Hard Block | Prometheus `:9283/metrics` |
| Per-MDS perf counters | `GET /api/mds` | 🔴 Hard Block | Prometheus `:9283/metrics` |
| Per-MON perf counters | `GET /api/monitor` | 🔴 Hard Block | Prometheus `:9283/metrics` |
| Per-RGW perf counters | `GET /api/rgw/daemon` | 🔴 Hard Block | Prometheus `:9283/metrics` |
| Pool I/O rate time-series | `GET /api/pool` | 🔴 Hard Block | Prometheus `:9283/metrics` |
| Daemon restart/start/stop | `POST /api/daemon/{id}/action` | 🟡 Conditional | `mgr_command("orch daemon restart")` if Orchestrator configured |
| Host add/remove | `POST /api/host` | 🟡 Conditional | `mgr_command("orch host add/rm")` if Orchestrator configured |
| OSD deployment/removal | `POST /api/osd` | 🟡 Conditional | `mgr_command("orch apply osd")` if Orchestrator configured |
| Hardware health (SMART) | `GET /api/host/{hostname}/devices` | 🟡 Conditional | `mgr_command("device ls-by-host")` + device health module |
| Cluster rolling upgrade | `POST /api/upgrade` | 🟡 Conditional | `mgr_command("orch upgrade start")` if Orchestrator configured |
| OSD `safe_to_delete` (Orch level) | `GET /api/osd/safe_to_delete` | 🟡 Conditional | `mon_command("osd safe-to-destroy")` covers Ceph-level check |
| Bucket notifications | `GET/PUT /api/rgw/bucket` notifications | 🟠 Soft Block | Custom S3 HTTP client to RGW |
| S3 lifecycle policies | `GET/PUT /api/rgw/bucket` lifecycle | 🟠 Soft Block | Custom S3 HTTP client to RGW |
| S3 CORS/SSE/replication | `GET/PUT /api/rgw/bucket` | 🟠 Soft Block | Custom S3 HTTP client to RGW |
| Context cancellation of in-flight ops | Internal | 🟤 Architectural | `rados_osd_op_timeout` for OSD ops; `rados_mon_op_timeout` does NOT bound `mon_command` |
| Goroutine preemption in CGO | Internal | 🟤 Architectural | Semaphore + WaitGroup bounding |
| RBD trash list > 10,240 items | `GET /api/block/image/trash` | 🟤 Architectural | Cursor-based pagination in handler |
| Short-lived RADOS conn leak | Internal | 🟤 Architectural | Long-lived singleton connection |
| `ceph -w` style live log | `GET /api/logs/audit` streaming | 🟠 Soft Block | Poll `log last` (filter by Paxos version); or direct CGO `rados_monitor_log2` for true push |
| MGR module private KV (`get_store`) | Module settings | 🟣 Partial | `config-key get mgr/<module>/<key>` (undocumented) |

---

## 2. In-process MGR Perf Counter Pipeline

### What It Is

The Ceph Dashboard's `GET /api/perf_counters`, OSD I/O rate graphs, and MDS/MON/RGW performance views all consume data from a **perf counter circular buffer** maintained exclusively inside the active MGR process.

### How It Works Internally

Every Ceph daemon (OSD, MDS, MON, RGW) sends periodic `MMgrReport` messages directly to the active MGR via msgr2 TCP. The MGR's `DaemonServer` C++ class receives these messages and calls Python callbacks via the C extension (`BaseMgrModule`). The in-memory rolling window (20 data points) is never serialized to any external protocol.

```mermaid
sequenceDiagram
    box Ceph Daemons (msgr2 TCP — private channel)
        participant OSD0 as OSD.0
        participant OSD1 as OSD.1
        participant MDS0 as MDS.0
        participant MON0 as MON.0
    end
    box Active MGR Process (single PID)
        participant DS as DaemonServer.cc<br>C++ layer
        participant BM as BaseMgrModule.cc<br>C extension
        participant PY as Active Python Module<br>(dashboard.py)
    end
    box External Processes
        participant PROM as Prometheus Module<br>(also in MGR)
        participant CAPI as ceph-api<br>(external Go process)
    end

    loop every ~2 seconds
        OSD0->>DS: MMgrReport { perf_schema, perf_data, pg_stats }
        OSD1->>DS: MMgrReport { perf_schema, perf_data, pg_stats }
        MDS0->>DS: MMgrReport { perf_schema, perf_data }
        MON0->>DS: MMgrReport { perf_schema, perf_data }
    end

    Note over DS: Updates in-memory circular buffer<br>(20 data points × N counters per daemon)<br>src/mgr/DaemonState.h

    DS->>BM: notify_all_progress_events()
    BM->>PY: Python callback (no serialization)

    PY->>BM: mgr.get_unlabeled_counter_latest("osd.0", "osd.op")
    Note over BM,PY: Direct C extension call<br>Reads shared in-process memory<br>Zero network hop
    BM-->>PY: [ 0.0, 1532.4, 1821.3, 0.0, ... ]

    DS->>PROM: exposes as labeled Prometheus metrics
    Note over PROM: ceph_osd_op_r, ceph_osd_op_w, etc.<br>scraped by Prometheus at :9283

    CAPI->>DS: ExecMgr({"prefix":"osd perf"}) via librados MgrCommand
    Note over DS,CAPI: osd perf = CURRENT SNAPSHOT only<br>commit_latency_ms + apply_latency_ms<br>no rolling window, no time-series
    DS-->>CAPI: {"osd_perf_infos":[{"id":0,"perf_stats":{"commit_latency_ms":3,"apply_latency_ms":5}}]}

    Note over CAPI: ❌ Rolling window data lives only<br>in DaemonServer's in-process heap.<br>No mgr_command serializes it.
```

### The Exact Barrier

The data lives in `src/mgr/DaemonState.h`:

```cpp
// src/mgr/DaemonState.h (simplified)
struct PerfCounterInstance {
  PerfCounterType type;
  std::vector<PerfCounterData> data;  // circular buffer, 20 entries
};

class DaemonState {
  std::map<std::string, PerfCounterInstance> perf_counters;
  // ...no serialization to any command response format
};
```

The Python binding at `src/mgr/BaseMgrModule.cc`:

```cpp
PyObject *BaseMgrModule::get_unlabeled_counter_latest(
    const std::string &daemon_name, const std::string &counter_name) {
  // Directly reads DaemonState::perf_counters in-process
  // Returns a Python list — no msgr2 serialization happens
}
```

There is no `mon_command` or `mgr_command` that serializes this rolling-window data. The only command that touches perf counters is `osd perf`, which returns a **current snapshot** of commit and apply latency (in milliseconds, from `PGMap::dump_osd_perf_stats()`), not the time-windowed rates that the dashboard computes by diffing consecutive circular buffer entries. Source: `src/mon/PGMap.cc:2101` — dumps `os_commit_latency_ns / 1000000ull` and `os_apply_latency_ns / 1000000ull`.

### External Access Paths

While no `mon_command`/`mgr_command` serializes the circular buffer data, there are multiple external paths. For ceph-api the most practical is the Prometheus module:

1. **Prometheus MGR module** — `http://<mgr>:9283/metrics` (in-process, reads DaemonState directly)
2. **Dashboard REST API** — `GET /api/perf_counters/{svc_type}/{id}` (in-process, via `get_unlabeled_counter_latest()`)
3. **influx/telegraf MGR modules** — push perf counters to InfluxDB/Telegraf (in-process)
4. **`ceph-exporter` daemon** — `http://<host>:9926/metrics` — a standalone C++ daemon that reads each daemon's admin socket via `counter dump`, bypassing the MGR entirely

Note: `osd perf` (`PGMap::dump_osd_perf_stats()`) travels via `MPGStats` from OSDs to MONs — it is a completely different pipeline from `MMgrReport`. It returns a windowed latency average `(sum_new - sum_old) / (count_new - count_old)`, not a monotonic counter and not the MGR circular buffer.

### The Prometheus Workaround

The MGR's built-in Prometheus module (`ceph mgr module enable prometheus`) exposes all `MMgrReport` data as labeled metrics at `http://<mgr-host>:9283/metrics`. The Prometheus module runs **inside the same MGR process** and has access to the same `DaemonState` memory. ceph-api can proxy or query this endpoint:

```mermaid
flowchart LR
    subgraph MGR["Active MGR Process"]
        DS["DaemonServer\n(circular buffer)"]
        PROM_MOD["Prometheus Module\n(runs inside MGR)"]
        DS -->|"in-process read"| PROM_MOD
    end

    subgraph CAPI["ceph-api"]
        PROXY["PrometheusProxy\nhandler"]
        CLIENT["HTTP GET\nhttp://mgr:9283/metrics"]
        PROXY --> CLIENT
    end

    subgraph CONSUMER["API Consumer"]
        UI["Dashboard UI\nor monitoring system"]
    end

    PROM_MOD -->|"HTTP :9283/metrics\nceph_osd_op_r{osd='0'}"| CLIENT
    UI -->|"gRPC / REST"| PROXY

    style PROM_MOD fill:#0d2228,stroke:#1a7a8a,color:#4fc0da
    style PROXY fill:#0d2218,stroke:#1a5040,color:#62e2b7
```

Add a `PrometheusProxy` RPC that: (1) reads the Prometheus endpoint URL from `mon_command("mgr dump")["services"]["prometheus"]`, (2) proxies the `/metrics` response, (3) optionally re-labels/aggregates specific metric families.

---

## 3. Orchestrator-Dependent Operations

### What It Is

Operations that modify cluster topology — adding/removing hosts, deploying or restarting daemons, rolling upgrades, and hardware SMART queries — go through the **Orchestrator module** running inside the MGR Python layer.

### How It Works Internally

```mermaid
sequenceDiagram
    actor USER as User / Dashboard
    participant CAPI as ceph-api
    participant LIBR as librados<br>(MgrCommand)
    participant MGR as MGR Daemon
    participant ORC as Orchestrator Module<br>(Python, inside MGR)
    participant BACKEND as Orchestrator Backend
    participant HOST as Remote Host

    Note over BACKEND: Either cephadm (SSH) or rook (k8s API)

    USER->>CAPI: gRPC DaemonRestart("osd.3")
    CAPI->>LIBR: conn.MgrCommand({"prefix":"orch daemon restart","name":"osd.3"})
    LIBR->>MGR: msgr2 TCP → MgrCommand RPC

    Note over CAPI,MGR: ✅ ceph-api CAN send this command

    MGR->>ORC: handle_command("orch daemon restart", args)

    Note over ORC: Dispatches to configured backend
    alt cephadm backend
        ORC->>BACKEND: _run_cephadm(host, "daemon", "restart", ["--name","osd.3"])
        BACKEND->>HOST: SSH exec → "cephadm daemon restart --name osd.3"
        HOST-->>BACKEND: exit 0
        BACKEND-->>ORC: CompletionStatus.OK
    else rook backend
        ORC->>BACKEND: k8s_client.patch(pod_name, {"spec": {"containers": [...]}})
        BACKEND-->>ORC: k8s patch applied
    end

    ORC-->>MGR: AsyncCompletion(result="restarted osd.3")
    MGR-->>LIBR: {"status": 0, "stdout": "restarted osd.3"}
    LIBR-->>CAPI: []byte(response)
    CAPI-->>USER: DaemonRestartResponse{ok}

    Note over CAPI,HOST: ⚠️ The actual SSH keys and<br>cephadm binary must exist on<br>the MGR host — ceph-api has<br>no role in that setup
```

### The Conditional Barrier

ceph-api CAN send all Orchestrator `mgr_commands`. The **conditional** part:

1. The cluster must have the `orchestrator` MGR module enabled and configured
2. The active backend (cephadm or rook) must be bootstrapped
3. For cephadm: SSH keys must be on the MGR host; cephadm must be installed on target hosts
4. For rook: k8s credentials and CRD access must be configured in the MGR pod

ceph-api has no control over any of these — they are cluster deployment concerns, not API concerns.

### What ceph-api Can Do Without an Orchestrator Backend

**All `orch *` commands require an Orchestrator backend** (`cephadm` or `rook`). Without one, every `orch` command — including read-only queries like `orch host ls` — raises `NoOrchestrator` with return code `-ENOENT` and the message `"No orchestrator configured (try \`ceph orch set backend\`)"`. Source: `src/pybind/mgr/orchestrator/_interface.py:1899,81` — `_oremote()` raises unconditionally when `_select_orchestrator()` returns `None`.

The following commands are **not `orch` commands** and work without any Orchestrator backend — they are core MGR or `devicehealth` module commands:

| mgr_command | Returns | Source |
|-------------|---------|--------|
| `{"prefix":"device ls","format":"json"}` | All known devices with health | `DaemonServer.cc` |
| `{"prefix":"device ls-by-host","hostname":"X"}` | Devices on specific host | `MgrCommands.h:209` |
| `{"prefix":"device get-health-metrics","devid":"X"}` | SMART health data | `devicehealth` module |
| `{"prefix":"osd safe-to-destroy","ids":["0"]}` | Whether OSD can be safely removed | `DaemonServer.cc:2073` |

The correct command for listing deployed daemons when an Orchestrator **is** configured is `orch ps` (not `orch daemon ls`, which does not exist).

---

## 4. RGW S3 Protocol Operations

### What It Is

RGW exposes **two completely separate API surfaces** on the same port:

1. **S3 API** (`/`) — implements the AWS S3 REST protocol for object operations AND bucket configuration (lifecycle, CORS, notifications, SSE, replication)
2. **Admin API** (`/admin/`) — RGW-specific management API for users, quotas, buckets (link/unlink/stat), GC, LC, usage logs

go-ceph's `rgw/admin` package wraps **only the Admin API**. Several important dashboard features use the S3 API's bucket configuration endpoints.

### The Two API Surfaces

```mermaid
flowchart TD
    subgraph RGW["RGW Daemon :7480"]
        subgraph ADMIN_API["Admin API /admin/*\n(covered by go-ceph rgw/admin)"]
            U["/admin/user\nUser CRUD, keys, caps"]
            B["/admin/bucket\nBucket link/unlink/stat/rm"]
            Q["/admin/quota\nUser + bucket quotas"]
            G["/admin/gc\nGarbage collection"]
            US["/admin/usage\nUsage log + trim"]
            INFO["/admin/info\nCluster info"]
        end
        subgraph S3_API["S3 API /*\n(NOT in go-ceph rgw/admin)"]
            NOTIF["PUT /bucket?notification\nBucket notifications"]
            LC["PUT /bucket?lifecycle\nLifecycle policies"]
            CORS["PUT /bucket?cors\nCORS policies"]
            SSE["PUT /bucket?encryption\nSSE configuration"]
            REPL["PUT /bucket?replication\nCross-region replication"]
            ACL["PUT /bucket?acl\nBucket ACLs"]
            TAGS["PUT /bucket?tagging\nBucket tags"]
        end
    end

    subgraph CEPI["ceph-api RGWService"]
        ADMCL["go-ceph rgw/admin\nHTTP client (SigV4)"]
        S3CL["Custom S3 client\n⚠️ needs implementation"]
    end

    ADMCL -->|"✅ works today"| ADMIN_API
    ADMCL -.->|"❌ not routed here"| S3_API
    S3CL -->|"🟠 implementable"| S3_API

    style S3CL fill:#201c0d,stroke:#5a4010,color:#f2c97c
    style S3_API fill:#201c0d,stroke:#5a4010,color:#f2c97c
```

### Sequence: What Happens for a Bucket Notification PUT

```mermaid
sequenceDiagram
    participant DASH as Dashboard (Python)
    participant RGWPY as rgw_client.py<br>(Python HTTP client)
    participant RGW as RGW Daemon
    participant CAPI as ceph-api (Go)
    participant ADMCL as go-ceph rgw/admin

    Note over DASH,RGWPY: Dashboard sets bucket notification
    DASH->>RGWPY: rgw_client.set_bucket_notification(bucket, config)
    RGWPY->>RGW: PUT /bucket-name?notification HTTP/1.1<br>Authorization: AWS4-HMAC-SHA256 ...<br>Body: <NotificationConfiguration>...</NotificationConfiguration>
    Note over RGW: S3 notification handler<br>src/rgw/rgw_op.cc::RGWPutBucketNotification
    RGW-->>RGWPY: 200 OK

    Note over CAPI,ADMCL: ceph-api tries the same via rgw/admin
    CAPI->>ADMCL: api.GetBucketInfo(bucket) → no notification field
    Note over ADMCL: go-ceph rgw/admin has no<br>SetBucketNotification method
    ADMCL-->>CAPI: ❌ method does not exist

    Note over CAPI: Must implement custom S3 HTTP client:<br>1. Discover RGW endpoint via mon_command("service dump")<br>2. Build AWS SigV4 signed request<br>3. PUT /bucket?notification with XML body<br>(or use aws-sdk-go-v2/service/s3)
```

### Implementation Path (Soft Block)

These operations can be implemented using `aws-sdk-go-v2/service/s3` pointed at the RGW endpoint:

```go
// Endpoint discovery
svcDump, _ := svc.ExecMon(ctx, `{"prefix":"service dump","format":"json"}`)
rgwEndpoint := parseRGWEndpoint(svcDump) // e.g. "http://192.168.1.10:7480"

// Configure AWS SDK with RGW endpoint
cfg, _ := config.LoadDefaultConfig(ctx,
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
        accessKey, secretKey, "",
    )),
    config.WithEndpointResolverWithOptions(
        aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
            return aws.Endpoint{URL: rgwEndpoint, HostnameImmutable: true}, nil
        }),
    ),
)
s3Client := s3.NewFromConfig(cfg)

// Now bucket notifications, lifecycle, CORS, SSE, replication all work
s3Client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{...})
```

Add `github.com/aws/aws-sdk-go-v2/service/s3` as a dependency.

---

## 5. CGO / go-ceph Runtime Constraints

These are **not blockers on functionality** but impose runtime safety constraints that must be handled correctly. Ignoring them causes goroutine leaks, unbounded memory growth, or silent data corruption.

### 5a. Context Cancellation Does Not Cancel In-flight Ceph Ops

```mermaid
sequenceDiagram
    participant CLIENT as gRPC Client
    participant HANDLER as Go Handler goroutine
    participant RADOS as pkg/rados ExecMon()
    participant CGO as CGO bridge
    participant LIBC as librados C
    participant MON as MON

    CLIENT->>HANDLER: gRPC request (with 5s deadline)
    HANDLER->>RADOS: ExecMon(ctx, "pg dump", ...)
    RADOS->>CGO: conn.MonCommand([]byte(cmd))

    Note over CGO: Go goroutine enters C call.<br>Go runtime CANNOT preempt<br>goroutines blocked in C.

    CGO->>LIBC: rados_mon_command() [blocking C call]
    LIBC->>MON: command dispatch via msgr2

    Note over CLIENT,HANDLER: 5 second deadline expires
    CLIENT-->>HANDLER: gRPC DeadlineExceeded sent to client
    HANDLER->>HANDLER: ctx.Done() fires
    HANDLER->>HANDLER: Handler goroutine returns error ✅

    Note over CGO,LIBC: ⚠️ CGO goroutine is still blocked<br>in rados_mon_command()!<br>It CANNOT be cancelled.<br>It CANNOT be preempted.<br>Go runtime has no handle on it.

    Note over CGO,LIBC: goroutine leaks here until<br>C call completes or process restarts

    MON-->>LIBC: response (arrives after timeout)
    LIBC-->>CGO: return (goroutine unblocks)
    CGO-->>RADOS: response dropped (no receiver)
    Note over RADOS: Memory freed, goroutine exits.<br>No crash — but latency spike and<br>goroutine count grows under load.
```

**Current mitigation in ceph-api:** `production_conn.go` sets `rados_osd_op_timeout` and `rados_mon_op_timeout` in the connection config so the C-level timeout fires and unblocks the C call eventually. This limits the goroutine leak duration to the configured timeout window.

**Recommended pattern:**

```go
func (s *Svc) ExecMonBounded(ctx context.Context, cmd string) ([]byte, error) {
    type result struct { data []byte; err error }
    ch := make(chan result, 1)
    go func() {
        // This goroutine may leak if CGO blocks, but it is bounded by rados_mon_op_timeout
        data, err := s.ExecMon(context.Background(), cmd)
        ch <- result{data, err}
    }()
    select {
    case res := <-ch:
        return res.data, res.err
    case <-ctx.Done():
        // Client context cancelled/timed out.
        // Background goroutine continues until C-level timeout fires.
        return nil, ctx.Err()
    }
}
```

### 5b. Goroutine Blocking Under Load

```mermaid
flowchart TD
    subgraph GORUNTIME["Go Runtime"]
        G1["goroutine 1\nExecMon(osd dump)"]
        G2["goroutine 2\nExecMon(pg dump)"]
        G3["goroutine 3\nExecMon(df detail)"]
        G4["goroutine 4\nwaiting for M"]
        G5["goroutine 5\nwaiting for M"]
        G6["goroutine 6\nwaiting for M"]
    end

    subgraph OS_THREADS["OS Threads (M)"]
        M1["M1\nrunning G1 C call\nlocked to OS thread"]
        M2["M2\nrunning G2 C call\nlocked to OS thread"]
        M3["M3\nrunning G3 C call\nlocked to OS thread"]
    end

    subgraph LIBRADOS["librados C (blocking)"]
        C1["rados_mon_command()\nblocked ~50ms"]
        C2["rados_mon_command()\nblocked ~50ms"]
        C3["rados_mon_command()\nblocked ~50ms"]
    end

    G1 --> M1 --> C1
    G2 --> M2 --> C2
    G3 --> M3 --> C3

    G4 -.->|"waiting - no M available\nbeyond GOMAXPROCS"| M1
    G5 -.->|"waiting"| M2
    G6 -.->|"waiting"| M3

    Note1["⚠️ Each CGO call consumes\none OS thread (M) for its duration.\nWith GOMAXPROCS=8, only 8\nconcurrent Ceph commands can run.\nAdditional requests queue."]

    style Note1 fill:#201c0d,stroke:#5a4010,color:#f2c97c
```

**Mitigation:** Use a semaphore to bound concurrent Ceph operations:

```go
type Svc struct {
    conn    RadosConnInterface
    limiter chan struct{} // e.g., make(chan struct{}, 16)
}

func (s *Svc) ExecMon(ctx context.Context, cmd string) ([]byte, error) {
    select {
    case s.limiter <- struct{}{}:
        defer func() { <-s.limiter }()
    case <-ctx.Done():
        return nil, ctx.Err()
    }
    // ... proceed with MonCommand
}
```

### 5c. RBD Trash List Pagination Gap

The go-ceph `rbd.ListTrashEntries(ioctx)` function internally calls `rbd_trash_list()` which is capped at **10,240 items** by the go-ceph retry allocator (`retry.WithSizes(32, 10240, ...)`). When there are more than 10,240 trash items, `rbd_trash_list` returns `-ERANGE` and go-ceph propagates it as an error — items are **NOT silently dropped; the entire call returns an error** (source: go-ceph `rbd/rbd.go`, retry.go #779).

```mermaid
sequenceDiagram
    participant HANDLER as RBDService handler
    participant RBD as go-ceph rbd
    participant LIBRBD as librbd C
    participant OSD as OSD (cls_rbd)

    HANDLER->>RBD: rbd.ListTrashEntries(ioctx)
    RBD->>LIBRBD: rbd_trash_list(io, entries, MAX_ENTRIES=10240)
    LIBRBD->>OSD: cls_rbd.dir_list (RADOS object read)
    OSD-->>LIBRBD: trash index: 15,000 entries
    LIBRBD-->>RBD: -ERANGE (buffer too small: 15,000 > 10,240 cap)
    RBD-->>HANDLER: error: -ERANGE

    Note over HANDLER: Handler receives error, NOT a partial list.<br>The 10,240 cap is the go-ceph retry max.<br>Items are NOT silently dropped.

    Note over HANDLER: Fix: handler-level pagination using<br>IoCtx.GetOmapWithKeys() as cursor<br>into the cls_rbd OMAP index.
```

**Mitigation:** Implement pagination at the handler level using the entry `id` field as a cursor:

```go
func listAllTrash(ioctx *rados.IOContext) ([]rbd.TrashImageInfo, error) {
    var all []rbd.TrashImageInfo
    for {
        batch, err := rbd.ListTrashEntries(ioctx) // max 10,240
        if err != nil { return nil, err }
        all = append(all, batch...)
        if len(batch) < 10240 { break } // no more pages
        // advance by removing the first 10,240 from the index
        // (implementation requires direct cls_rbd OMAP pagination)
    }
    return all, nil
}
```

Note: true cursor-based pagination requires calling the underlying `cls_rbd` OMAP directly via `IoCtx.GetOmapWithKeys()`, as go-ceph does not expose the offset parameter of `rbd_trash_list`.

### 5d. Memory Leak with Short-lived RADOS Connections

Creating and destroying `rados.Conn` objects rapidly causes RSS to grow toward the high-water mark of peak connection state. This is not an unbounded leak — each destroyed connection frees its resources — but glibc's ptmalloc2 allocator retains freed heap pages as RSS rather than returning them to the OS immediately, so RSS climbs with each cycle up to the peak allocation. Calling `malloc_trim(0)` or using a single long-lived connection avoids this.

**The pattern ceph-api already uses correctly:** a single long-lived `rados.Conn` created at startup (`pkg/rados/production_conn.go`), shared across all requests. Never create per-request connections.

---

## 6. Real-time Event Log Streaming

### What It Is

The `ceph -w` command displays new cluster log entries as they arrive. The dashboard's `GET /api/logs/audit` with long-polling semantics provides similar functionality. Both rely on getting log entries pushed as they are generated.

### How `ceph -w` Actually Works

`ceph -w` uses a genuine **push subscription**, not polling. Source: `src/ceph.in:1123–1157`.

```python
# src/ceph.in (simplified)
run_in_thread(cluster_handle.monitor_log2, level, watch_cb, 0)
signal.pause()  # blocks; watch_cb fires on each new entry
```

The call chain:
1. `monitor_log2()` in `src/pybind/rados/rados.pyx:1305` → `rados_monitor_log2()` C API
2. `src/librados/RadosClient.cc:976` → `monclient.sub_want("log-info", 0, 0)` + `renew_subs()` — sends `MMonSubscribe` to the Monitor
3. `src/mon/LogMonitor.cc:1077` (`check_subs`) — Monitor pushes new `MLog` messages to all subscribers when entries are committed
4. `RadosClient::handle_log()` at line 1031 — invokes callback and advances the subscription watermark via `sub_got()`

There is **no polling loop and no 200ms interval**. The `rados_monitor_log` / `rados_monitor_log2` C API is a documented, public push interface (`src/include/rados/librados.h:4087`). The Mon's `LogMonitor` handles subscriptions for `"log-debug"`, `"log-info"`, `"log-warn"`, `"log-error"` via the same `MMonSubscribeAck` mechanism used for OSDMap and MDSMap subscriptions. Log entries are stored in the Monitor's internal **RocksDB** (MonitorDBStore prefix `"logm"`) — they have nothing to do with RADOS objects or the config-key store.

De-duplication is by **Paxos version watermark** (`sub_got()` advances `s->next`), not by timestamp or message hash. The Monitor only sends entries with version > the client's last acknowledged version.

### Why Push Is Unavailable in ceph-api Today

The librados C API `rados_monitor_log2` IS a real push mechanism. The barrier for ceph-api is that **go-ceph v0.26.0 does not wrap `rados_monitor_log`** — there is no `MonitorLog` function in the go-ceph rados package (confirmed in `github.com/ceph/go-ceph@v0.32.0/rados/`).

Two paths to true push in ceph-api:
1. **Direct CGO call** — call `rados_monitor_log2` directly from Go via `import "C"`, bypassing go-ceph. The callback fires in a C thread; it must post to a Go channel via `CGO_EXPORT`.
2. **MGR plugin webhook** — a small Python MGR module registers via `NotificationQueue` and HTTP-POSTs each `clog` entry to ceph-api.

### Polling Workaround (for now)

Until CGO push is implemented, polling every 3–5 seconds via `log last` is adequate for dashboard-style log views:

```mermaid
sequenceDiagram
    participant CLIENT as API Client
    participant CAPI as ceph-api LogService
    participant RADOS as pkg/rados

    CLIENT->>CAPI: gRPC LogService.Watch(channel="cluster") [streaming RPC]
    Note over CAPI: Opens server-streaming gRPC response

    loop every 3 seconds
        CAPI->>RADOS: ExecMon(ctx, {"prefix":"log last","num":100,"channel":"cluster"})
        RADOS-->>CAPI: last 100 entries
        CAPI->>CAPI: filter entries with version > lastSeen
        alt new entries exist
            CAPI-->>CLIENT: stream LogEntry{timestamp, channel, message}
            CAPI->>CAPI: update lastSeen = max(entry.version)
        end
    end

    CLIENT->>CAPI: cancel stream
```

The `log last` Mon command supports `num`, `level` (`debug|info|sec|warn|error`), and `channel` (`cluster|audit|cephadm`) parameters (source: `src/mon/MonCommands.h:227–232`).

---

## 7. MGR Module Private KV Store

### What It Is

MGR Python modules can store and retrieve private data using:

```python
self.set_store("my_setting", "my_value")
value = self.get_store("my_setting")
```

This is used by the dashboard for things like: Grafana URL, JWT secret seed, MOTD, audit log settings, and other per-module configuration that doesn't fit the `config set` namespace.

### How It Works Internally

```mermaid
flowchart LR
    subgraph MGR["MGR Daemon"]
        MOD["Python Module\nmodule.py"]
        BM["BaseMgrModule.cc\nC extension"]
        MC["MonClient\n(embedded librados)"]
    end

    subgraph CEPH["Ceph Config-Key Store\n(MonitorDBStore RocksDB, prefix 'mon_config_key')"]
        KEY1["mgr/dashboard/jwt_secret"]
        KEY2["mgr/dashboard/accessdb_v2"]
        KEY3["mgr/dashboard/<mgr-id>/crt"]
        KEY4["mgr/telemetry/last_opt_revision"]
    end

    MOD -->|"set_store('jwt_secret', '...')"| BM
    BM -->|"mon_command(config-key set\nmgr/dashboard/jwt_secret=...)"| MC
    MC -->|"msgr2 TCP"| KEY1

    MOD -->|"get_store('accessdb_v2')"| BM
    BM -->|"mon_command(config-key get\nmgr/dashboard/accessdb_v2)"| MC
    MC -->|"msgr2 TCP"| KEY2
```

### The Undocumented Access Path

The config-key store is accessible externally via `mon_command`:

```json
{"prefix": "config-key get", "key": "mgr/dashboard/jwt_secret"}
{"prefix": "config-key set", "key": "mgr/dashboard/jwt_secret", "val": "..."}
{"prefix": "config-key ls"}
```

**Important:** `config-key` access requires `mon 'allow *'` (admin) or an explicit `config-key` capability grant. A client with only `mon 'allow r'` or `mon 'allow rw'` cannot access config-key — the Mon's `MonCap` explicitly exempts the `config-key` service from blanket caps (source: `src/mon/MonCap.cc:427`). Only the `"profile mgr"` capability or `allow *` grants implicit config-key access.

**The limitation is:**
1. The key names are **module-specific and undocumented** — no public schema exists
2. They can change between Ceph versions
3. Writing to another module's KV store is unusual and could break that module
4. Note: `jwt_token_ttl` and `GRAFANA_API_URL` are **module options** (accessed via `get_module_option()`/`set_module_option()`, stored in the ceph config subsystem), not `set_store()` keys — they are NOT in the config-key store

### Practical Guidance

For ceph-api, this matters only for reading/writing settings that the dashboard or other modules store privately. For operational features (not dashboard compatibility), ceph-api uses `config set/get` for all its own settings — which is the documented, stable API.

If a specific module store key is needed (e.g., reading the JWT secret the dashboard set), the access path is:

```go
jwtSecret, err := svc.ExecMon(ctx, `{"prefix":"config-key get","key":"mgr/dashboard/jwt_secret","format":"plain"}`)
```

Note: Grafana URL, JWT TTL, and similar per-module settings are **module options** stored via `get_module_option()`/`set_module_option()`, not in config-key. Access those via `{"prefix":"config get","who":"mgr","key":"mgr/dashboard/<option_name>"}` or `{"prefix":"config set","who":"mgr.module.dashboard","name":"<option_name>","value":"..."}`.

---

## 8. Workaround Summary

| Infeasible operation | Workaround |
|---------------------|------------|
| Per-OSD/MDS/MON perf counter time-series | Proxy Prometheus at `http://<mgr>:9283/metrics`; discover URL via `mon_command("mgr dump")["services"]["prometheus"]` |
| Orchestrator daemon/host lifecycle | `mgr_command("orch daemon restart/add/rm")` — works IF Orchestrator configured; return descriptive error if not |
| RGW bucket notifications | `aws-sdk-go-v2/service/s3` + RGW endpoint discovery via `service dump` |
| RGW lifecycle/CORS/SSE/replication | Same AWS SDK approach |
| Context cancellation of CGO ops | Wrapper goroutine + `select` on `ctx.Done()` to abandon result; set `rados_osd_op_timeout` for OSD-level C-side timeout. Note: `rados_mon_op_timeout` does NOT bound `rados_mon_command` — `ctx.wait()` in `RadosClient::mon_command` has no timeout (source: `src/librados/RadosClient.cc`) |
| RBD trash list > 10,240 (returns `-ERANGE`, not silent drop) | Handler-level pagination via `IoCtx.GetOmapWithKeys()` as cursor into `cls_rbd` OMAP |
| Real-time log streaming | Near-term: gRPC server-streaming RPC wrapping `log last` poll (3–5s, filter by Paxos version); long-term: direct CGO call to `rados_monitor_log2` for true push |
| MGR module private KV (specific known keys) | `mon_command("config-key get/set key=mgr/<module>/<keyname>")` |
