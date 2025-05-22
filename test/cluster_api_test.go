package test

import (
	"context"
	"strings"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_ClusterStatus(t *testing.T) {
	r := require.New(t)
	client := pb.NewClusterClient(admConn)

	res, err := client.GetStatus(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	initStatus := res.Status
	newStatus := pb.ClusterStatus_INSTALLED
	if initStatus == newStatus {
		newStatus = pb.ClusterStatus_POST_INSTALLED
	}

	_, err = client.UpdateStatus(tstCtx, &pb.ClusterStatus{Status: newStatus})
	r.NoError(err)
	t.Cleanup(func() {
		client.UpdateStatus(tstCtx, &pb.ClusterStatus{Status: initStatus})
	})

	res, err = client.GetStatus(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.EqualValues(newStatus, res.Status)
}

func Test_ClusterUsers(t *testing.T) {
	r := require.New(t)
	client := pb.NewClusterClient(admConn)
	const (
		user = "client.test"
	)

	users, err := client.GetUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err, "get all users")

	_, err = client.CreateUser(tstCtx, &pb.CreateClusterUserReq{
		Capabilities: map[string]string{"mon": "allow r"},
		UserEntity:   user,
	})
	r.NoError(err, "create a new test user %s", user)
	t.Cleanup(func() {
		// delete test user on exit
		client.DeleteUser(context.Background(), &pb.DeleteClusterUserReq{UserEntity: user})
	})

	users2, err := client.GetUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err, "get all users including a new one")
	r.EqualValues(len(users.Users)+1, len(users2.Users), "users number increased")
	var created *pb.ClusterUser = nil
	for i, v := range users2.Users {
		if v.Entity == user {
			created = users2.Users[i]
			break
		}
	}
	r.NotNil(created, "new user created")
	r.Len(created.Caps, 1, "new user has correct capabilities")
	r.EqualValues(created.Caps["mon"], "allow r", "new user has correct capabilities")

	exp, err := client.ExportUser(tstCtx, &pb.ExportClusterUserReq{Entities: []string{user}})
	r.NoError(err, "new user can be exported")
	r.Contains(string(exp.Data), `mon = "allow r"`, "new user export conains correct caps")

	_, err = client.UpdateUser(tstCtx, &pb.UpdateClusterUserReq{
		UserEntity:   user,
		Capabilities: map[string]string{"mon": "allow w"}})
	r.NoError(err, "new user caps updated")

	exp, err = client.ExportUser(tstCtx, &pb.ExportClusterUserReq{Entities: []string{user}})
	r.NoError(err)
	r.Contains(string(exp.Data), `mon = "allow w"`, "export contains updated caps")
	r.NotContains(string(exp.Data), `mon = "allow r"`, "export does not contains old caps")

	users2, err = client.GetUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.NotEmpty(users2.Users)
	r.EqualValues(len(users.Users)+1, len(users2.Users))
	created = nil
	for i, v := range users2.Users {
		if v.Entity == user {
			created = users2.Users[i]
			break
		}
	}
	r.NotNil(created, "list user returns updated caps")
	r.Len(created.Caps, 1, "list user returns updated caps")
	r.EqualValues(created.Caps["mon"], "allow w", "list user returns updated caps")

	_, err = client.DeleteUser(tstCtx, &pb.DeleteClusterUserReq{
		UserEntity: user})
	r.NoError(err, "delete new user")

	users2, err = client.GetUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.EqualValues(len(users.Users), len(users2.Users), "user was removed from list")
	created = nil
	for i, v := range users2.Users {
		if v.Entity == user {
			created = users2.Users[i]
			break
		}
	}
	r.Nil(created, "user was removed from list")

	_, err = client.CreateUser(tstCtx, &pb.CreateClusterUserReq{ImportData: exp.Data})
	r.NoError(err, "user was imported back from export data")

	users2, err = client.GetUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.EqualValues(len(users.Users)+1, len(users2.Users), "user is back after import")
	created = nil
	for i, v := range users2.Users {
		if v.Entity == user {
			created = users2.Users[i]
			break
		}
	}
	r.NotNil(created, "user is back after import")
}

func Test_SearchConfig(t *testing.T) {
	r := require.New(t)
	client := pb.NewClusterClient(admConn)

	// Test 1: Basic query with no filters
	resp, err := client.SearchConfig(tstCtx, &pb.SearchConfigRequest{})
	r.NoError(err, "should not error with empty search request")
	r.NotNil(resp, "response should not be nil")
	r.Greater(len(resp.Params), 0, "should return at least some parameters")

	// Test 2: Filter by service type
	serviceMon := pb.SearchConfigRequest_MON
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Service: &serviceMon,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 0 {
		// Verify that all returned params have the MON service
		for _, param := range resp.Params {
			hasMonService := false
			for _, svc := range param.Services {
				if svc == pb.SearchConfigRequest_MON {
					hasMonService = true
					break
				}
			}
			r.True(hasMonService, "parameter '%s' should have 'mon' service", param.Name)
		}
	}

	// Test 3: Filter by name with wildcard
	// Find a common parameter prefix first to ensure we get results
	commonParamPrefix := "mon_"
	namePattern := commonParamPrefix + "*"
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Name: &namePattern,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 0 {
		// Verify that all returned params start with the prefix
		for _, param := range resp.Params {
			r.True(strings.HasPrefix(param.Name, commonParamPrefix),
				"parameter '%s' should start with '%s'", param.Name, commonParamPrefix)
		}
	}

	// Test 4: Filter by level
	levelBasic := pb.SearchConfigRequest_BASIC
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Level: &levelBasic,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 0 {
		// Verify that all returned params have the BASIC level
		for _, param := range resp.Params {
			r.Equal(pb.SearchConfigRequest_BASIC, param.Level, "parameter '%s' should have 'basic' level", param.Name)
		}
	}

	// Test 5: Full text search for a very common term
	searchTerm := "mon"
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		FullText: &searchTerm,
	})
	r.NoError(err)
	r.NotNil(resp)
	r.Greater(len(resp.Params), 0, "should return at least some parameters for common term")

	// Check first few responses for the search term
	foundMatch := false
	checkCount := min(5, len(resp.Params))
	for i := 0; i < checkCount; i++ {
		param := resp.Params[i]
		if strings.Contains(strings.ToLower(param.Name), searchTerm) ||
			strings.Contains(strings.ToLower(param.Desc), searchTerm) ||
			strings.Contains(strings.ToLower(param.LongDesc), searchTerm) {
			foundMatch = true
			break
		}
	}
	r.True(foundMatch, "should find at least one parameter matching the search term in first %d results", checkCount)

	// Test 6: Sorting by name ascending (default)
	sortName := pb.SearchConfigRequest_NAME
	sortAsc := pb.SearchConfigRequest_ASC
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Sort:  &sortName,
		Order: &sortAsc,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 1 {
		// Verify that parameters are sorted by name in ascending order
		for i := 0; i < len(resp.Params)-1; i++ {
			r.LessOrEqual(resp.Params[i].Name, resp.Params[i+1].Name,
				"parameters should be sorted by name in ascending order")
		}
	}

	// Test 7: Sorting by name descending
	sortDesc := pb.SearchConfigRequest_DESC
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Sort:  &sortName,
		Order: &sortDesc,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 1 {
		// Verify that parameters are sorted by name in descending order
		for i := 0; i < len(resp.Params)-1; i++ {
			r.GreaterOrEqual(resp.Params[i].Name, resp.Params[i+1].Name,
				"parameters should be sorted by name in descending order")
		}
	}

	// Test 8: Combined filters - service and level
	serviceOsd := pb.SearchConfigRequest_OSD
	levelAdvanced := pb.SearchConfigRequest_ADVANCED
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Service: &serviceOsd,
		Level:   &levelAdvanced,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 0 {
		// Verify that all returned params have the OSD service and ADVANCED level
		for _, param := range resp.Params {
			found := false
			for _, svc := range param.Services {
				if svc == pb.SearchConfigRequest_OSD {
					found = true
					break
				}
			}
			r.True(found, "parameter '%s' should have 'osd' service", param.Name)
			r.Equal(pb.SearchConfigRequest_ADVANCED, param.Level, "parameter '%s' should have 'advanced' level", param.Name)
		}
	}

	// Test 9: Check that parameter fields are properly populated
	// This test checks that the conversion from internal config param to protobuf message works
	paramName := "mon_max_pg_per_osd"
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		// Checking with a very common parameter name
		Name: &paramName,
	})
	r.NoError(err)
	r.NotNil(resp)
	if len(resp.Params) > 0 {
		param := resp.Params[0]
		r.NotEmpty(param.Name, "name should not be empty")
		r.NotEqual(pb.SearchConfigRequest_STR, param.Type, "type should not be default/empty")
		r.NotEqual(pb.SearchConfigRequest_BASIC, param.Level, "level should not be default/empty")
	}

	// Test 10: Test with invalid service type
	// The handler should not return an error but rather return no results
	serviceCommon := pb.SearchConfigRequest_COMMON
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Service: &serviceCommon,
	})
	r.NoError(err)
	r.NotNil(resp)

	// Test 11: Test combining name wildcard with service filter
	namePattern = "osd_*"
	resp, err = client.SearchConfig(tstCtx, &pb.SearchConfigRequest{
		Name:    &namePattern,
		Service: &serviceOsd,
	})
	r.NoError(err)
	r.NotNil(resp)

	if len(resp.Params) > 0 {
		for _, param := range resp.Params {
			// Verify name starts with "osd_"
			r.True(strings.HasPrefix(param.Name, "osd_"),
				"parameter '%s' should start with 'osd_'", param.Name)

			// Verify it has OSD service
			found := false
			for _, svc := range param.Services {
				if svc == pb.SearchConfigRequest_OSD {
					found = true
					break
				}
			}
			r.True(found, "parameter '%s' should have 'osd' service", param.Name)
		}
	}
}
