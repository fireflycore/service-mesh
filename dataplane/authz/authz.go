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

var ErrPermissionDenied = errors.New("authz permission denied")

type Authorizer interface {
	Check(ctx context.Context, req *invokev1.UnaryInvokeRequest) error
}

type AllowAll struct{}

func NewAllowAll() *AllowAll {
	return &AllowAll{}
}

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
			return nil
		}
		return err
	}

	if resp.GetStatus() == nil {
		if a.client.FailOpen() {
			return nil
		}
		return errors.New("ext_authz returned empty status")
	}

	if resp.GetStatus().GetCode() == int32(grpccodes.OK) {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrPermissionDenied, statusMessage(resp.GetStatus()))
}

func statusMessage(status *rpcstatus.Status) string {
	if status == nil {
		return "empty authz status"
	}
	if strings.TrimSpace(status.GetMessage()) != "" {
		return status.GetMessage()
	}
	return grpccodes.Code(status.GetCode()).String()
}
