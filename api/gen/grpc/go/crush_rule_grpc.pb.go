// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             (unknown)
// source: crush_rule.proto

package pb

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	CrushRule_CreateRule_FullMethodName = "/ceph.CrushRule/CreateRule"
	CrushRule_DeleteRule_FullMethodName = "/ceph.CrushRule/DeleteRule"
	CrushRule_GetRule_FullMethodName    = "/ceph.CrushRule/GetRule"
	CrushRule_ListRules_FullMethodName  = "/ceph.CrushRule/ListRules"
)

// CrushRuleClient is the client API for CrushRule service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type CrushRuleClient interface {
	CreateRule(ctx context.Context, in *CreateRuleRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	DeleteRule(ctx context.Context, in *DeleteRuleRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	GetRule(ctx context.Context, in *GetRuleRequest, opts ...grpc.CallOption) (*Rule, error)
	ListRules(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*ListRulesResponse, error)
}

type crushRuleClient struct {
	cc grpc.ClientConnInterface
}

func NewCrushRuleClient(cc grpc.ClientConnInterface) CrushRuleClient {
	return &crushRuleClient{cc}
}

func (c *crushRuleClient) CreateRule(ctx context.Context, in *CreateRuleRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, CrushRule_CreateRule_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *crushRuleClient) DeleteRule(ctx context.Context, in *DeleteRuleRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, CrushRule_DeleteRule_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *crushRuleClient) GetRule(ctx context.Context, in *GetRuleRequest, opts ...grpc.CallOption) (*Rule, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(Rule)
	err := c.cc.Invoke(ctx, CrushRule_GetRule_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *crushRuleClient) ListRules(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*ListRulesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListRulesResponse)
	err := c.cc.Invoke(ctx, CrushRule_ListRules_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CrushRuleServer is the server API for CrushRule service.
// All implementations should embed UnimplementedCrushRuleServer
// for forward compatibility.
type CrushRuleServer interface {
	CreateRule(context.Context, *CreateRuleRequest) (*emptypb.Empty, error)
	DeleteRule(context.Context, *DeleteRuleRequest) (*emptypb.Empty, error)
	GetRule(context.Context, *GetRuleRequest) (*Rule, error)
	ListRules(context.Context, *emptypb.Empty) (*ListRulesResponse, error)
}

// UnimplementedCrushRuleServer should be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedCrushRuleServer struct{}

func (UnimplementedCrushRuleServer) CreateRule(context.Context, *CreateRuleRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CreateRule not implemented")
}
func (UnimplementedCrushRuleServer) DeleteRule(context.Context, *DeleteRuleRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteRule not implemented")
}
func (UnimplementedCrushRuleServer) GetRule(context.Context, *GetRuleRequest) (*Rule, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetRule not implemented")
}
func (UnimplementedCrushRuleServer) ListRules(context.Context, *emptypb.Empty) (*ListRulesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListRules not implemented")
}
func (UnimplementedCrushRuleServer) testEmbeddedByValue() {}

// UnsafeCrushRuleServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CrushRuleServer will
// result in compilation errors.
type UnsafeCrushRuleServer interface {
	mustEmbedUnimplementedCrushRuleServer()
}

func RegisterCrushRuleServer(s grpc.ServiceRegistrar, srv CrushRuleServer) {
	// If the following call pancis, it indicates UnimplementedCrushRuleServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&CrushRule_ServiceDesc, srv)
}

func _CrushRule_CreateRule_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateRuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CrushRuleServer).CreateRule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CrushRule_CreateRule_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CrushRuleServer).CreateRule(ctx, req.(*CreateRuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CrushRule_DeleteRule_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteRuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CrushRuleServer).DeleteRule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CrushRule_DeleteRule_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CrushRuleServer).DeleteRule(ctx, req.(*DeleteRuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CrushRule_GetRule_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetRuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CrushRuleServer).GetRule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CrushRule_GetRule_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CrushRuleServer).GetRule(ctx, req.(*GetRuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CrushRule_ListRules_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CrushRuleServer).ListRules(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CrushRule_ListRules_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CrushRuleServer).ListRules(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

// CrushRule_ServiceDesc is the grpc.ServiceDesc for CrushRule service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var CrushRule_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "ceph.CrushRule",
	HandlerType: (*CrushRuleServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateRule",
			Handler:    _CrushRule_CreateRule_Handler,
		},
		{
			MethodName: "DeleteRule",
			Handler:    _CrushRule_DeleteRule_Handler,
		},
		{
			MethodName: "GetRule",
			Handler:    _CrushRule_GetRule_Handler,
		},
		{
			MethodName: "ListRules",
			Handler:    _CrushRule_ListRules_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "crush_rule.proto",
}
