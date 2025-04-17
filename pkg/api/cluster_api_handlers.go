package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/cephconfig"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

func NewClusterAPI(radosSvc *rados.Svc) pb.ClusterServer {
	configSvc, err := cephconfig.NewConfig()
	if err != nil {
		zerolog.Ctx(context.Background()).Err(err).Msg("failed to create config service")
	}
	return &clusterAPI{
		radosSvc:  radosSvc,
		configSvc: configSvc,
	}
}

type clusterAPI struct {
	radosSvc  *rados.Svc
	configSvc *cephconfig.Config
}

func (c *clusterAPI) DeleteUser(ctx context.Context, req *pb.DeleteClusterUserReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermDelete); err != nil {
		return nil, err
	}
	const monCmdTeml = `{"prefix": "auth del", "entity": "%s"}`
	monCmd := fmt.Sprintf(monCmdTeml, req.UserEntity)
	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (c *clusterAPI) ExportUser(ctx context.Context, req *pb.ExportClusterUserReq) (*pb.ExportClusterUserResp, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermRead); err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	for _, entity := range req.Entities {
		const monCmdTeml = `{"prefix": "auth export", "entity": "%s"}`
		monCmd := fmt.Sprintf(monCmdTeml, entity)
		res, err := c.radosSvc.ExecMon(ctx, monCmd)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Str("mon_cmd", monCmd).Msg("unable to export user")
			continue
		}
		buf.Write(res)
		buf.WriteRune('\n')
	}
	return &pb.ExportClusterUserResp{Data: buf.Bytes()}, nil
}

func (c *clusterAPI) CreateUser(ctx context.Context, req *pb.CreateClusterUserReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermCreate); err != nil {
		return nil, err
	}
	if len(req.ImportData) != 0 {
		zerolog.Ctx(ctx).Debug().Msg("import user data")
		const monCmd = `{"prefix": "auth import"}`
		_, err := c.radosSvc.ExecMonWithInputBuff(ctx, monCmd, req.ImportData)
		if err != nil {
			return nil, err
		}
		return &emptypb.Empty{}, nil
	}

	const cmdTempl = `{"prefix": "auth add", "entity": "%s", "caps": [%s]}`
	caps := make([]string, 0, len(req.Capabilities)*2)
	for k, v := range req.Capabilities {
		caps = append(caps, strconv.Quote(k), strconv.Quote(v))
	}
	monCmd := fmt.Sprintf(cmdTempl, req.UserEntity, strings.Join(caps, ","))
	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// GetUsers implements pb.ClusterServer.
func (c *clusterAPI) GetUsers(ctx context.Context, _ *emptypb.Empty) (*pb.ClusterUsers, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermRead); err != nil {
		return nil, err
	}
	const monCmd = `{"prefix": "auth ls", "format": "json"}`

	cmdRes, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return nil, err
	}
	var res struct {
		AuthDump []*pb.ClusterUser `json:"auth_dump"`
	}

	err = json.Unmarshal(cmdRes, &res)
	if err != nil {
		return nil, err
	}
	return &pb.ClusterUsers{Users: res.AuthDump}, nil
}

func (c *clusterAPI) UpdateUser(ctx context.Context, req *pb.UpdateClusterUserReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermUpdate); err != nil {
		return nil, err
	}
	const cmdTempl = `{"prefix": "auth caps", "entity": "%s", "caps": [%s]}`
	caps := make([]string, 0, len(req.Capabilities)*2)
	for k, v := range req.Capabilities {
		caps = append(caps, strconv.Quote(k), strconv.Quote(v))
	}
	monCmd := fmt.Sprintf(cmdTempl, req.UserEntity, strings.Join(caps, ","))
	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		if errors.Is(err, types.RadosErrorNotFound) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (c *clusterAPI) GetStatus(ctx context.Context, _ *emptypb.Empty) (*pb.ClusterStatus, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermRead); err != nil {
		return nil, err
	}
	const monCmd = `{"prefix":"config-key get", "key":"mgr/dashboard/cluster/status"}`
	cmdRes, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		if errors.Is(err, types.RadosErrorNotFound) {
			// If the status is not set, assume it is already fully functional.
			return &pb.ClusterStatus{Status: pb.ClusterStatus_POST_INSTALLED}, nil
		}
		return nil, err
	}

	status := pb.ClusterStatus_Status(pb.ClusterStatus_Status_value[string(cmdRes)])
	return &pb.ClusterStatus{Status: status}, nil
}

func (c *clusterAPI) UpdateStatus(ctx context.Context, req *pb.ClusterStatus) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermUpdate); err != nil {
		return nil, err
	}
	monCmd := fmt.Sprintf(
		`{"prefix":"config-key set", "key":"mgr/dashboard/cluster/status", "val":"%s"}`,
		req.Status.String())

	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (c *clusterAPI) SearchConfig(ctx context.Context, req *pb.SearchConfigRequest) (*pb.SearchConfigResponse, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermRead); err != nil {
		return nil, err
	}

	if c.configSvc == nil {
		return nil, errors.New("config service is not initialized")
	}

	query := cephconfig.QueryParams{
		Name:     req.Name,
		FullText: req.FullText,
	}

	switch req.Service {
	case pb.SearchConfigRequest_SERVICE_MON:
		query.Service = cephconfig.ServiceMon
	case pb.SearchConfigRequest_SERVICE_OSD:
		query.Service = cephconfig.ServiceOSD
	case pb.SearchConfigRequest_SERVICE_MDS:
		query.Service = cephconfig.ServiceMDS
	case pb.SearchConfigRequest_SERVICE_RGW:
		query.Service = cephconfig.ServiceRGW
	case pb.SearchConfigRequest_SERVICE_MGR:
		query.Service = cephconfig.ServiceMgr
	case pb.SearchConfigRequest_SERVICE_COMMON:
		query.Service = cephconfig.ServiceCommon
	case pb.SearchConfigRequest_SERVICE_CLIENT:
		query.Service = cephconfig.ServiceClient
	}

	switch req.Level {
	case pb.SearchConfigRequest_LEVEL_BASIC:
		query.Level = cephconfig.LevelBasic
	case pb.SearchConfigRequest_LEVEL_ADVANCED:
		query.Level = cephconfig.LevelAdvanced
	case pb.SearchConfigRequest_LEVEL_DEVELOPER:
		query.Level = cephconfig.LevelDeveloper
	case pb.SearchConfigRequest_LEVEL_EXPERIMENTAL:
		query.Level = cephconfig.LevelExperimental
	}

	switch req.Sort {
	case pb.SearchConfigRequest_SORT_NAME:
		query.Sort = cephconfig.SortFieldName
	case pb.SearchConfigRequest_SORT_TYPE:
		query.Sort = cephconfig.SortFieldType
	case pb.SearchConfigRequest_SORT_SERVICE:
		query.Sort = cephconfig.SortFieldService
	case pb.SearchConfigRequest_SORT_LEVEL:
		query.Sort = cephconfig.SortFieldLevel
	default:
		query.Sort = cephconfig.SortFieldName
	}

	switch req.Order {
	case pb.SearchConfigRequest_SORT_ASC:
		query.Order = cephconfig.SortOrderAsc
	case pb.SearchConfigRequest_SORT_DESC:
		query.Order = cephconfig.SortOrderDesc
	default:
		query.Order = cephconfig.SortOrderAsc
	}

	params := c.configSvc.Search(query)

	respParams := make([]*pb.ConfigParam, len(params))
	for i, param := range params {
		respParams[i] = &pb.ConfigParam{
			Name:               param.Name,
			Type:               param.Type,
			Level:              string(param.Level),
			Desc:               param.Desc,
			LongDesc:           param.LongDesc,
			DefaultValue:       param.Default,
			DaemonDefault:      param.DaemonDefault,
			Tags:               param.Tags,
			Services:           param.Services,
			SeeAlso:            param.SeeAlso,
			EnumValues:         param.EnumValues,
			Min:                param.Min,
			Max:                param.Max,
			CanUpdateAtRuntime: param.CanUpdateAtRuntime,
			Flags:              param.Flags,
		}
	}

	return &pb.SearchConfigResponse{
		Params: respParams,
	}, nil
}
