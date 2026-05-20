package api

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"

	"google.golang.org/protobuf/types/known/emptypb"
)

func NewCrushRuleAPI(radosSvc *rados.Svc) pb.CrushRuleServer {
	return &crushRuleAPI{
		radosSvc: radosSvc,
	}
}

type crushRuleAPI struct {
	radosSvc *rados.Svc
}

type crushDump struct {
	Rules []*pb.Rule `json:"rules"`
}

func (c *crushRuleAPI) CreateRule(ctx context.Context, req *pb.CreateRuleRequest) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeOsd, user.PermCreate); err != nil {
		return nil, err
	}

	// check if name and failure domain are set
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name is required", types.ErrInvalidArg)
	}
	if req.FailureDomain == "" {
		return nil, fmt.Errorf("%w: failure domain is required", types.ErrInvalidArg)
	}
	// Prepare the command map
	cmdMap := map[string]interface{}{
		"prefix": "osd crush rule create-replicated",
		"name":   req.Name,
		"type":   req.FailureDomain,
		"format": "json",
	}

	// Add optional fields if present
	if req.DeviceClass != nil {
		cmdMap["class"] = *req.DeviceClass
	}

	// Adjust command and fields based on PoolType
	if req.PoolType == pb.PoolType_erasure {
		cmdMap["prefix"] = "osd crush rule create-erasure"
		if req.Profile != nil {
			cmdMap["profile"] = *req.Profile
		}
	} else if req.Root != nil {
		cmdMap["root"] = *req.Root
	}

	cmdBytes, err := json.Marshal(cmdMap)
	if err != nil {
		return nil, err
	}

	_, err = c.radosSvc.ExecMon(ctx, string(cmdBytes))
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (c *crushRuleAPI) DeleteRule(ctx context.Context, req *pb.DeleteRuleRequest) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeOsd, user.PermDelete); err != nil {
		return nil, err
	}

	cmdMap := map[string]interface{}{
		"prefix": "osd crush rule rm",
		"name":   req.Name,
		"format": "json",
	}

	cmdBytes, err := json.Marshal(cmdMap)
	if err != nil {
		return nil, err
	}

	_, err = c.radosSvc.ExecMon(ctx, string(cmdBytes))
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (c *crushRuleAPI) GetRule(ctx context.Context, req *pb.GetRuleRequest) (*pb.Rule, error) {
	if err := user.HasPermissions(ctx, user.ScopeOsd, user.PermRead); err != nil {
		return nil, err
	}
	const cmdTempl = `{"prefix": "osd crush dump", "format": "json"}`
	res, err := c.radosSvc.ExecMon(ctx, cmdTempl)
	if err != nil {
		return nil, err
	}
	var dump crushDump
	if err := json.Unmarshal(res, &dump); err != nil {
		return nil, err
	}

	// Find the rule by name.
	for _, rule := range dump.Rules {
		if rule.RuleName == req.Name {
			return rule, nil
		}
	}

	return nil, types.ErrNotFound
}

func (c *crushRuleAPI) ListRules(ctx context.Context, req *emptypb.Empty) (*pb.ListRulesResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopeOsd, user.PermRead); err != nil {
		return nil, err
	}
	const cmdTempl = `{"prefix": "osd crush dump", "format": "json"}`
	res, err := c.radosSvc.ExecMon(ctx, cmdTempl)
	if err != nil {
		return nil, err
	}
	var dump crushDump
	if err := json.Unmarshal(res, &dump); err != nil {
		return nil, err
	}

	return &pb.ListRulesResponse{Rules: dump.Rules}, nil
}
