package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"

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

// Body keys consumed as named fields; everything else is forwarded as an
// `osd pool set var=<key> val=<value>` command (the dashboard's **kwargs).
var poolNamedKeys = map[string]struct{}{
	"pool":                 {},
	"pg_num":               {},
	"pool_type":            {},
	"erasure_code_profile": {},
	"flags":                {},
	"rule_name":            {},
	"application_metadata": {},
	"quota_max_objects":    {},
	"quota_max_bytes":      {},
	// Out of scope (RBD/rbd-mirroring resources); accepted and ignored.
	"configuration": {},
	"rbd_mirroring": {},
}

func (p *poolAPI) CreatePool(ctx context.Context, req *structpb.Struct) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
		return nil, err
	}

	cmds, err := buildCreatePoolCommands(req.AsMap())
	if err != nil {
		return nil, err
	}

	// Non-atomic by design, mirroring the dashboard: create then a sequence of
	// set/set-quota/application-enable. A later command failing (e.g.
	// allow_ec_overwrites on a replicated pool) leaves the pool created but
	// partially configured; the dashboard's create is equally non-transactional.
	for _, cmd := range cmds {
		cmdBytes, err := json.Marshal(cmd)
		if err != nil {
			return nil, err
		}
		if _, err := p.radosSvc.ExecMon(ctx, string(cmdBytes)); err != nil {
			return nil, mapMonError(err)
		}
	}

	return &emptypb.Empty{}, nil
}

func (p *poolAPI) ListPools(ctx context.Context, req *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
		return nil, err
	}

	// The stats=true branch needs per-pool rate/rates derived from an in-mgr
	// lifetime time-series of pool stats, which is a stateful component this
	// service deliberately does not reimplement (see tasks/get-api-pool.md
	// §Open decisions). The no-stats path is faithful; stats is unsupported.
	if req.Stats {
		return nil, fmt.Errorf("%w: stats=true is not supported", types.ErrNotImplemented)
	}

	dump, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	crush, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd crush dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}

	ruleNames, err := parseCrushRuleNames(crush)
	if err != nil {
		return nil, err
	}

	pools, err := serializePools(dump, ruleNames)
	if err != nil {
		return nil, err
	}

	out := make([]*structpb.Struct, 0, len(pools))
	for _, pool := range pools {
		s, err := structpb.NewStruct(pool)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", types.ErrInternal, err.Error())
		}
		out = append(out, s)
	}

	return &pb.ListPoolsResponse{Pools: out}, nil
}

// floatTokenRe matches the bare inf/-inf/nan tokens Ceph's JSON formatter can
// emit (Formatter.cc, dump_float streams a double so non-finite values print
// unquoted) which Go's encoding/json rejects. Only matches a token that sits
// where a JSON value is expected (after ':' or '[' or ',').
var floatTokenRe = regexp.MustCompile(`([:\[,]\s*)(-?inf|nan)\b`)

// sanitizeCephJSON quotes the bare non-finite float tokens so the document
// unmarshals. The dashboard substitutes inf with "Infinity" (only inside
// read_balance, pool.py:120-121) but leaves nan as-is; we mirror that
// distinction here — inf→"Infinity", nan→"NaN" — rather than collapsing both
// to "Infinity". Live values are finite, so this is a safety path.
func sanitizeCephJSON(b []byte) []byte {
	return floatTokenRe.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := floatTokenRe.FindSubmatch(m)
		repl := `"Infinity"`
		if string(sub[2]) == "nan" {
			repl = `"NaN"`
		}
		return append(append([]byte{}, sub[1]...), repl...)
	})
}

// parseCrushRuleNames builds the rule_id → rule_name map from `osd crush dump`.
func parseCrushRuleNames(crushDumpBytes []byte) (map[int]string, error) {
	var dump struct {
		Rules []struct {
			RuleID   int    `json:"rule_id"`
			RuleName string `json:"rule_name"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(crushDumpBytes, &dump); err != nil {
		return nil, err
	}
	names := make(map[int]string, len(dump.Rules))
	for _, r := range dump.Rules {
		names[r.RuleID] = r.RuleName
	}
	return names, nil
}

// poolTypeNames maps the osd dump integer pool type to the dashboard string
// (pool.py:111).
var poolTypeNames = map[int]string{1: "replicated", 3: "erasure"}

// serializePools parses `osd dump` and applies the dashboard _serialize_pool
// transforms (pool.py:99-130): type int→string, crush_rule id→name,
// application_metadata dict→list-of-keys, read_balance inf→"Infinity".
func serializePools(osdDumpBytes []byte, ruleNames map[int]string) ([]map[string]any, error) {
	var dump struct {
		Pools []json.RawMessage `json:"pools"`
	}
	if err := json.Unmarshal(sanitizeCephJSON(osdDumpBytes), &dump); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(dump.Pools))
	for _, raw := range dump.Pools {
		var pool map[string]any
		if err := json.Unmarshal(raw, &pool); err != nil {
			return nil, err
		}
		// Go's map decode loses object key order, but the dashboard emits
		// application_metadata as list(dict.keys()) (pool.py:115) — i.e. the
		// order the keys appear in the osd dump JSON. Recover that order from
		// the raw bytes so a multi-app pool diffs clean against the dashboard.
		appOrder, err := objectKeyOrder(raw, "application_metadata")
		if err != nil {
			return nil, err
		}
		if err := serializePool(pool, ruleNames, appOrder); err != nil {
			return nil, err
		}
		out = append(out, pool)
	}
	return out, nil
}

// objectKeyOrder returns the keys of the named top-level object field in
// source order. Returns nil if the field is absent or is not an object.
func objectKeyOrder(raw json.RawMessage, field string) ([]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	val, ok := top[field]
	if !ok {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(val))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, nil
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, keyTok.(string))
		// Skip the value (may be a nested object/array).
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// serializePool applies the per-field transforms in place. appOrder carries
// the source order of application_metadata keys (Go maps lose it). A type or
// crush_rule value with no mapping is an error, mirroring the dashboard's
// dict-lookup KeyError → 500 (pool.py:111,113) rather than silently leaking
// the raw int where the wire contract is a string.
func serializePool(pool map[string]any, ruleNames map[int]string, appOrder []string) error {
	if t, ok := pool["type"].(float64); ok {
		name, ok := poolTypeNames[int(t)]
		if !ok {
			return fmt.Errorf("%w: unknown pool type %d", types.ErrInternal, int(t))
		}
		pool["type"] = name
	}
	if id, ok := pool["crush_rule"].(float64); ok {
		name, ok := ruleNames[int(id)]
		if !ok {
			return fmt.Errorf("%w: unknown crush rule id %d", types.ErrInternal, int(id))
		}
		pool["crush_rule"] = name
	}
	if _, ok := pool["application_metadata"].(map[string]any); ok {
		keys := make([]any, 0, len(appOrder))
		for _, k := range appOrder {
			keys = append(keys, k)
		}
		pool["application_metadata"] = keys
	}
	return nil
}

// mapMonError maps a failed mon command's errno onto the semantically correct
// sentinel so the ErrorInterceptor emits a 4xx for user errors instead of a
// blanket 500. go-ceph surfaces the Ceph errno via the ErrorCode() interface
// (available on the real RADOS error; the !cgo mock never returns one).
func mapMonError(err error) error {
	var coder interface{ ErrorCode() int }
	if !errors.As(err, &coder) {
		return err
	}
	switch coder.ErrorCode() {
	case -22: // -EINVAL: rejected arguments (e.g. ec_overwrites on a replicated pool)
		return fmt.Errorf("%w: %s", types.ErrInvalidArg, err.Error())
	case -2: // -ENOENT
		return fmt.Errorf("%w: %s", types.ErrNotFound, err.Error())
	case -17: // -EEXIST
		return fmt.Errorf("%w: %s", types.ErrAlreadyExists, err.Error())
	default:
		return err
	}
}

// buildCreatePoolCommands maps the dashboard Pool.create request body onto
// the ordered mon command sequence (create → ec_overwrites → app enable →
// quotas → remaining `osd pool set` kwargs), mirroring
// controllers/pool.py:_set_pool_values.
func buildCreatePoolCommands(body map[string]any) ([]map[string]any, error) {
	poolName, ok := body["pool"].(string)
	if !ok || poolName == "" {
		return nil, fmt.Errorf("%w: pool is required", types.ErrInvalidArg)
	}

	pgNum, ok, err := toInt(body["pg_num"])
	if err != nil {
		return nil, fmt.Errorf("%w: pg_num must be an integer", types.ErrInvalidArg)
	}
	if !ok {
		return nil, fmt.Errorf("%w: pg_num is required", types.ErrInvalidArg)
	}

	poolType, ok := body["pool_type"].(string)
	if !ok || poolType == "" {
		return nil, fmt.Errorf("%w: pool_type is required", types.ErrInvalidArg)
	}
	if poolType != "replicated" && poolType != "erasure" {
		return nil, fmt.Errorf("%w: pool_type must be replicated or erasure", types.ErrInvalidArg)
	}

	create := map[string]any{
		"prefix":    "osd pool create",
		"pool":      poolName,
		"pg_num":    pgNum,
		"pgp_num":   pgNum,
		"pool_type": poolType,
		"format":    "json",
	}
	if ecp, ok := body["erasure_code_profile"].(string); ok && ecp != "" {
		create["erasure_code_profile"] = ecp
	}
	if rule, ok := body["rule_name"].(string); ok && rule != "" {
		create["rule"] = rule
	}
	cmds := []map[string]any{create}

	if flags, err := toStringSlice(body["flags"]); err != nil {
		return nil, fmt.Errorf("%w: flags must be an array of strings", types.ErrInvalidArg)
	} else {
		// Dashboard does a single `'ec_overwrites' in flags` membership test
		// (pool.py:209), so emit at most one set command regardless of dupes.
		for _, f := range flags {
			if f == "ec_overwrites" {
				cmds = append(cmds, map[string]any{
					"prefix": "osd pool set",
					"pool":   poolName,
					"var":    "allow_ec_overwrites",
					"val":    "true",
					"format": "json",
				})
				break
			}
		}
	}

	apps, err := toStringSlice(body["application_metadata"])
	if err != nil {
		return nil, fmt.Errorf("%w: application_metadata must be an array of strings", types.ErrInvalidArg)
	}
	for _, app := range apps {
		cmds = append(cmds, map[string]any{
			"prefix":               "osd pool application enable",
			"pool":                 poolName,
			"app":                  app,
			"yes_i_really_mean_it": true,
			"format":               "json",
		})
	}

	// Quotas before generic kwargs, max_objects before max_bytes, matching
	// _set_quotas dict iteration order.
	for _, q := range []struct {
		key   string
		field string
	}{
		{"quota_max_objects", "max_objects"},
		{"quota_max_bytes", "max_bytes"},
	} {
		val, present := body[q.key]
		if !present || val == nil {
			continue
		}
		cmds = append(cmds, map[string]any{
			"prefix": "osd pool set-quota",
			"pool":   poolName,
			"field":  q.field,
			"val":    stringifyVal(val),
			"format": "json",
		})
	}

	// Go map iteration is randomized and structpb.Struct does not preserve the
	// client's JSON key order, so request-order is unrecoverable; sort for a
	// deterministic, reproducible command sequence (the vars are independent).
	kwargKeys := make([]string, 0, len(body))
	for key := range body {
		if _, named := poolNamedKeys[key]; named {
			continue
		}
		kwargKeys = append(kwargKeys, key)
	}
	sort.Strings(kwargKeys)
	for _, key := range kwargKeys {
		cmds = append(cmds, map[string]any{
			"prefix": "osd pool set",
			"pool":   poolName,
			"var":    key,
			"val":    stringifyVal(body[key]),
			"format": "json",
		})
	}

	return cmds, nil
}

// toInt coerces a JSON-decoded numeric (Struct yields float64) into an int.
func toInt(v any) (int, bool, error) {
	switch n := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		return int(n), true, nil
	case int:
		return n, true, nil
	case int64:
		return int(n), true, nil
	default:
		return 0, false, fmt.Errorf("not a number: %T", v)
	}
}

func toStringSlice(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("not an array: %T", v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("not a string: %T", e)
		}
		out = append(out, s)
	}
	return out, nil
}

// stringifyVal mirrors the dashboard's str(value): the val arg of every
// `osd pool set`/`set-quota` is always a string. JSON-decoded numbers come
// back as float64, so integral values must render without a decimal point.
func stringifyVal(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case bool:
		if n {
			return "True"
		}
		return "False"
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
