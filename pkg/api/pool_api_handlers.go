package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"

	"google.golang.org/protobuf/types/known/emptypb"
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
