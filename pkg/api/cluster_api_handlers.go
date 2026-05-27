package api

import (
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

func NewClusterAPI(radosSvc *rados.Svc, configSvc *cephconfig.Config) pb.ClusterServer {
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
	var buf strings.Builder
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
	return &pb.ExportClusterUserResp{Data: buf.String()}, nil
}

func (c *clusterAPI) CreateUser(ctx context.Context, req *pb.CreateClusterUserReq) (*pb.ClusterUserStatusResp, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermCreate); err != nil {
		return nil, err
	}
	if req.ImportData != "" {
		zerolog.Ctx(ctx).Debug().Msg("import user data")
		const monCmd = `{"prefix": "auth import"}`
		_, err := c.radosSvc.ExecMonWithInputBuff(ctx, monCmd, []byte(req.ImportData))
		if err != nil {
			return nil, err
		}
		return &pb.ClusterUserStatusResp{Status: "Successfully imported user"}, nil
	}

	const cmdTempl = `{"prefix": "auth add", "entity": "%s", "caps": [%s]}`
	caps := make([]string, 0, len(req.Capabilities)*2)
	for _, cuc := range req.Capabilities {
		caps = append(caps, strconv.Quote(cuc.Entity), strconv.Quote(cuc.Cap))
	}
	monCmd := fmt.Sprintf(cmdTempl, req.UserEntity, strings.Join(caps, ","))
	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return nil, err
	}
	return &pb.ClusterUserStatusResp{Status: fmt.Sprintf("Successfully created user '%s'", req.UserEntity)}, nil
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
	// Match dashboard: it serializes the cephx key via SecretStr,
	// which renders as 11 asterisks. See dashboard _crud.serialize.
	for _, u := range res.AuthDump {
		u.Key = "***********"
	}
	return &pb.ClusterUsers{Users: res.AuthDump}, nil
}

func (c *clusterAPI) UpdateUser(ctx context.Context, req *pb.UpdateClusterUserReq) (*pb.ClusterUserStatusResp, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermUpdate); err != nil {
		return nil, err
	}
	const cmdTempl = `{"prefix": "auth caps", "entity": "%s", "caps": [%s]}`
	caps := make([]string, 0, len(req.Capabilities)*2)
	for _, cuc := range req.Capabilities {
		caps = append(caps, strconv.Quote(cuc.Entity), strconv.Quote(cuc.Cap))
	}
	monCmd := fmt.Sprintf(cmdTempl, req.UserEntity, strings.Join(caps, ","))
	_, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		if errors.Is(err, types.ErrRadosNotFound) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}
	return &pb.ClusterUserStatusResp{Status: fmt.Sprintf("Successfully edited user '%s'", req.UserEntity)}, nil
}

func (c *clusterAPI) GetStatus(ctx context.Context, _ *emptypb.Empty) (*pb.ClusterStatus, error) {
	if err := user.HasPermissions(ctx, user.ScopeConfigOpt, user.PermRead); err != nil {
		return nil, err
	}
	const monCmd = `{"prefix":"config-key get", "key":"mgr/dashboard/cluster/status"}`
	cmdRes, err := c.radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		if errors.Is(err, types.ErrRadosNotFound) {
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

	// Set defaults for all optional fields
	query := cephconfig.QueryParams{
		Service:  req.Service,
		Level:    req.Level,
		Name:     req.Name,
		FullText: req.FullText,
		Sort:     req.Sort,
		Order:    req.Order,
		Type:     req.Type,
	}

	params := c.configSvc.Search(query)

	respParams := make([]*pb.ConfigParam, len(params))
	for i, param := range params {
		minPtr := cephconfig.ParseMinMax(param.Min)
		maxPtr := cephconfig.ParseMinMax(param.Max)

		servicesEnums := make([]pb.ConfigParam_ServiceType, len(param.Services))
		for i, s := range param.Services {
			servicesEnums[i] = cephconfig.ServiceStringToEnum[s]
		}

		respParams[i] = &pb.ConfigParam{
			Name:               param.Name,
			Type:               pb.ConfigParam_ParamType(pb.ConfigParam_ParamType_value[param.Type]),
			Level:              pb.ConfigParam_ConfigLevel(pb.ConfigParam_ConfigLevel_value[param.Level]),
			Desc:               param.Desc,
			LongDesc:           param.LongDesc,
			DefaultValue:       fmt.Sprint(param.Default),
			DaemonDefault:      fmt.Sprint(param.DaemonDefault),
			Tags:               param.Tags,
			Services:           servicesEnums,
			SeeAlso:            param.SeeAlso,
			EnumValues:         param.EnumValues,
			Min:                minPtr,
			Max:                maxPtr,
			CanUpdateAtRuntime: param.CanUpdateAtRuntime,
			Flags:              param.Flags,
		}
	}

	return &pb.SearchConfigResponse{
		Params: respParams,
	}, nil
}
