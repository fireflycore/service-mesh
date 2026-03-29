# service-mesh

`service-mesh` 是 firefly 体系中面向非 K8s 阶段的轻量服务网格项目，用于承接：

- `go-micro/invocation` 的统一业务调用语义
- `agent` 优先的数据面治理模型
- 外挂 `Authz` 对齐 `Envoy ext_authz`
- `consul / etcd` 这类目录来源的统一适配

## 当前定位

- 默认运行模式：`agent`
- 可选运行模式：`sidecar`
- 非 K8s 阶段提供轻量数据面能力
- 进入 `K8s + Istio` 阶段后，由平台侧主数据面替换

## 当前目标

第一阶段重点不是一次性做完整 service mesh，而是先跑通这条主链路：

```text
service
  -> go-micro/invocation
  -> local service-mesh
  -> authz(ext_authz)
  -> source(consul / etcd)
  -> remote service
```

## 当前实现进度

- 第一版：
  - 完成仓库骨架、CLI、配置模型
- 第二版：
  - 跑通 `Buf CLI`
  - 跑通本地 `Invoke` gRPC 服务
- 第三版当前范围已完成：
  - 接入真实 `consul source`
  - 接入真实 gRPC transport
  - `method` 统一使用完整 gRPC method path
- 第四版当前范围已完成：
  - 接入真实 `ext_authz client`
  - 实现 `fail-close / fail-open`
  - 实现 Authz timeout 与错误处理
- 第五版当前范围已完成：
  - 接入真实 `etcd source`
  - 输出统一 `ServiceSnapshot`
  - 与 `consul source` 保持一致的内部快照语义
- 第七版当前范围已完成：
  - 接入 OTel trace provider
  - 接入 OTel metric provider
  - 为 Invoke 调用链补充基础埋点与 gRPC instrumentation
- 第八版当前范围已完成：
  - 最小 `MeshControlPlaneService`
  - dataplane register / heartbeat
  - 基础 `ServiceSnapshot / RoutePolicy` 下发
- 第九版当前范围已完成：
  - 让 controlplane 下发 `ServiceSnapshot` 真正进入 dataplane 生效链
  - 让 controlplane 下发 `RoutePolicy` 真正影响 timeout / retry
  - 保持 controlplane 优先、本地配置与目录回退
- 第十版当前范围已完成：
  - 让 `sidecar` 运行时复用同一套 dataplane 主链
  - 让 `sidecar` 具备 invoke / controlplane / telemetry 基础能力
  - 验证 agent / sidecar 只保留运行时身份与监听差异
- 第十一版当前范围已完成：
  - sidecar 具备更明确的 `service / namespace / env / instance_id` 本地身份
  - controlplane register 使用 sidecar 精确身份
  - sidecar 更容易命中按服务维度下发的 snapshot / route policy
- 第十二版当前范围已完成：
  - sidecar 自动补齐 caller identity
  - sidecar 自动补齐默认 `namespace / env`
  - sidecar 拒绝与本地绑定身份冲突的调用上下文
- 第十二版当前目标：
  - sidecar 自动补齐 caller identity
  - sidecar 自动补齐默认 `namespace / env`
  - sidecar 拒绝与本地绑定身份冲突的调用上下文
- 第十版当前目标：
  - 让 `sidecar` 运行时复用同一套 dataplane 主链
  - 让 `sidecar` 具备 invoke / controlplane / telemetry 基础能力
  - 验证 agent / sidecar 只保留运行时身份与监听差异

## 设计原则

- 业务服务不直接调 Authz
- 业务服务不直接做节点发现
- Invoke / Control 使用自定义 Proto
- Authz 直接复用 `Envoy ext_authz`
- Observe 优先复用 OpenTelemetry

## Proto 工具链

- Proto 源文件放在：
  - `proto/acme/invoke/v1`
  - `proto/acme/control/v1`
- 使用 `proto/buf.yaml` 管理模块、lint 与 breaking
- 使用仓库根 `buf.gen.yaml` 管理生成规则
- 生成代码输出到：
  - `.gen/proto`

常用命令：

```bash
buf lint proto
buf generate
```

参考：

- https://buf.build/docs/

## 依赖装配

- 第一版先手写构造与装配
- 当前不引入 `Wire`
- 当 controlplane / dataplane / source / integration 的装配复杂度明显上升后，再评估是否引入

## 文档入口

当前设计文档位于：

- [design/mesh/README.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/README.md)

推荐先看：

- [service-mesh-plan.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-plan.md)
- [service-mesh-component-design.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-component-design.md)
- [service-mesh-proto-contract.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-proto-contract.md)
- [service-mesh-mvp-decisions.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-mvp-decisions.md)
