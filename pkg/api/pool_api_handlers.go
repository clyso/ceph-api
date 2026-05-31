package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

	if req.Pool == "" {
		return nil, fmt.Errorf("%w: pool name is required", types.ErrInvalidArg)
	}
	if req.PgNum <= 0 {
		return nil, fmt.Errorf("%w: pg_num is required and must be positive", types.ErrInvalidArg)
	}
	if req.PoolType != "replicated" && req.PoolType != "erasure" {
		return nil, fmt.Errorf("%w: pool_type must be \"replicated\" or \"erasure\"", types.ErrInvalidArg)
	}

	createCmd := map[string]interface{}{
		"prefix":    "osd pool create",
		"format":    "json",
		"pool":      req.Pool,
		"pg_num":    req.PgNum,
		"pgp_num":   req.PgNum,
		"pool_type": req.PoolType,
	}
	// The dashboard's CephService.send_command drops only None kwargs
	// (ceph_service.py), so it omits "rule" when rule_name is unset but
	// forwards an empty string verbatim (mon accepts "" as the default rule).
	if req.RuleName != nil {
		createCmd["rule"] = *req.RuleName
	}
	if req.ErasureCodeProfile != nil && *req.ErasureCodeProfile != "" {
		createCmd["erasure_code_profile"] = *req.ErasureCodeProfile
	}
	if err := p.execMon(ctx, createCmd); err != nil {
		return nil, err
	}

	if req.Flags != nil && strings.Contains(*req.Flags, "ec_overwrites") {
		if err := p.execMon(ctx, map[string]interface{}{
			"prefix": "osd pool set",
			"format": "json",
			"pool":   req.Pool,
			"var":    "allow_ec_overwrites",
			"val":    "true",
		}); err != nil {
			return nil, err
		}
	}

	for _, app := range req.ApplicationMetadata {
		if err := p.execMon(ctx, map[string]interface{}{
			"prefix":               "osd pool application enable",
			"format":               "json",
			"pool":                 req.Pool,
			"app":                  app,
			"yes_i_really_mean_it": true,
		}); err != nil {
			return nil, err
		}
	}

	if req.QuotaMaxObjects != nil {
		if err := p.setQuota(ctx, req.Pool, "max_objects", *req.QuotaMaxObjects); err != nil {
			return nil, err
		}
	}
	if req.QuotaMaxBytes != nil {
		if err := p.setQuota(ctx, req.Pool, "max_bytes", *req.QuotaMaxBytes); err != nil {
			return nil, err
		}
	}

	if req.PgAutoscaleMode != nil {
		if err := p.setPoolVar(ctx, req.Pool, "pg_autoscale_mode", *req.PgAutoscaleMode); err != nil {
			return nil, err
		}
	}
	if req.CompressionMode != nil {
		if err := p.setPoolVar(ctx, req.Pool, "compression_mode", *req.CompressionMode); err != nil {
			return nil, err
		}
	}

	return &emptypb.Empty{}, nil
}

func (p *poolAPI) setQuota(ctx context.Context, pool, field string, val int64) error {
	return p.execMon(ctx, map[string]interface{}{
		"prefix": "osd pool set-quota",
		"format": "json",
		"pool":   pool,
		"field":  field,
		"val":    strconv.FormatInt(val, 10),
	})
}

func (p *poolAPI) setPoolVar(ctx context.Context, pool, varName, val string) error {
	return p.execMon(ctx, map[string]interface{}{
		"prefix": "osd pool set",
		"format": "json",
		"pool":   pool,
		"var":    varName,
		"val":    val,
	})
}

func (p *poolAPI) execMon(ctx context.Context, cmd map[string]interface{}) error {
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = p.radosSvc.ExecMon(ctx, string(cmdBytes))
	return err
}
