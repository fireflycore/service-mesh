# service-mesh 当前模块说明与目录结构

> 状态更新：当前仓库已降级为历史模块参考。`sidecar-agent` 已从 `service-mesh` 方向独立出来，成为当前裸机主路径；本文件只用于帮助识别哪些旧模块可继续摘取复用，哪些应直接停止演进。

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
  plane/control/
    client/
    server/
    snapshot/
  plane/data/
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
    watch/
  examples/mvp/
  README.md
  MODULES.md
```

## 3. 模块说明

- `app/service-mesh`
  - 负责把配置、runtime、source、authz、telemetry、control plane 组装成可运行应用
- `cmd/service-mesh`
  - CLI 入口
  - 包含 `run / validate / print-config / version` 等子命令
- `plane/control/client`
  - data plane 连接 control plane 的客户端
  - 负责 register、heartbeat、subscribe、接收 snapshot / policy / delete 更新
- `plane/control/server`
  - MeshControlPlaneService 服务端
  - 负责 register、目标订阅、定向推送、watch update 广播
- `plane/control/snapshot`
  - control plane 的本地快照存储与 loader
  - 负责 revision、watch bridge、snapshot delete、变更写入
- `plane/data/authz`
  - 对齐 `Envoy ext_authz` 的授权调用
- `plane/data/balancer`
  - 服务实例选择策略
  - 当前以最小 round-robin 语义为主
- `plane/data/invoke`
  - 统一业务调用主链
  - 串起 authz、resolver、transport、retry、timeout、telemetry
- `plane/data/resolver`
  - 把目标服务解析为单个 endpoint
  - 当前已显式消费 `stale / degraded`
- `plane/data/telemetry`
  - OTel trace / metric 的最小抽象层
- `plane/data/transport`
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
- `source/watch`
  - watch 类型、polling watch runner、错误分类与状态升级逻辑
- `source/overlay.go`
  - control plane snapshot 与原始 source 的叠加与回退逻辑
- `source/provider.go`
  - source 抽象与 provider 装配入口
- `source/watch.go`
  - watch 对外兼容类型别名
- `examples/mvp`
  - MVP 控制面、上游服务、客户端与示例配置

## 4. 当前主线责任划分

- M14 主路径
  - `plane/control/client`
  - `plane/control/server`
  - `source/overlay.go`
  - `plane/data/resolver`
- M15 目录汇聚层
  - `plane/control/snapshot`
  - `source/watch`
  - `source/consul`
  - `source/etcd`
  - `source/memory`
  - `plane/control/server/watch_manager.go`
- M16 identity 与定向下发
  - `runtime/shared`
  - `plane/control/server/selector.go`
  - `plane/control/server/delivery_cycle.go`
  - `plane/control/server/delivery_batch.go`

## 5. 当前最重要的状态

- 已完成：
  - control plane 默认主路径
  - target subscribe / targeted push
  - snapshot delete 显式消息
  - snapshot `current / stale / degraded`
  - polling watch 的 `stale -> degraded`
  - watch manager 自动重建
- 仍需继续收口：
  - watch/source 错误分级的正式策略
  - stale / degraded 的观测出口
  - M16 的 identity 命中与运行态统一语义

## 6. 设计文档入口

- V2 文档索引：`../lhdht/backend/design/mesh/v2/README.md`
- 当前重构版总览：`../lhdht/backend/design/mesh/v2/service-mesh-minimal-architecture.md`
- 模块拆分方案：`../lhdht/backend/design/mesh/v2/service-mesh-module-split.md`
- 运行时模式拆分：`../lhdht/backend/design/mesh/v2/service-mesh-runtime-modes.md`
- 数据面拆分方案：`../lhdht/backend/design/mesh/v2/service-mesh-data-plane.md`
- 控制面拆分方案：`../lhdht/backend/design/mesh/v2/service-mesh-control-plane.md`
- 历史设计索引：`../lhdht/backend/design/mesh/README.md`
