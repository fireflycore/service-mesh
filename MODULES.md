# service-mesh 当前模块说明与目录结构

## 1. 仓库定位

`service-mesh` 当前承接 firefly 体系在非 K8s 阶段的轻量数据面与控制面能力，主线围绕三段：

- M14：让 dataplane 默认消费 control plane 快照
- M15：让 control plane 成为稳定目录汇聚层
- M16：让 agent / sidecar identity 与定向下发语义收口

## 2. 当前目录结构

```text
service-mesh/
  app/
    service-mesh/
  cmd/
    service-mesh/
  controlplane/
    client/
    server/
    snapshot/
  dataplane/
    authz/
    balancer/
    invoke/
    resolver/
    telemetry/
    transport/
  integration/
    extauthz/
    otel/
  pkg/
    config/
    errors/
    model/
  proto/
    acme/
      control/v1/
      invoke/v1/
  runtime/
    agent/
    shared/
    sidecar/
  source/
    consul/
    etcd/
    memory/
    watchapi/
  testdata/
  README.md
  MODULES.md
```

## 3. 模块说明

- `app/service-mesh`
  - 负责把配置、runtime、source、authz、telemetry、controlplane 组装成可运行应用
- `cmd/service-mesh`
  - CLI 入口
  - 包含 `run / validate / print-config / version` 等子命令
- `controlplane/client`
  - dataplane 连接 control plane 的客户端
  - 负责 register、heartbeat、subscribe、接收 snapshot / policy / delete 更新
- `controlplane/server`
  - MeshControlPlaneService 服务端
  - 负责 register、目标订阅、定向推送、watch update 广播
- `controlplane/snapshot`
  - control plane 的本地快照存储与 loader
  - 负责 revision、watch bridge、snapshot delete、变更写入
- `dataplane/authz`
  - 对齐 `Envoy ext_authz` 的授权调用
- `dataplane/balancer`
  - 服务实例选择策略
  - 当前以最小 round-robin 语义为主
- `dataplane/invoke`
  - 统一业务调用主链
  - 串起 authz、resolver、transport、retry、timeout、telemetry
- `dataplane/resolver`
  - 把目标服务解析为单个 endpoint
  - 当前已显式消费 `stale / degraded`
- `dataplane/telemetry`
  - OTel trace / metric 的最小抽象层
- `dataplane/transport`
  - 底层 gRPC transport 封装
- `integration/extauthz`
  - ext_authz 对齐用的外部集成辅助代码
- `integration/otel`
  - OTel 初始化与集成辅助代码
- `pkg/config`
  - 配置模型、默认值、加载、归一化、校验
- `pkg/errors`
  - 通用错误定义
- `pkg/model`
  - 共享数据模型
  - 包括 `ServiceRef / ServiceSnapshot / RoutePolicy` 等
- `proto/acme/control/v1`
  - 控制面协议
  - 当前包含 snapshot、route policy、snapshot delete、snapshot status 等语义
- `proto/acme/invoke/v1`
  - 数据面调用协议
- `runtime/agent`
  - agent 运行时入口
- `runtime/shared`
  - agent / sidecar 共用运行时装配与 identity 逻辑
- `runtime/sidecar`
  - sidecar 运行时入口
- `source/consul`
  - Consul 目录适配器
- `source/etcd`
  - etcd 目录适配器
- `source/memory`
  - 内存版 source，主要用于测试与本地验证
- `source/watchapi`
  - watch 类型、polling watch runner、错误分类与状态升级逻辑
- `source/sourceerr`
  - source 侧统一错误语义
  - 当前已承接 `no healthy endpoints` 这类 provider 共享错误
- `source/overlay.go`
  - controlplane snapshot 与原始 source 的叠加与回退逻辑
- `source/provider.go`
  - source 抽象与 provider 装配入口
- `source/watch.go`
  - watch 对外兼容类型别名
- `testdata`
  - agent / sidecar 样例配置

## 4. 当前主线责任划分

- M14 主路径
  - `controlplane/client`
  - `controlplane/server`
  - `source/overlay.go`
  - `dataplane/resolver`
- M15 目录汇聚层
  - `controlplane/snapshot`
  - `source/watchapi`
  - `source/consul`
  - `source/etcd`
  - `source/memory`
  - `controlplane/server/watch_manager.go`
- M16 identity 与定向下发
  - `runtime/shared`
  - `controlplane/server/selector.go`
  - `controlplane/server/delivery_cycle.go`
  - `controlplane/server/delivery_batch.go`

## 5. 当前最重要的状态

- 已完成：
  - controlplane 默认主路径
  - target subscribe / targeted push
  - snapshot delete 显式消息
  - snapshot `current / stale / degraded`
  - polling watch 的 `stale -> degraded`
  - watch manager 自动重建
- 仍需继续收口：
  - watch/source 错误分级的正式策略
  - stale / degraded 的观测出口
  - M16 的 identity 命中与运行态统一语义
