package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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

func (p *poolAPI) ListPools(ctx context.Context, req *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
		return nil, err
	}

	// The stats path requires the mgr's in-memory PGMap time-series
	// (get_updated_pool_stats), a stateful poller that is out of scope per
	// CLAUDE.md. See the task file §Open decisions.
	if req.Stats != nil && *req.Stats {
		return nil, fmt.Errorf("%w: stats=true is not supported", types.ErrNotImplemented)
	}
	// attrs is ignored: a typed proto always emits the full field set, so the
	// dashboard's per-field whitelist can't be honored. Returning the full
	// object is a harmless superset (the dashboard client only ever drops
	// fields). See the task file §Open decisions.

	crushRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd crush dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	var dump crushDump
	if err := json.Unmarshal(crushRes, &dump); err != nil {
		return nil, err
	}
	crushRules := make(map[int32]string, len(dump.Rules))
	for _, rule := range dump.Rules {
		crushRules[rule.RuleId] = rule.RuleName
	}

	osdRes, err := p.radosSvc.ExecMon(ctx, `{"prefix": "osd dump", "format": "json"}`)
	if err != nil {
		return nil, err
	}
	var osdDump struct {
		Pools []poolListEntry `json:"pools"`
	}
	if err := json.Unmarshal(sanitizeCephFloats(osdRes), &osdDump); err != nil {
		return nil, err
	}

	pools := make([]*pb.PoolInfo, 0, len(osdDump.Pools))
	for i := range osdDump.Pools {
		pools = append(pools, serializePool(&osdDump.Pools[i], crushRules))
	}

	return &pb.ListPoolsResponse{Pools: pools}, nil
}

// poolListEntry parses an osd dump pool while overriding read_balance to a
// free-form Struct: after sanitizeCephFloats its scores may be the string
// "Infinity" and its key set varies by score_type, so the typed
// OsdDumpReadBalance on the embedded struct cannot hold it.
type poolListEntry struct {
	types.OsdDumpPool
	ReadBalance   *structpb.Struct `json:"read_balance,omitempty"`
	IsStretchPool bool             `json:"is_stretch_pool,omitempty"`
}

// cephFloatToken matches a bare inf/-inf/nan token in JSON value position
// (after `:` or `[` or `,`). Ceph's JSON formatter streams non-finite doubles
// as these unquoted tokens, which encoding/json rejects.
var cephFloatToken = regexp.MustCompile(`([:\[,]\s*)(-?inf|nan)(\s*[,}\]])`)

// sanitizeCephFloats rewrites Ceph's bare inf/-inf/nan float tokens into
// quoted strings so encoding/json accepts them.
//
// In practice only the read_balance scores in osd dump carry non-finite
// floats (see _serialize_pool, pool.py); this rewrite is over the whole blob
// for simplicity, but no other osd-dump float is expected to be non-finite.
//
// The dashboard replaces any read_balance score for which math.isinf is true
// with the literal "Infinity" — math.isinf is sign-agnostic, so both +inf and
// -inf map to "Infinity" there; we mirror that. nan is not handled by the
// dashboard (math.isinf(nan) is False, so it leaks through as a bare NaN
// token); we quote it as "NaN" purely so encoding/json accepts the unmarshal.
func sanitizeCephFloats(data []byte) []byte {
	// Two passes: a token's trailing delimiter is also the next token's leading
	// delimiter (e.g. `[inf, inf]`), and a single regex pass consumes it.
	repl := func(b []byte) []byte {
		return cephFloatToken.ReplaceAllFunc(b, func(m []byte) []byte {
			sub := cephFloatToken.FindSubmatch(m)
			str := "Infinity"
			if string(sub[2]) == "nan" {
				str = "NaN"
			}
			return []byte(string(sub[1]) + `"` + str + `"` + string(sub[3]))
		})
	}
	return repl(repl(data))
}

// serializePool mirrors the dashboard's _serialize_pool shaping: type
// int->string, crush_rule id->name, application_metadata dict->key-list. The
// read_balance inf->"Infinity" replacement is handled upstream in
// sanitizeCephFloats. Remaining fields are copied verbatim from the osd dump
// pool entry.
func serializePool(pool *poolListEntry, crushRules map[int32]string) *pb.PoolInfo {
	return &pb.PoolInfo{
		Pool:       pool.Pool,
		PoolName:   pool.PoolName,
		CreateTime: pool.CreateTime.Timestamp,
		Flags:      pool.Flags,
		FlagsNames: pool.FlagsNames,
		Type:       poolTypeName(pool.Type),
		Size:       pool.Size,
		MinSize:    pool.MinSize,
		// A crush_rule id absent from the rule table yields "" rather than
		// erroring, unlike the dashboard's crush_rules[id] KeyError->500
		// (pool.py:113). Better than crashing, but silent; same divergence as
		// poolTypeName's default->"".
		CrushRule:                         crushRules[pool.CrushRule],
		PeeringCrushBucketCount:           pool.PeeringCrushBucketCount,
		PeeringCrushBucketTarget:          pool.PeeringCrushBucketTarget,
		PeeringCrushBucketBarrier:         pool.PeeringCrushBucketBarrier,
		PeeringCrushBucketMandatoryMember: pool.PeeringCrushBucketMandatoryMember,
		ObjectHash:                        pool.ObjectHash,
		PgAutoscaleMode:                   pool.PgAutoscaleMode,
		PgNum:                             pool.PgNum,
		PgPlacementNum:                    pool.PgPlacementNum,
		PgPlacementNumTarget:              pool.PgPlacementNumTarget,
		PgNumTarget:                       pool.PgNumTarget,
		PgNumPending:                      pool.PgNumPending,
		LastPgMergeMeta:                   pool.LastPgMergeMeta,
		LastChange:                        pool.LastChange,
		LastForceOpResend:                 pool.LastForceOpResend,
		LastForceOpResendPrenautilus:      pool.LastForceOpResendPrenautilus,
		LastForceOpResendPreluminous:      pool.LastForceOpResendPreluminous,
		Auid:                              pool.Auid,
		SnapMode:                          pool.SnapMode,
		SnapSeq:                           pool.SnapSeq,
		SnapEpoch:                         pool.SnapEpoch,
		PoolSnaps:                         pool.PoolSnaps,
		RemovedSnaps:                      pool.RemovedSnaps,
		QuotaMaxBytes:                     pool.QuotaMaxBytes,
		QuotaMaxObjects:                   pool.QuotaMaxObjects,
		Tiers:                             pool.Tiers,
		TierOf:                            pool.TierOf,
		ReadTier:                          pool.ReadTier,
		WriteTier:                         pool.WriteTier,
		CacheMode:                         pool.CacheMode,
		TargetMaxBytes:                    pool.TargetMaxBytes,
		TargetMaxObjects:                  pool.TargetMaxObjects,
		CacheTargetDirtyRatioMicro:        pool.CacheTargetDirtyRatioMicro,
		CacheTargetDirtyHighRatioMicro:    pool.CacheTargetDirtyHighRatioMicro,
		CacheTargetFullRatioMicro:         pool.CacheTargetFullRatioMicro,
		CacheMinFlushAge:                  pool.CacheMinFlushAge,
		CacheMinEvictAge:                  pool.CacheMinEvictAge,
		ErasureCodeProfile:                pool.ErasureCodeProfile,
		HitSetParams:                      pool.HitSetParams,
		HitSetPeriod:                      pool.HitSetPeriod,
		HitSetCount:                       pool.HitSetCount,
		UseGmtHitset:                      pool.UseGmtHitset,
		MinReadRecencyForPromote:          pool.MinReadRecencyForPromote,
		MinWriteRecencyForPromote:         pool.MinWriteRecencyForPromote,
		HitSetGradeDecayRate:              pool.HitSetGradeDecayRate,
		HitSetSearchLastN:                 pool.HitSetSearchLastN,
		GradeTable:                        pool.GradeTable,
		StripeWidth:                       pool.StripeWidth,
		ExpectedNumObjects:                pool.ExpectedNumObjects,
		FastRead:                          pool.FastRead,
		Options:                           pool.Options,
		ApplicationMetadata:               structKeys(pool.ApplicationMetadata),
		ReadBalance:                       pool.ReadBalance,
		IsStretchPool:                     pool.IsStretchPool,
	}
}

// poolTypeName maps the osd dump numeric pool type to the dashboard string.
// 1->replicated, 3->erasure (pool.py _serialize_pool). An unknown value yields
// the empty string rather than panicking, unlike the dashboard's dict lookup.
func poolTypeName(t int32) string {
	switch t {
	case 1:
		return "replicated"
	case 3:
		return "erasure"
	default:
		return ""
	}
}

// structKeys returns the sorted key list of a struct, mirroring the
// dashboard's application_metadata dict->list reduction. The dashboard emits
// JSON-insertion order; a Go map has none, so we sort for determinism (pools
// almost always carry a single application, so order rarely shows).
func structKeys(s *structpb.Struct) []string {
	if s == nil {
		return nil
	}
	keys := make([]string, 0, len(s.Fields))
	for k := range s.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
