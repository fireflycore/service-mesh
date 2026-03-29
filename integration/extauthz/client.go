package extauthz

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client 是第四版引入的 ext_authz gRPC 客户端。
//
// 它把 service-mesh 的 Invoke 请求转换为 ext_authz CheckRequest，
// 并负责超时控制、连接复用与最小字段映射。
type Client struct {
	target         string
	timeout        time.Duration
	failOpen       bool
	includeHeaders map[string]struct{}

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client authv3.AuthorizationClient
}

// New 创建 ext_authz client。
func New(cfg config.AuthzConfig) (*Client, error) {
	conn, err := grpc.Dial(cfg.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	headers := make(map[string]struct{}, len(cfg.IncludeHeaders))
	for _, header := range cfg.IncludeHeaders {
		headers[header] = struct{}{}
	}

	return &Client{
		target:         cfg.Target,
		timeout:        time.Duration(cfg.TimeoutMS) * time.Millisecond,
		failOpen:       cfg.FailOpen,
		includeHeaders: headers,
		conn:           conn,
		client:         authv3.NewAuthorizationClient(conn),
	}, nil
}

// FailOpen 返回当前 client 的失败策略。
func (c *Client) FailOpen() bool {
	return c.failOpen
}

// Check 执行 ext_authz 检查。
func (c *Client) Check(ctx context.Context, req *invokev1.UnaryInvokeRequest) (*authv3.CheckResponse, error) {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.client.Check(callCtx, buildCheckRequest(req, c.includeHeaders))
}

func buildCheckRequest(req *invokev1.UnaryInvokeRequest, includeHeaders map[string]struct{}) *authv3.CheckRequest {
	headers := map[string]string{
		":authority":                      req.GetTarget().GetService(),
		":path":                           req.GetMethod(),
		":method":                         "POST",
		"content-type":                    "application/grpc",
		"x-service-mesh-target-service":   req.GetTarget().GetService(),
		"x-service-mesh-target-namespace": req.GetTarget().GetNamespace(),
		"x-service-mesh-target-env":       req.GetTarget().GetEnv(),
	}

	for _, entry := range req.GetContext().GetMetadata() {
		if len(entry.GetValues()) == 0 {
			continue
		}
		if len(includeHeaders) > 0 {
			if _, ok := includeHeaders[entry.GetKey()]; !ok {
				continue
			}
		}
		headers[entry.GetKey()] = entry.GetValues()[0]
	}

	return &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Service: req.GetContext().GetCaller().GetService(),
			},
			Destination: &authv3.AttributeContext_Peer{
				Service: req.GetTarget().GetService(),
				Address: buildAddress(req.GetTarget().GetService(), req.GetTarget().GetPort()),
			},
			Request: &authv3.AttributeContext_Request{
				Time: timestamppb.Now(),
				Http: &authv3.AttributeContext_HttpRequest{
					Id:       req.GetContext().GetTraceId(),
					Method:   "POST",
					Host:     req.GetTarget().GetService(),
					Path:     req.GetMethod(),
					Scheme:   "grpc",
					Headers:  headers,
					Protocol: "HTTP/2",
					RawBody:  req.GetPayload(),
					Size:     int64(len(req.GetPayload())),
				},
			},
			ContextExtensions: map[string]string{
				"codec":     req.GetCodec(),
				"namespace": req.GetTarget().GetNamespace(),
				"env":       req.GetTarget().GetEnv(),
				"method":    req.GetMethod(),
			},
		},
	}
}

func buildAddress(host string, port uint32) *corev3.Address {
	return &corev3.Address{
		Address: &corev3.Address_SocketAddress{
			SocketAddress: &corev3.SocketAddress{
				Address: host,
				PortSpecifier: &corev3.SocketAddress_PortValue{
					PortValue: port,
				},
			},
		},
	}
}

// Close 用于测试或进程退出时释放连接。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.client = nil
	if err != nil {
		return fmt.Errorf("close ext_authz client %s failed: %w", c.target, err)
	}
	return nil
}
