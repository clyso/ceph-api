# Vendored grpc-gateway reference material

Read-only reference snapshots from `github.com/grpc-ecosystem/grpc-gateway`
(v2.29.0-229-g3b07844f; commit 3b07844f), BSD-2-Clause (see `LICENSE`).

**Caveat:** the example `.proto` files use **in-proto `google.api.http` annotations**;
this repo binds routes via an **external `api/http.yaml`** instead. Use them for proto
message/type/shape patterns and what is expressible — not as binding-syntax templates.

- `docs/grpc_api_configuration.md` — the external-config (`http.yaml`) workflow this repo uses.
- `docs/patch_feature.md` — PATCH + `FieldMask` auto-population rules.
- `examples/a_bit_of_everything.proto` — richest single example (types, oneof, WKTs, FieldMask, additional_bindings); the `openapiv2_*` option blocks are irrelevant here.
- `examples/wrappers.proto` — `google.protobuf.*Value` wrapper fields (nullable scalars).
- `examples/proto3_field_semantics.proto` — recursive `oneof` (tagged union).
- `examples/echo_service.proto` — multiple oneofs, `Struct`/`Value`, PATCH+FieldMask with a named body.
- `examples/response_body_service.proto` — `response_body` selecting a sub-field as the payload.
