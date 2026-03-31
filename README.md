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
- 第十三版当前范围已完成：
  - agent / sidecar 本地监听当前明确收敛为 `tcp`
  - 运行时本地入口配置统一收敛为 `runtime.<mode>.address`
  - sidecar 不再接受指向本地绑定服务身份的目标
  - sidecar 更明确地只承担“本地服务 -> 上游服务”的代理边界
  - sidecar 通过 `target_mode` 显式声明目标策略，默认 `upstream_only`
  - sidecar 可通过 `allow_cross_scope_same_service` 放宽“同名但跨 namespace / env”的目标
- 第十四版当前范围已完成：
  - control plane 成为 dataplane 默认主路径
  - control plane 开启时默认不再回退本地 source
  - register 后 dataplane 默认消费控制面当前已知的全量快照与策略
- 第十五版当前范围已完成首轮：
  - control plane 具备最小 source cache loader
  - source 快照进入 control plane store 后可生成递增 revision
  - dataplane 可通过 control stream 自动订阅目标服务
  - `pull + cache` 刷新结果可主动推送给已连接 dataplane
  - control plane 已支持按已知目标做周期 refresh
  - 为后续 watch / 增量更新保留基础广播链路
- 第十六版当前范围已完成首轮：
  - dataplane identity 在 runtime 内按进程级稳定生成
  - agent / sidecar 统一复用同一套 register identity 构造逻辑
  - 启动日志与 OTel resource 已挂上 dataplane identity 关键字段
  - control plane 对订阅目标的推送已按目标粒度收敛，而不是默认广播给所有 dataplane
  - register 阶段的 snapshot / route policy 回放已按 dataplane identity 的 namespace / env 收敛
  - subscribe 阶段的 route policy 同步也已按目标订阅与 dataplane identity 收敛
  - route policy 主动变更已支持按目标订阅与 dataplane identity 定向推送
  - snapshot / route policy 下发判定已收敛到统一匹配层，减少后续规则漂移
  - snapshot / route policy / subscriber 的 selector 模型已显式化，后续下发规则更易扩展
  - matcher / selector 组件已从 server 主文件中拆出，便于后续继续扩规则而不堆积在 server.go
  - matcher 已显式区分 exact / fallback 优先级，为后续更复杂命中顺序保留清晰语义
  - register 回放等多候选资源选择已优先取 exact 命中，必要时才回退 fallback
  - 主动推送路径也开始复用 exact / fallback 裁决，避免 fallback 资源覆盖 exact 订阅者
  - resource arbitration 入口已统一化，register / subscribe / 主动推送开始复用同一套候选裁决模型
  - arbitration cache 已引入，减少同一轮 register / subscribe / push 中的重复裁决构造成本
  - delivery cycle 上下文已承接 target 级裁决，subscribe / push 进一步从 server 主流程下沉
  - changed snapshot / already-sent policy 等本轮局部状态也已下沉到 delivery cycle，减少 server 主流程分支噪音
  - push 路径的目标投递判定也开始收敛为统一 delivery plan，broadcast 逻辑进一步瘦身
  - register / subscribe / push 三条路径开始复用统一 delivery batch 模型，stream 回放与异步推送的输出形态继续收敛
  - route policy 主动推送也已并入通用 response / batch 路径，减少独立 push 分支
  - delivery batch 已开始内聚 response / delivery 追加逻辑，为后续 builder / planner 继续演进打底
  - subscribe response 与 target broadcast plan 已直接由 batch builder 构造，`server.go` 进一步退回调度职责
  - batch builder 已补齐集合追加与 push response 入口，旧的零散 helper 继续收口
  - source watch 抽象已落地，memory provider 与 controlplane loader 已具备最小 watch bridge
  - controlplane server 已可消费 watch update，并把 source 变更主动推送给已连接 dataplane
  - consul / etcd 的 polling watch 已收敛为通用 watch runner，减少双份 watcher 骨架
  - controlplane 内部已引入 watch manager，开始统一 tracked target 的 watch 生命周期管理
  - snapshot delete 已有显式 control 消息，watch delete 现在能真正从 controlplane 推到 dataplane 并删除本地状态
  - snapshot status 已支持 `current / stale / degraded`，为 source 不可达时的降级语义打底
  - dataplane 已显式消费 `stale / degraded`：stale 可继续路由，degraded 会拒绝调用或回退原始 source
  - polling watch 已支持 `stale -> degraded` 升级，watch manager 也开始具备自动重建能力
  - source watch 的 degraded 阈值已支持按 provider 配置，watch 错误原因也开始带上统一分类
  - controlplane 已开始为 watch restart 与 snapshot status / reason class 输出最小观测出口
  - `consul / etcd` 的“空健康实例”错误已开始收敛为统一 `empty` 类，便于后续做 provider-specific 错误策略
  - consul / etcd / ext_authz / telemetry 初始化等外部交互已补齐默认超时保护，避免缺环境时长时间挂起

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

- [design/mesh/README.md](../../lhdht/backend/design/mesh/README.md)

推荐先看：

- [service-mesh-plan.md](../../lhdht/backend/design/mesh/service-mesh-plan.md)
- [service-mesh-component-design.md](../../lhdht/backend/design/mesh/service-mesh-component-design.md)
- [service-mesh-proto-contract.md](../../lhdht/backend/design/mesh/service-mesh-proto-contract.md)
- [service-mesh-mvp-decisions.md](../../lhdht/backend/design/mesh/service-mesh-mvp-decisions.md)
- [MODULES.md](./MODULES.md)
- [CURRENT_STATE_AND_PLAN.md](./CURRENT_STATE_AND_PLAN.md)
