---
name: proto-grpc-gateway
description: Use when shaping a ported endpoint's protobuf messages or http.yaml REST mapping — choosing proto types (scalars, enums, oneof, maps, well-known types) and body/param bindings that round-trip the dashboard's wire without silent divergence.
---

# Modeling a dashboard endpoint as proto + grpc-gateway

The ported message + route must round-trip the dashboard's **real** REST
wire unchanged. `anatomy.md` has the file mechanics and the worked
`crush_rule` example; CLAUDE.md "Parity vs API quality" sets the
typed-over-ignore preference. This is the capability reference: what the
mapping and type system here can express, and how each lands on the JSON
wire. Versions are pinned in `go.mod` (grpc-gateway v2, protobuf v1.36) —
behavior below is for those; re-verify against the module source if a
version moves.

## REST mapping — beyond the basics

`anatomy.md` §"HTTP mapping" has the rule shape (`selector`, verb, path
`{params}`, `body`, `response_body`). The non-obvious, decision-relevant
parts:

- **Binding order:** body decoded first, then path params, then query
  params for leftover fields. A field in the path or body is excluded from
  query binding; with `body: "*"` there is **no** query binding at all.
- **Query params** can set singular scalars, dotted paths into a *singular*
  nested message, `repeated` (repeat the key), `map` (`key[k]=v`), enums
  (name or number), and WKTs from their string form — but **cannot** descend
  into a `repeated` message. Use `body: "<field>"` (not `"*"`) when you need
  both a body and query params.
- **`additional_bindings`** adds extra routes for one rpc (one level deep).
- **PATCH + `FieldMask`:** if the verb is `patch`, the request has exactly
  one `FieldMask` field, and the body is a **named field** (not `"*"`), the
  gateway auto-derives the mask from the JSON keys actually sent. With
  `body: "*"` the FieldMask is a plain field the client must set. See
  `third_party/grpc_gateway/docs/patch_feature.md`.
- **Non-JSON response** (text/CSV/binary) → return `google.api.HttpBody`;
  the handler sets `content_type` + `data` and the gateway emits them
  verbatim, bypassing the JSON marshaler.
- **Path param containing `/`** (a Ceph name/id with a slash) mis-routes,
  because the gateway unescapes the whole path by default — register the mux
  with `runtime.WithUnescapingMode(runtime.UnescapingModeAllExceptReserved)`.

## Runtime marshaler rules (the gotchas)

Set once in `pkg/api/grpc_http_gateway.go`; confirm there if surprised.
These three drive most divergences:

- **`DiscardUnknown` (request):** a body/query key with no matching field
  is **silently dropped** — no 4xx, handler sees zero values, returns 2xx.
  Every key the dashboard sends must be a declared field. (Also: an unknown
  **enum name** string decodes to *unset*, silently.)
- **`EmitUnpopulated` (response):** every field is emitted even at zero
  value — so you **cannot** honor a query param that whitelists which
  fields to return (`attrs`-style). Record that as a documented divergence.
- **`UseProtoNames` (response):** wire keys are the proto (snake_case)
  names; `json_names_for_fields=false` keeps the generated swagger matching
  them. Name fields to match the dashboard's JSON keys; use `json_name`
  only for a genuinely camelCase dashboard key.

## Type toolbox — proto construct → JSON wire form

Pick the most specific type (CLAUDE.md). Grep `api/*.proto` for a live
example before inventing one.

| Dashboard value | Proto | JSON wire form |
|---|---|---|
| small int | `int32`/`uint32` | number |
| 64-bit int (verify in `third_party/ceph/`) | `int64`/`uint64` | **string** (matcher coerces ↔ number) |
| bool / float | `bool` / `double` | bool / number |
| timestamp | `google.protobuf.Timestamp` | RFC3339 `...Z` (never a unix int) |
| fixed wire-string set | `enum`, value names == wire strings | the value-name string |
| nullable / must distinguish absent | `optional <scalar>` or a `google.protobuf.*Value` wrapper | value, or `null`/absent |
| list | `repeated` | array |
| keyed object, homogeneous values | `map<string, X>` | object |
| structured sub-object, known shape | nested `message` | object |
| object whose keys/shape vary | `google.protobuf.Struct` | object |
| one value of unknown JSON type | `google.protobuf.Value` | any JSON value |
| mutually-exclusive request/response variants | `oneof` | only the set member appears |
| void request or response | `google.protobuf.Empty` | `{}` |

All standard well-known types are importable here (timestamp, struct,
empty are already used; duration, wrappers, any, field_mask are equally
available from the buf WKT set) — `Duration`→`"1.5s"`, wrappers→bare
value, `Any`→`@type` object, `FieldMask`→comma string (camelCased).

## Mapping decision rules

- **List endpoint** → unwrap with `response_body: "<field>"` so the body
  is the bare array, not `{"field": [...]}`.
- **64-bit counts** → `int64`/`uint64`; the parity matcher already bridges
  the string↔number wire shape — no ignore needed.
- **Fixed string set** → `enum` whose value **names** equal the wire
  strings. protojson serializes/parses an enum by name only (number as
  fallback) and openapiv2 emits the name — **no enum-value option**
  (`allow_alias`, `deprecated`, …) can rewrite the wire string. So if a
  Ceph wire string isn't a valid identifier (starts with a digit, has a
  dash, collides), don't reach for options — use a plain `string` field, or
  keep the enum and do the string↔value conversion yourself in Go. (An
  unknown enum name in a request is also silently dropped under
  `DiscardUnknown`.)
- **Heterogeneous or version-variant object** → `Struct`; a single
  unknown-typed value → `Value`. Don't hand-type a shape that drifts
  across Ceph versions.
- **Mutually-exclusive variants** → `oneof`. Note the JSON contract: only
  the set member is emitted, an **unset oneof is never emitted even with
  `EmitUnpopulated`**, and setting two members on decode is an error.
- **Flat open key set** (a controller `**kwargs` that forwards arbitrary
  top-level body keys): each key must be a **top-level field**, or make the
  whole request a `Struct` body. **Never** a nested `map<string,...>` — the
  client sends the keys flat, they match no field, `DiscardUnknown` drops
  them, and the resource is built with defaults at 2xx. A `map` is one
  nested field, never a sink for flat siblings.
- **Field-whitelist param** (`attrs`-style) → can't be honored under
  `EmitUnpopulated`; return the full object and record the divergence.

## Worked examples

`third_party/grpc_gateway/` vendors a curated set of grpc-gateway examples
plus the `grpc_api_configuration` and `patch_feature` docs (see its
`README.md` index). Use them for proto type/shape patterns — `oneof`,
wrappers, `FieldMask`, `Struct`/`Value`, `response_body` unwrap; their
routes use in-proto annotations, ours use `http.yaml`. Richest single file:
`examples/a_bit_of_everything.proto`.
