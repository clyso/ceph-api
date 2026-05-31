//go:build cgo

package test

import (
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_ListRlues(t *testing.T) {
	r := require.New(t)
	client := pb.NewCrushRuleClient(admConn)
	res, err := client.ListRules(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	rules := res.Rules
	r.NotEmpty(rules)
}

func Test_Create_Get_And_Delete(t *testing.T) {
	r := require.New(t)
	client := pb.NewCrushRuleClient(admConn)

	_, err := client.CreateRule(tstCtx, &pb.CreateRuleRequest{
		Name:          "some_rule",
		Root:          proto.String("default"),
		FailureDomain: "host",
	})
	r.NoError(err)

	res, err := client.GetRule(tstCtx, &pb.GetRuleRequest{
		Name: "some_rule",
	})
	r.NoError(err)
	r.Equal("some_rule", res.RuleName)

	listRes, err := client.ListRules(tstCtx, &emptypb.Empty{})

	r.NoError(err)
	rules := listRes.Rules
	r.NotEmpty(rules)
	// Check if "some_rule" is in the list of rules
	// Extract rule names
	var ruleNames []string
	for _, rule := range rules {
		ruleNames = append(ruleNames, rule.RuleName)
	}

	// Check if "some_rule" is in the list
	r.Contains(ruleNames, "some_rule", "The rule 'some_rule' should be present in the list of rules")

	_, err = client.DeleteRule(tstCtx, &pb.DeleteRuleRequest{
		Name: "some_rule",
	})
	r.NoError(err)

	_, err = client.GetRule(tstCtx, &pb.GetRuleRequest{Name: "some_rule"})
	r.Error(err)
	// expecting a not found error
	r.Contains(err.Error(), "NotFound")
}

func Test_GetNonExistingRule(t *testing.T) {
	r := require.New(t)
	client := pb.NewCrushRuleClient(admConn)

	// Attempt to get a rule with a name that we know does not exist
	_, err := client.GetRule(tstCtx, &pb.GetRuleRequest{
		Name: "non_existing_rule",
	})
	r.Error(err)
	// expecting a not found error
	r.Contains(err.Error(), "NotFound")
}

func Test_CreateRuleWithoutRequiredFields(t *testing.T) {
	r := require.New(t)
	client := pb.NewCrushRuleClient(admConn)

	// Attempt to create a rule without setting the Name field
	_, err := client.CreateRule(tstCtx, &pb.CreateRuleRequest{
		// Name is missing
		Root:          proto.String("default"),
		FailureDomain: "host",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")
}
