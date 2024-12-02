package test

import (
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_List_Ceph_Status(t *testing.T) {
	r := require.New(t)
	client := pb.NewStatusClient(admConn)
	res, err := client.GetCephStatus(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.NotEmpty(res)
	r.NotEmpty(res.Fsid)
	r.NotEmpty(res.QuorumAge)
	r.NotEmpty(res.Health)
	r.NotEmpty(res.Osdmap)
	r.NotEmpty(res.Mgrmap)
	r.NotEmpty(res.Monmap)
}
