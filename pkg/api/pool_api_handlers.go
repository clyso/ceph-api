package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

	cmds, err := buildCreatePoolCommands(req)
	if err != nil {
		return nil, err
	}

	// No rollback on partial failure: a failing command leaves a
	// partially-configured pool. This matches the dashboard, which also issues
	// the sequence without rollback.
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

// buildCreatePoolCommands derives the ordered mon command sequence for a pool
// create from the request, mirroring the dashboard's Pool.create orchestration
// (osd pool create → allow_ec_overwrites → application enable → set-quota →
// remaining osd pool set). It is rados-free so it can be table-tested.
func buildCreatePoolCommands(req *pb.CreatePoolRequest) ([]map[string]interface{}, error) {
	if req.Pool == "" {
		return nil, fmt.Errorf("%w: pool is required", types.ErrInvalidArg)
	}
	if req.PgNum == nil {
		return nil, fmt.Errorf("%w: pg_num is required", types.ErrInvalidArg)
	}
	if req.PoolType == "" {
		return nil, fmt.Errorf("%w: pool_type is required", types.ErrInvalidArg)
	}
	if len(req.Configuration) != 0 {
		return nil, fmt.Errorf("%w: configuration field is not supported", types.ErrNotImplemented)
	}
	// rbd_mirroring is deferred entirely (the rbd-mirror peer/mode command path
	// is unported). The dashboard sets the mode whenever rbd_mirroring is not
	// None: 'pool' for true, 'disabled' for false. We reject true, and treat
	// false/absent as a no-op because 'disabled' is the default on a fresh pool
	// so the omitted command has no effect at create time. See §Open decisions.
	if req.RbdMirroring != nil && *req.RbdMirroring {
		return nil, fmt.Errorf("%w: rbd_mirroring field is not supported", types.ErrNotImplemented)
	}

	var cmds []map[string]interface{}

	create := map[string]interface{}{
		"prefix":    "osd pool create",
		"pool":      req.Pool,
		"pg_num":    *req.PgNum,
		"pgp_num":   *req.PgNum,
		"pool_type": req.PoolType,
		"format":    "json",
	}
	if req.ErasureCodeProfile != nil && *req.ErasureCodeProfile != "" {
		create["erasure_code_profile"] = *req.ErasureCodeProfile
	}
	if req.RuleName != nil && *req.RuleName != "" {
		create["rule"] = *req.RuleName
	}
	cmds = append(cmds, create)

	if req.Flags != nil && strings.Contains(*req.Flags, "ec_overwrites") {
		cmds = append(cmds, map[string]interface{}{
			"prefix": "osd pool set",
			"pool":   req.Pool,
			"var":    "allow_ec_overwrites",
			"val":    "true",
			"format": "json",
		})
	}

	for _, app := range req.ApplicationMetadata {
		cmds = append(cmds, map[string]interface{}{
			"prefix":               "osd pool application enable",
			"pool":                 req.Pool,
			"app":                  app,
			"yes_i_really_mean_it": true,
			"format":               "json",
		})
	}

	if v, ok := req.Options["quota_max_objects"]; ok {
		cmds = append(cmds, map[string]interface{}{
			"prefix": "osd pool set-quota",
			"pool":   req.Pool,
			"field":  "max_objects",
			"val":    v,
			"format": "json",
		})
	}
	if v, ok := req.Options["quota_max_bytes"]; ok {
		cmds = append(cmds, map[string]interface{}{
			"prefix": "osd pool set-quota",
			"pool":   req.Pool,
			"field":  "max_bytes",
			"val":    v,
			"format": "json",
		})
	}

	// The dashboard emits these `osd pool set` commands in JSON body key order,
	// but a proto map<string,string> has no defined iteration order, so the
	// original order is unrecoverable here. We sort for a deterministic
	// sequence; this is independent of the dashboard's order (the vars are
	// mutually independent, so order has no functional effect) and is not a
	// parity guarantee.
	keys := make([]string, 0, len(req.Options))
	for k := range req.Options {
		if k == "quota_max_objects" || k == "quota_max_bytes" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := req.Options[k]
		cmds = append(cmds, map[string]interface{}{
			"prefix": "osd pool set",
			"pool":   req.Pool,
			"var":    k,
			"val":    v,
			"format": "json",
		})
		if k == "pg_num" {
			cmds = append(cmds, map[string]interface{}{
				"prefix": "osd pool set",
				"pool":   req.Pool,
				"var":    "pgp_num",
				"val":    v,
				"format": "json",
			})
		}
	}

	return cmds, nil
}
