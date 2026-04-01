# MVP Quickstart

## 启动最小控制面

```bash
go run ./cmd/service-mesh-controlplane --config ./examples/mvp/controlplane.yaml
```

## 启动最小 sidecar

```bash
go run ./cmd/service-mesh run --config ./examples/mvp/sidecar.yaml
```

## 启动 demo upstream

```bash
go run ./cmd/service-mesh-demo-upstream
```

## 发起一次真实调用

```bash
go run ./cmd/service-mesh-demo-client --original-subject alice@example.com --payload hello
```

## 当前这版能做什么

- controlplane 监听 `127.0.0.1:19080`
- sidecar 监听 `127.0.0.1:19091`
- controlplane 预置了 `orders.default.dev` 的 snapshot 和 route policy
- sidecar 会连 controlplane，并把它作为主路径 source
- sidecar 打开了 `trusted_original_identity_injector`

## 当前这版还需要你提供什么

- 一个真实的上游 gRPC 服务监听在 `127.0.0.1:50051`
- 如果你只想先验证链路，可以直接使用仓库自带的 `service-mesh-demo-upstream`
- 如果暂时没有 ext_authz，当前配置已经 `fail_open: true`

## 最小验证点

- controlplane 启动后不退出
- sidecar 启动后不退出
- sidecar 日志里能看到连上 controlplane
- 调用 `orders.default.dev` 时会优先走 controlplane 下发的 endpoint `127.0.0.1:50051`
- demo client 成功时会输出 `demo:hello`
