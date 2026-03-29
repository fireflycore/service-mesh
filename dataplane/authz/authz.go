package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/integration/extauthz"
	"github.com/fireflycore/service-mesh/pkg/config"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	grpccodes "google.golang.org/grpc/codes"
)

// ErrPermissionDenied 用于让上层能区分“鉴权明确拒绝”和“调用链本身异常”。
var ErrPermissionDenied = errors.New("authz permission denied")

// Authorizer 定义 Invoke 前的统一鉴权接口。
type Authorizer interface {
	Check(ctx context.Context, req *invokev1.UnaryInvokeRequest) error
}

// AllowAll 是最简单的鉴权实现，主要用于测试或早期占位。
type AllowAll struct{}

// NewAllowAll 创建一个永远放行的 authorizer。
func NewAllowAll() *AllowAll {
	return &AllowAll{}
}

// Check 对所有请求都直接放行。
func (a *AllowAll) Check(_ context.Context, _ *invokev1.UnaryInvokeRequest) error {
	return nil
}

// ExtAuthz 是第四版引入的真实鉴权实现。
//
// 它负责：
// - 把 Invoke 请求映射到 Envoy ext_authz CheckRequest
// - 调用外部鉴权服务
// - 按配置处理 fail-open / fail-close
type ExtAuthz struct {
	// client 内部已经封装了 fail-open、header 过滤和 gRPC 连接。
	client *extauthz.Client
}

// NewExtAuthz 创建一个基于配置的 ext_authz authorizer。
func NewExtAuthz(cfg config.AuthzConfig) (*ExtAuthz, error) {
	client, err := extauthz.New(cfg)
	if err != nil {
		return nil, err
	}

	return &ExtAuthz{
		client: client,
	}, nil
}

// Check 执行一次外部鉴权。
func (a *ExtAuthz) Check(ctx context.Context, req *invokev1.UnaryInvokeRequest) error {
	resp, err := a.client.Check(ctx, req)
	if err != nil {
		if a.client.FailOpen() {
			// fail-open 场景下，外部鉴权不可达不会阻断主调用链。
			return nil
		}
		return err
	}

	if resp.GetStatus() == nil {
		if a.client.FailOpen() {
			// 即使响应结构异常，只要 fail-open 打开，也继续放行。
			return nil
		}
		return errors.New("ext_authz returned empty status")
	}

	if resp.GetStatus().GetCode() == int32(grpccodes.OK) {
		// ext_authz 返回 OK 才代表明确授权通过。
		return nil
	}

	return fmt.Errorf("%w: %s", ErrPermissionDenied, statusMessage(resp.GetStatus()))
}

// statusMessage 尽量从 ext_authz 返回状态中提取更可读的错误文本。
func statusMessage(status *rpcstatus.Status) string {
	if status == nil {
		return "empty authz status"
	}
	if strings.TrimSpace(status.GetMessage()) != "" {
		return status.GetMessage()
	}
	return grpccodes.Code(status.GetCode()).String()
}
