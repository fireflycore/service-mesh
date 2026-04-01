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
	"github.com/fireflycore/service-mesh/pkg/originalidentity"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client 是第四版引入的 ext_authz gRPC 客户端。
//
// 它把 service-mesh 的 Invoke 请求转换为 ext_authz CheckRequest，
// 并负责超时控制、连接复用与最小字段映射。
type Client struct {
	// 这些字段都是从配置展开后的运行时常量。
	target         string
	timeout        time.Duration
	failOpen       bool
	includeHeaders map[string]struct{}

	// conn/client 在当前实现里长期复用，减少频繁建链开销。
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client authv3.AuthorizationClient
}

// New 创建 ext_authz client。
func New(cfg config.AuthzConfig) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout(cfg.TimeoutMS))
	defer cancel()

	// ext_authz 目前固定使用 gRPC 明文连接；后续若需要可再扩展 TLS。
	conn, err := grpc.DialContext(
		dialCtx,
		cfg.Target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]struct{}, len(cfg.IncludeHeaders))
	for _, header := range cfg.IncludeHeaders {
		// includeHeaders 转成 set 后，后面判断是否透传会更高效。
		headers[header] = struct{}{}
	}

	return &Client{
		target: cfg.Target,
		// timeout/failOpen/includeHeaders 都在 New 阶段固化，后续调用只读使用。
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
		// 没给 timeout 时兜底 500ms，避免 authz 调用无限挂住。
		timeout = 500 * time.Millisecond
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.client.Check(callCtx, buildCheckRequest(req, c.includeHeaders))
}

// buildCheckRequest 把内部 Invoke 请求映射为 Envoy ext_authz 请求。
func buildCheckRequest(req *invokev1.UnaryInvokeRequest, includeHeaders map[string]struct{}) *authv3.CheckRequest {
	// 先构造一组与 gRPC 请求强相关的基础 header。
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
			// 没值的 metadata 对 ext_authz 没有意义，直接跳过。
			continue
		}
		if len(includeHeaders) > 0 {
			// 一旦配置了 allow-list，就只透传显式允许的 header。
			if _, ok := includeHeaders[entry.GetKey()]; !ok {
				continue
			}
		}
		headers[entry.GetKey()] = entry.GetValues()[0]
	}

	original := originalidentity.Extract(req.GetContext().GetMetadata())
	contextExtensions := map[string]string{
		"codec":     req.GetCodec(),
		"namespace": req.GetTarget().GetNamespace(),
		"env":       req.GetTarget().GetEnv(),
		"method":    req.GetMethod(),
	}
	if original.Present() {
		contextExtensions["original_user_id"] = original.UserID
		contextExtensions["original_user_subject"] = original.Subject
		contextExtensions["original_user_issuer"] = original.Issuer
	}

	return &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			// Source/Destination 是 ext_authz 最核心的两个身份维度。
			Source: &authv3.AttributeContext_Peer{
				Service: req.GetContext().GetCaller().GetService(),
			},
			Destination: &authv3.AttributeContext_Peer{
				Service: req.GetTarget().GetService(),
				Address: buildAddress(req.GetTarget().GetService(), req.GetTarget().GetPort()),
			},
			Request: &authv3.AttributeContext_Request{
				// HTTPRequest 是把 gRPC 请求投影到 ext_authz 预期的 HTTP 视图。
				Time: timestamppb.Now(),
				Http: &authv3.AttributeContext_HttpRequest{
					Id:     req.GetContext().GetTraceId(),
					Method: "POST",
					Host:   req.GetTarget().GetService(),
					Path:   req.GetMethod(),
					Scheme: "grpc",
					// header 经过 allow-list 过滤后再放进 ext_authz 请求。
					Headers:  headers,
					Protocol: "HTTP/2",
					RawBody:  req.GetPayload(),
					Size:     int64(len(req.GetPayload())),
				},
			},
			ContextExtensions: contextExtensions,
		},
	}
}

// buildAddress 生成 ext_authz 需要的目标地址结构。
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
		// 允许重复 Close，保证测试或退出钩子里调用更安全。
		return nil
	}
	err := c.conn.Close()
	// 无论 Close 成功与否，都把内部句柄清空，避免后续误用已关闭连接。
	c.conn = nil
	c.client = nil
	if err != nil {
		return fmt.Errorf("close ext_authz client %s failed: %w", c.target, err)
	}
	return nil
}

func dialTimeout(timeoutMS uint64) time.Duration {
	if timeoutMS == 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(timeoutMS) * time.Millisecond
}
