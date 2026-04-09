# service-mesh 当前问题与后续路径

> 状态更新：本文档属于历史跟踪记录。当前工作区已把裸机主路径收敛到 `sidecar-agent`，`service-mesh` 不再作为继续实现的目标仓库；本文仅保留给后续做能力拆解、历史追溯与代码摘取时参考。

## 1. 当前主线判断

当前仓库的有效主线不是继续重构内部 delivery 细节，而是围绕：

- M14：control plane 成为 dataplane 默认主路径
- M15：control plane 成为稳定目录汇聚层
- M16：identity 与定向下发语义收口

## 2. 当前已完成的关键闭环

- M14
  - control plane 开启时默认不再回退本地 source
  - register 后会回放当前已知快照与策略
- M15
  - source cache / refresh / subscribe / push
  - watch bridge
  - watch manager
  - snapshot delete
  - snapshot `current / stale / degraded`
  - polling watch 的 `stale -> degraded`
  - degraded 阈值按 provider 配置
- M16
  - identity 稳定生成
  - register / subscribe / push 已按 target 与 identity 收敛
- M17
  - original end-user identity 已开始以标准 metadata key 做最小透传
  - ext_authz context_extensions 已开始消费 original user 上下文
  - invoke span 已开始带 original user presence / subject / issuer
  - original end-user identity 当前已显式标记为 `metadata / unverified`
  - effective principal 已开始显式收敛：优先 original user，其次 caller service
  - issuer-only original metadata 不会单独升级为 effective principal
  - trusted injector 已有最小边界：仅 sidecar 显式配置后才会把 original identity trust 升为 `local`
  - 已补最小可运行 MVP：静态 controlplane 二进制 + sidecar 示例配置 + quickstart 文档
  - MVP 现在也已补 demo upstream 与 demo client，可直接验证端到端调用
  - selector / arbitration / delivery batch 已基本立住

## 3. 当前仍存在的问题

- M14
  - controlplane 主路径已经成立，但状态变化还缺更明确的观测出口
- M15
  - source/watch 错误分类已具备基础统一实现，但 provider-specific 策略仍需继续细化
  - `stale / degraded` 已能传播，且已有最小日志/指标出口，但还不够完整
  - provider-specific 的错误分类策略仍未单独细化
- M16
  - identity 已接入运行链路，但命中解释、日志可见性、指标统一语义还不够完整
- M17
  - 当前还是 metadata 级最小透传，尚未形成正式 proto 字段
  - caller identity 与 original end-user identity 已开始显式并存，但合并契约仍未正式固化到 proto
  - trust boundary / signature / verification 规则仍未进入实现
  - trusted injector 仍是本地配置级边界，尚未形成更强的签名/校验约束
- 文档
  - 当前设计文档已基本与代码方向对齐
  - 但仍要以仓库现状文档为准，而不是继续把“未来建议结构”当成“当前已有结构”

## 4. 后续路径

### 第一优先级：M15 最终收口

- 建立 source/watch 错误分类策略表
- 继续补强 snapshot status 的日志/指标出口
- 细化 stale 与 degraded 的升级/回退规则
- 明确不同 provider 下的错误归类

当前已收敛到的最小错误矩阵：

- `timeout`
  - 典型来源：query timeout / context deadline exceeded
- `unavailable`
  - 典型来源：目录服务不可达、连接异常、临时失败
- `empty`
  - 典型来源：provider 能返回服务记录，但没有健康实例
- `internal`
  - 预留给后续更内部的不可归类错误

### 第二优先级：M16 运行态语义收口

- identity 命中与推送决策的 explain 能力
- identity 进入日志、指标与策略命中记录
- 继续避免在 delivery 内部结构上做低价值重构

当前已开始落地的首轮 explain 能力：

- register replay 会输出 identity 与回放响应数量
- subscribe replay 会输出 identity、目标数量与回放数量
- target push 会输出 delivered / denied_subscription / denied_identity / denied_arbitration
- target push 也开始输出 subscription / identity 的 exact / fallback 统计
- register / subscribe replay 也开始输出 snapshot / route policy 的 exact / fallback 命中统计
- explain 统计也开始进入 controlplane telemetry 指标出口
- target push 已开始输出 subscriber 级 decision trace 摘要，便于直接排查投递命中路径
- decision trace 已按 subscriber 稳定排序，并输出 trace_total / trace_shown，避免排障结果漂移
- decision trace 已开始具备正式结构化导出视图，便于后续做 trace/export 而不依赖字符串拼接
- controlplane server 已开始提供可调用的 replay / push explain 导出方法，便于后续接调试接口
- controlplane server 现在也已补齐 subscribe replay explain 导出，register / subscribe / push 三类视图开始对齐
- controlplane 当前 subscriber / tracked target 状态也已可结构化导出，开始具备最小 debug state 视图
- debug state 现在也已覆盖 store 内 snapshot / route policy 当前状态，调试视图开始包含“连接态 + 资源态”

### 第三优先级：M18 最小观测补强

- watch restart 次数
- degraded target 数量
- source error class 统计
- snapshot status 分布

## 5. 明确不建议继续做的事

- 不建议继续围绕 `delivery_batch / delivery_cycle / selector` 做纯结构性打磨
- 不建议在 M15 未收口前提前展开 M17 original end-user identity
- 不建议把未来 `meshctl` 或更大控制面管理面作为当前主线

## 6. 阅读建议

- 仓库模块与目录说明：`MODULES.md`
- 主仓库说明：`README.md`
- 设计索引：`../lhdht/backend/design/mesh/README.md`
