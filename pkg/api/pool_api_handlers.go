package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func NewPoolAPI(radosSvc *rados.Svc) pb.PoolServer {
	return &poolAPI{
		radosSvc: radosSvc,
	}
}

type poolAPI struct {
	radosSvc *rados.Svc
}

func (p *poolAPI) CreatePool(ctx context.Context, req *pb.CreatePoolRequest) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
		return nil, err
	}

	if req.Pool == "" {
		return nil, fmt.Errorf("%w: pool is required", types.ErrInvalidArg)
	}
	if req.PgNum <= 0 {
		return nil, fmt.Errorf("%w: pg_num is required", types.ErrInvalidArg)
	}
	if req.PoolType != "replicated" && req.PoolType != "erasure" {
		return nil, fmt.Errorf("%w: pool_type must be 'replicated' or 'erasure'", types.ErrInvalidArg)
	}
	if req.Configuration != nil {
		return nil, fmt.Errorf("%w: pool configuration is not supported", types.ErrNotImplemented)
	}
	if req.RbdMirroring != nil {
		return nil, fmt.Errorf("%w: rbd_mirroring is not supported", types.ErrNotImplemented)
	}

	cmds := poolCreateCommands(req)
	for _, cmd := range cmds {
		cmdBytes, err := json.Marshal(cmd)
		if err != nil {
			return nil, err
		}
		if _, err := p.radosSvc.ExecMon(ctx, string(cmdBytes)); err != nil {
			return nil, err
		}
	}

	return &emptypb.Empty{}, nil
}

// poolCreateCommands reproduces the dashboard Pool.create command sequence:
// create -> ec_overwrites -> application enable -> set-quota -> osd pool set
// per leftover kwarg. The ordering matches the controller so the parity test
// and the audit log agree.
func poolCreateCommands(req *pb.CreatePoolRequest) []map[string]interface{} {
	var cmds []map[string]interface{}

	create := map[string]interface{}{
		"prefix":    "osd pool create",
		"format":    "json",
		"pool":      req.Pool,
		"pg_num":    req.PgNum,
		"pgp_num":   req.PgNum,
		"pool_type": req.PoolType,
	}
	if req.ErasureCodeProfile != nil && *req.ErasureCodeProfile != "" {
		create["erasure_code_profile"] = *req.ErasureCodeProfile
	}
	if req.RuleName != nil {
		create["rule"] = *req.RuleName
	}
	cmds = append(cmds, create)

	if poolFlagsHaveECOverwrites(req.Flags) {
		cmds = append(cmds, map[string]interface{}{
			"prefix": "osd pool set",
			"format": "json",
			"pool":   req.Pool,
			"var":    "allow_ec_overwrites",
			"val":    "true",
		})
	}

	for _, app := range req.ApplicationMetadata {
		cmds = append(cmds, map[string]interface{}{
			"prefix":               "osd pool application enable",
			"format":               "json",
			"pool":                 req.Pool,
			"app":                  app,
			"yes_i_really_mean_it": true,
		})
	}

	// Quotas first, then the remaining set-keys, matching _set_quotas before
	// _set_pool_keys in the controller.
	if req.QuotaMaxObjects != nil {
		cmds = append(cmds, poolSetQuotaCmd(req.Pool, "max_objects", strconv.FormatInt(*req.QuotaMaxObjects, 10)))
	}
	if req.QuotaMaxBytes != nil {
		cmds = append(cmds, poolSetQuotaCmd(req.Pool, "max_bytes", strconv.FormatInt(*req.QuotaMaxBytes, 10)))
	}

	for _, kv := range poolSetKeys(req) {
		cmds = append(cmds, poolSetCmd(req.Pool, kv.key, kv.val))
	}

	return cmds
}

type poolSetKV struct {
	key string
	val string
}

// poolSetKeys returns the leftover **kwargs set-keys (everything except the
// named create args and quotas) in a deterministic order, each value
// stringified as the dashboard does via str(value).
func poolSetKeys(req *pb.CreatePoolRequest) []poolSetKV {
	var kvs []poolSetKV
	if req.PgAutoscaleMode != nil {
		kvs = append(kvs, poolSetKV{"pg_autoscale_mode", *req.PgAutoscaleMode})
	}
	if req.Size != nil {
		kvs = append(kvs, poolSetKV{"size", strconv.FormatInt(int64(*req.Size), 10)})
	}
	if req.CompressionMode != nil {
		kvs = append(kvs, poolSetKV{"compression_mode", *req.CompressionMode})
	}
	if req.CompressionAlgorithm != nil {
		kvs = append(kvs, poolSetKV{"compression_algorithm", *req.CompressionAlgorithm})
	}
	if req.CompressionRequiredRatio != nil {
		kvs = append(kvs, poolSetKV{"compression_required_ratio", formatPoolFloat(*req.CompressionRequiredRatio)})
	}
	if req.CompressionMinBlobSize != nil {
		kvs = append(kvs, poolSetKV{"compression_min_blob_size", strconv.FormatInt(*req.CompressionMinBlobSize, 10)})
	}
	if req.CompressionMaxBlobSize != nil {
		kvs = append(kvs, poolSetKV{"compression_max_blob_size", strconv.FormatInt(*req.CompressionMaxBlobSize, 10)})
	}
	if req.TargetSizeBytes != nil {
		kvs = append(kvs, poolSetKV{"target_size_bytes", strconv.FormatInt(*req.TargetSizeBytes, 10)})
	}
	if req.TargetSizeRatio != nil {
		kvs = append(kvs, poolSetKV{"target_size_ratio", formatPoolFloat(*req.TargetSizeRatio)})
	}
	if req.PgNumMin != nil {
		kvs = append(kvs, poolSetKV{"pg_num_min", strconv.FormatInt(int64(*req.PgNumMin), 10)})
	}
	if req.PgNumMax != nil {
		kvs = append(kvs, poolSetKV{"pg_num_max", strconv.FormatInt(int64(*req.PgNumMax), 10)})
	}
	return kvs
}

func poolSetCmd(pool, key, val string) map[string]interface{} {
	return map[string]interface{}{
		"prefix": "osd pool set",
		"format": "json",
		"pool":   pool,
		"var":    key,
		"val":    val,
	}
}

// poolFlagsHaveECOverwrites mirrors the controller's only inspection of the
// flags array: enabling allow_ec_overwrites when ec_overwrites is present.
func poolFlagsHaveECOverwrites(flags []string) bool {
	for _, f := range flags {
		if f == "ec_overwrites" {
			return true
		}
	}
	return false
}

// formatPoolFloat stringifies a ratio for the osd pool set CephString val,
// matching the dashboard's str(value) (e.g. 0.8 -> "0.8").
func formatPoolFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func poolSetQuotaCmd(pool, field, val string) map[string]interface{} {
	return map[string]interface{}{
		"prefix": "osd pool set-quota",
		"format": "json",
		"pool":   pool,
		"field":  field,
		"val":    val,
	}
}

func (p *poolAPI) ListPools(ctx context.Context, req *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
		return nil, err
	}

	if req.Stats != nil && *req.Stats {
		return nil, fmt.Errorf("%w: stats are not supported", types.ErrNotImplemented)
	}

	dumpRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	crushRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd crush dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}

	var osdDump struct {
		Pools []map[string]interface{} `json:"pools"`
	}
	if err := json.Unmarshal(sanitizeCephFloats(dumpRes), &osdDump); err != nil {
		return nil, err
	}
	var crushDump struct {
		Rules []struct {
			RuleID   int    `json:"rule_id"`
			RuleName string `json:"rule_name"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(crushRes, &crushDump); err != nil {
		return nil, err
	}
	crushRules := make(map[int]string, len(crushDump.Rules))
	for _, r := range crushDump.Rules {
		crushRules[r.RuleID] = r.RuleName
	}

	attrs := parsePoolAttrs(req.Attrs)

	resp := &pb.ListPoolsResponse{Pools: make([]*structpb.Struct, 0, len(osdDump.Pools))}
	for _, pool := range osdDump.Pools {
		serialized, err := structpb.NewStruct(serializePool(pool, attrs, crushRules))
		if err != nil {
			return nil, err
		}
		resp.Pools = append(resp.Pools, serialized)
	}
	return resp, nil
}

// parsePoolAttrs splits the comma-separated attrs whitelist. A nil/empty value
// (no filtering requested) returns nil, meaning "all attributes".
func parsePoolAttrs(attrs *string) []string {
	if attrs == nil || *attrs == "" {
		return nil
	}
	return strings.Split(*attrs, ",")
}

// serializePool reproduces the dashboard's Pool._serialize_pool: optional
// attribute whitelisting (pool_name always kept) plus the four field
// transforms. crushRules maps rule_id -> rule_name for the crush_rule lookup.
func serializePool(pool map[string]interface{}, attrs []string, crushRules map[int]string) map[string]interface{} {
	keys := attrs
	if len(keys) == 0 {
		keys = make([]string, 0, len(pool))
		for k := range pool {
			keys = append(keys, k)
		}
	}

	res := make(map[string]interface{}, len(keys)+1)
	for _, attr := range keys {
		val, ok := pool[attr]
		if !ok {
			continue
		}
		switch attr {
		case "type":
			res[attr] = poolTypeName(val)
		case "crush_rule":
			res[attr] = poolCrushRuleName(val, crushRules)
		case "application_metadata":
			res[attr] = applicationMetadataKeys(val)
		default:
			res[attr] = val
		}
	}

	res["pool_name"] = pool["pool_name"]
	return res
}

// poolTypeName maps the numeric pool type to its wire string, mirroring the
// dashboard's {1: 'replicated', 3: 'erasure'} lookup. JSON numbers decode to
// float64. Documented divergence: the dashboard's dict index raises
// KeyError -> 500 on an unmapped type (pool.py:111); we pass the raw value
// through instead, since Ceph only ever emits 1 or 3 here (see task §11).
func poolTypeName(v interface{}) interface{} {
	n, ok := v.(float64)
	if !ok {
		return v
	}
	switch int(n) {
	case 1:
		return "replicated"
	case 3:
		return "erasure"
	default:
		return v
	}
}

// poolCrushRuleName looks the crush rule_id up to its name. Documented
// divergence: the dashboard's dict index raises KeyError -> 500 when the
// rule_id is absent from the crush dump (pool.py:113); we pass the raw id
// through instead (see task §11).
func poolCrushRuleName(v interface{}, crushRules map[int]string) interface{} {
	n, ok := v.(float64)
	if !ok {
		return v
	}
	if name, ok := crushRules[int(n)]; ok {
		return name
	}
	return v
}

// applicationMetadataKeys turns the {"rgw": {}} object into a sorted list of
// its keys (["rgw"]). Go map iteration is unordered; sorting keeps the output
// array deterministic for the positional parity matcher (the dashboard's
// list(dict.keys()) is insertion-ordered, but a stable order is what parity
// needs).
func applicationMetadataKeys(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]interface{}, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

// cephFloatToken matches Ceph's bare inf/-inf/nan/-nan JSON float tokens, which
// Go's encoding/json rejects (src/common/Formatter.cc dump_float streams the
// double via std::ostream, so libstdc++ emits negative NaN as bare -nan). They
// appear as a value after a ':' or '[' / ',', never inside a quoted string.
var cephFloatToken = regexp.MustCompile(`([:\[,]\s*)(-?inf|-?nan)\b`)

// sanitizeCephFloats rewrites bare inf/-inf/nan/-nan tokens to valid JSON
// strings so the response unmarshals. inf/-inf become "Infinity" to match the
// dashboard's read_balance conversion (pool.py:120-121). nan/-nan become "NaN":
// the dashboard keeps a real NaN float (Python json parses bare nan), but Go's
// encoding/json can neither parse nor emit a NaN float, so "NaN" is a forced
// valid-JSON divergence (task §11; read_balance scores are excluded from the
// parity whitelist, so this does not flap parity).
func sanitizeCephFloats(b []byte) []byte {
	return cephFloatToken.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := cephFloatToken.FindSubmatch(m)
		prefix, tok := sub[1], string(sub[2])
		repl := `"NaN"`
		if tok == "inf" || tok == "-inf" {
			repl = `"Infinity"`
		}
		return append(append([]byte{}, prefix...), repl...)
	})
}
