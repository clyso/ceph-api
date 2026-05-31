package api

import (
	"bytes"
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

// poolListPool mirrors the raw `osd dump` pool object. It carries the
// dashboard-transformed fields (`type`, `crush_rule`, `application_metadata`)
// in their raw shape plus `is_stretch_pool` (absent from types.OsdDumpPool);
// the rest, including read_balance, is reused verbatim from the embedded type.
type poolListPool struct {
	types.OsdDumpPool
	RawType                int                    `json:"type"`
	RawCrushRule           int32                  `json:"crush_rule"`
	RawApplicationMetadata map[string]interface{} `json:"application_metadata"`
	IsStretchPool          bool                   `json:"is_stretch_pool"`
}

type poolDump struct {
	Pools []poolListPool `json:"pools"`
}

func (p *poolAPI) ListPools(ctx context.Context, req *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
		return nil, err
	}

	// stats=true augments each pool with pg_status + a stats time-series, both
	// sourced from the mgr's in-process counter/pg caches that have no faithful
	// mon-command equivalent (rate/rates history in particular). Reject it
	// rather than silently return the base list as if stats were honored.
	if req.Stats != nil && *req.Stats {
		return nil, fmt.Errorf("%w: stats=true is not supported", types.ErrNotImplemented)
	}
	// attrs is the dashboard's response field-whitelist; a typed proto response
	// always emits every populated field, so attrs cannot be honored and is
	// intentionally ignored (the full pool object is always returned).

	dumpRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	var dump poolDump
	if err := json.Unmarshal(sanitizeNonFiniteFloats(dumpRes), &dump); err != nil {
		return nil, err
	}

	crushRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd crush dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	var crush crushDump
	if err := json.Unmarshal(crushRes, &crush); err != nil {
		return nil, err
	}
	crushRuleNames := make(map[int32]string, len(crush.Rules))
	for _, r := range crush.Rules {
		crushRuleNames[r.RuleId] = r.RuleName
	}

	pools := make([]*pb.PoolListItem, 0, len(dump.Pools))
	for i := range dump.Pools {
		item, err := serializePool(&dump.Pools[i], crushRuleNames)
		if err != nil {
			return nil, err
		}
		pools = append(pools, item)
	}

	return &pb.ListPoolsResponse{Pools: pools}, nil
}

func serializePool(src *poolListPool, crushRuleNames map[int32]string) (*pb.PoolListItem, error) {
	o := &src.OsdDumpPool
	// The dashboard resolves crush_rule against an atomic mgr cache snapshot
	// and raises KeyError (500) on a miss; our two ExecMon calls aren't atomic,
	// so a pool referencing a rule absent from the earlier crush dump is a
	// transient race — surface it rather than emit a wrong empty name.
	crushRuleName, ok := crushRuleNames[src.RawCrushRule]
	if !ok {
		return nil, fmt.Errorf("%w: crush rule id %d not found for pool %q", types.ErrInternal, src.RawCrushRule, o.PoolName)
	}
	item := &pb.PoolListItem{
		Pool:                              o.Pool,
		PoolName:                          o.PoolName,
		CreateTime:                        o.CreateTime.Timestamp,
		Flags:                             o.Flags,
		FlagsNames:                        o.FlagsNames,
		Type:                              poolTypeName(src.RawType),
		Size:                              o.Size,
		MinSize:                           o.MinSize,
		CrushRule:                         crushRuleName,
		PeeringCrushBucketCount:           o.PeeringCrushBucketCount,
		PeeringCrushBucketTarget:          o.PeeringCrushBucketTarget,
		PeeringCrushBucketBarrier:         o.PeeringCrushBucketBarrier,
		PeeringCrushBucketMandatoryMember: o.PeeringCrushBucketMandatoryMember,
		IsStretchPool:                     src.IsStretchPool,
		ObjectHash:                        o.ObjectHash,
		PgAutoscaleMode:                   o.PgAutoscaleMode,
		PgNum:                             o.PgNum,
		PgPlacementNum:                    o.PgPlacementNum,
		PgPlacementNumTarget:              o.PgPlacementNumTarget,
		PgNumTarget:                       o.PgNumTarget,
		PgNumPending:                      o.PgNumPending,
		LastPgMergeMeta:                   o.LastPgMergeMeta,
		LastChange:                        o.LastChange,
		LastForceOpResend:                 o.LastForceOpResend,
		LastForceOpResendPrenautilus:      o.LastForceOpResendPrenautilus,
		LastForceOpResendPreluminous:      o.LastForceOpResendPreluminous,
		Auid:                              o.Auid,
		SnapMode:                          o.SnapMode,
		SnapSeq:                           o.SnapSeq,
		SnapEpoch:                         o.SnapEpoch,
		PoolSnaps:                         o.PoolSnaps,
		RemovedSnaps:                      o.RemovedSnaps,
		QuotaMaxBytes:                     o.QuotaMaxBytes,
		QuotaMaxObjects:                   o.QuotaMaxObjects,
		Tiers:                             o.Tiers,
		TierOf:                            o.TierOf,
		ReadTier:                          o.ReadTier,
		WriteTier:                         o.WriteTier,
		CacheMode:                         o.CacheMode,
		TargetMaxBytes:                    o.TargetMaxBytes,
		TargetMaxObjects:                  o.TargetMaxObjects,
		CacheTargetDirtyRatioMicro:        o.CacheTargetDirtyRatioMicro,
		CacheTargetDirtyHighRatioMicro:    o.CacheTargetDirtyHighRatioMicro,
		CacheTargetFullRatioMicro:         o.CacheTargetFullRatioMicro,
		CacheMinFlushAge:                  o.CacheMinFlushAge,
		CacheMinEvictAge:                  o.CacheMinEvictAge,
		ErasureCodeProfile:                o.ErasureCodeProfile,
		HitSetParams:                      o.HitSetParams,
		HitSetPeriod:                      o.HitSetPeriod,
		HitSetCount:                       o.HitSetCount,
		UseGmtHitset:                      o.UseGmtHitset,
		MinReadRecencyForPromote:          o.MinReadRecencyForPromote,
		MinWriteRecencyForPromote:         o.MinWriteRecencyForPromote,
		HitSetGradeDecayRate:              o.HitSetGradeDecayRate,
		HitSetSearchLastN:                 o.HitSetSearchLastN,
		GradeTable:                        o.GradeTable,
		StripeWidth:                       o.StripeWidth,
		ExpectedNumObjects:                o.ExpectedNumObjects,
		FastRead:                          o.FastRead,
		Options:                           o.Options,
		ApplicationMetadata:               applicationMetadataKeys(src.RawApplicationMetadata),
		ReadBalance:                       o.ReadBalance,
	}
	return item, nil
}

// nonFiniteFloatToken matches the bare tokens Ceph's JSONFormatter streams
// for non-finite doubles (e.g. an infinite read_balance score on a pool with
// no PGs): `: inf`, `: -inf`, `: nan`. These are invalid JSON, so left as-is
// they fail json.Unmarshal for the entire osd dump. We rewrite them to null
// (decoding to a 0 score) rather than crash the whole pool list. The dashboard
// instead stringifies them to "Infinity" because mgr hands it native Python
// floats, never JSON text — an unavoidable edge-case divergence on a degenerate
// cluster, since read_balance scores are typed double here, not string.
var nonFiniteFloatToken = regexp.MustCompile(`:\s*-?(?:inf|nan)\b`)

func sanitizeNonFiniteFloats(b []byte) []byte {
	if !bytes.Contains(b, []byte("inf")) && !bytes.Contains(b, []byte("nan")) {
		return b
	}
	return nonFiniteFloatToken.ReplaceAll(b, []byte(":null"))
}

func poolTypeName(t int) string {
	switch t {
	case 1:
		return "replicated"
	case 3:
		return "erasure"
	default:
		return ""
	}
}

func applicationMetadataKeys(m map[string]interface{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (p *poolAPI) execMon(ctx context.Context, cmd map[string]interface{}) error {
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = p.radosSvc.ExecMon(ctx, string(cmdBytes))
	return err
}
