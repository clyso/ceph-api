package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
