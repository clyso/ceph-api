package api

import (
	"context"
	"encoding/json"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/user"
	"google.golang.org/protobuf/types/known/emptypb"
)

func NewStatusAPI(radosSvc *rados.Svc) pb.StatusServer {
	return &statusAPI{
		radosSvc: radosSvc,
	}
}

type statusAPI struct {
	radosSvc *rados.Svc
}

func (s *statusAPI) GetCephStatus(ctx context.Context, body *emptypb.Empty) (*pb.GetCephStatusResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopeMonitor, user.PermRead); err != nil {
		return nil, err
	}

	const cmdTempl = `{"prefix": "status", "format": "json"}`
	res, err := s.radosSvc.ExecMon(ctx, cmdTempl)
	if err != nil {
		return nil, err
	}
	var statusDump pb.GetCephStatusResponse
	if err := json.Unmarshal(res, &statusDump); err != nil {
		return nil, err
	}

	return &statusDump, nil
}
