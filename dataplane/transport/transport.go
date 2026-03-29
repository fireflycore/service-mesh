package transport

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Invoker 抽象“如何把一次 mesh Invoke 请求真正发到下游服务”。
//
// 在 service-mesh 当前阶段，Invoke API 只关心：
// - 目标 endpoint 是谁
// - 要调的 gRPC method 是什么
// - 要透传的 payload bytes 是什么
//
// 因此 transport 层负责把这些信息翻译成真实的网络请求。
type Invoker interface {
	Invoke(ctx context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error)
}

// RawMessage 是 transport 层使用的最小消息包裹。
//
// 它的存在只有一个目的：
// - 让 gRPC codec 能拿到“已经编码好的 protobuf bytes”
// - 避免 transport 在不知道具体消息类型的前提下重新构造 proto.Message
type RawMessage struct {
	// Payload 就是已经编码好的业务 protobuf bytes。
	Payload []byte
}

// rawProtoCodec 让客户端以“proto content-type + 原始 bytes”方式发起调用。
//
// 这样 transport 不需要了解下游业务 proto 的具体 Go 类型：
// - 请求方向：直接把 req.Payload 原样写到 gRPC wire
// - 响应方向：把下游返回的 protobuf bytes 原样放回响应
//
// 约束是：
// - req.Method 必须是完整的 gRPC method path
// - req.Payload 必须已经是目标请求消息的 protobuf 编码结果
type rawProtoCodec struct{}

// Marshal 直接透传已经编码好的 protobuf bytes。
func (c rawProtoCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(*RawMessage)
	if !ok {
		return nil, fmt.Errorf("raw proto codec expects *RawMessage, got %T", v)
	}
	return msg.Payload, nil
}

// Unmarshal 直接把下游返回的原始 protobuf bytes 收进 RawMessage。
func (c rawProtoCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(*RawMessage)
	if !ok {
		return fmt.Errorf("raw proto codec expects *RawMessage, got %T", v)
	}
	msg.Payload = append(msg.Payload[:0], data...)
	return nil
}

// Name 返回 proto，确保 gRPC content-type 仍然保持兼容。
func (c rawProtoCodec) Name() string {
	// 这里显式返回 proto。
	//
	// 这样发出去的 content-type 仍然是 grpc+proto，
	// 下游业务服务可以继续使用默认 protobuf codec 来解码请求。
	return "proto"
}

// GRPC 是第三版引入的真实网络 transport。
//
// 它做的事情很克制：
// - 负责连接缓存
// - 负责把 endpoint 转成 gRPC 连接
// - 负责把 req.Method + req.Payload 发给下游
//
// 它不负责：
// - 解析服务目录
// - 做实例选择
// - 做鉴权判断
type GRPC struct {
	mu sync.Mutex
	// conns 按 host:port 复用底层连接，避免每次 Invoke 都重新拨号。
	conns map[string]*grpc.ClientConn
	codec rawProtoCodec
}

// NewGRPC 创建默认的 gRPC transport。
func NewGRPC() *GRPC {
	return &GRPC{
		conns: make(map[string]*grpc.ClientConn),
		codec: rawProtoCodec{},
	}
}

// Invoke 使用真实 gRPC 连接把 payload 发到下游。
//
// 当前约束：
// - req.Method 必须是完整的 gRPC method 路径，例如 `/acme.foo.v1.BarService/Get`
// - req.Payload 必须是目标请求消息的 protobuf bytes
func (t *GRPC) Invoke(ctx context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	// endpoint.Address/Port 会被收敛成标准 host:port 形式。
	target := net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port)))

	conn, err := t.conn(ctx, target)
	if err != nil {
		return nil, err
	}

	request := &RawMessage{Payload: req.GetPayload()}
	response := &RawMessage{}

	// ForceCodec 让 gRPC 走我们的“原始 protobuf bytes”编解码路径。
	if err := conn.Invoke(ctx, req.GetMethod(), request, response, grpc.ForceCodec(t.codec)); err != nil {
		return nil, err
	}

	return &invokev1.UnaryInvokeResponse{
		Payload: response.Payload,
		Codec:   req.GetCodec(),
	}, nil
}

// conn 复用到同一个 target 的底层连接，减少重复拨号成本。
func (t *GRPC) conn(ctx context.Context, target string) (*grpc.ClientConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, ok := t.conns[target]; ok {
		// 已有连接直接复用，避免重复建链带来的时延与资源开销。
		return conn, nil
	}

	conn, err := grpc.DialContext(
		ctx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, err
	}

	// 只有拨号成功后才放进缓存，避免缓存半初始化连接。
	t.conns[target] = conn
	return conn, nil
}
