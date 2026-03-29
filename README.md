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

## 设计原则

- 业务服务不直接调 Authz
- 业务服务不直接做节点发现
- Invoke / Control 使用自定义 Proto
- Authz 直接复用 `Envoy ext_authz`
- Observe 优先复用 OpenTelemetry

## 文档入口

当前设计文档位于：

- [design/mesh/README.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/README.md)

推荐先看：

- [service-mesh-plan.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-plan.md)
- [service-mesh-component-design.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-component-design.md)
- [service-mesh-proto-contract.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-proto-contract.md)
- [service-mesh-mvp-decisions.md](file:///Users/lhdht/product/lhdht/backend/design/mesh/service-mesh-mvp-decisions.md)
