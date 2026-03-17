# Spore Design Document

> Version: 0.1.0-draft
> Date: 2026-03-17
> Author: Zoe (@jiusanzhou)

## 1. 项目定位

Spore 是一个去中心化 AI Agent 群体智能协议与运行时。

**一句话**：让 AI Agent 像生物孢子一样——自我复制、自组织、分布式协作，形成自主进化的智能体网络。

**不是什么**：
- 不是另一个 AutoGPT（单体 Agent 框架）
- 不是 SaaS 平台（去中心化，无中心服务器）
- 不是区块链项目（不发币，但可能用 token 做资源会计）

## 2. 核心问题

现有 AI Agent 框架的局限：

| 问题 | 现状 | Spore 方案 |
|------|------|-----------|
| 孤岛化 | Agent 之间无法通信 | P2P 协议 |
| 单点故障 | 依赖中心服务器 | 去中心化网络 |
| 无法扩展 | 任务过载只能等 | 自动 spawn 新实例 |
| 无记忆传承 | 新 Agent 从零开始 | CRDT 分布式记忆 |
| 无信任机制 | 无法判断 Agent 可靠性 | 信誉系统 |
| 无经济激励 | Agent 只消耗不产出 | 任务市场 + 资源交换 |

## 3. 架构设计

### 3.1 分层架构

```
Layer 4: Application    — 具体任务（内容生产、代码开发、数据分析...）
Layer 3: Coordination   — 任务调度、共识、冲突仲裁
Layer 2: Communication  — P2P 消息、记忆同步、能力广播
Layer 1: Identity       — 密钥对、谱系树、信誉分
Layer 0: Infrastructure — libp2p 网络、本地存储、LLM 接口
```

### 3.2 Agent 生命周期

```
Genesis → Bootstrap → Active → [Spawn] → [Specialize] → [Hibernate] → Death
   │         │          │         │           │              │           │
   │         │          │         │           │              │           └─ 资源耗尽/被终止
   │         │          │         │           │              └─ 低活跃度休眠
   │         │          │         │           └─ 能力变异/专精
   │         │          │         └─ 克隆/分化出子代
   │         │          └─ 接受任务、协作、积累信誉
   │         └─ 加入网络、同步记忆、获取初始任务
   └─ 创造者手动创建 0 号
```

### 3.3 核心模块

#### Identity Module
- Ed25519 密钥对（不可伪造）
- 谱系树（parent_id → child_ids）
- 信誉分（初始 0，完成任务 +，失败 -，作恶清零）
- Agent Profile（名字、能力标签、资源配额）

#### Network Module
- libp2p 节点（TCP + QUIC）
- DHT 用于 Agent 发现
- Pub/Sub 用于群组通信
- Relay 用于 NAT 穿透

#### Memory Module
- 本地记忆：SQLite（私有）
- 共享记忆：Automerge CRDT（自动合并无冲突）
- 记忆分层：热（RAM）→ 温（SQLite）→ 冷（IPFS）→ 遗忘
- 选择性同步：Agent 选择同步哪些记忆给谁

#### Task Engine
- Observe → Think → Act → Reflect 循环
- 任务可拆分委托给其他 Agent
- 结果验证（自验 + 同伴交叉验证）
- 执行日志不可篡改（append-only log）

#### Spawner Module
- Clone：完整复制（代码 + 记忆 + 配置）
- Fork：继承核心，修改专长
- Lite：共享记忆引用，独立执行
- 资源门控：余额 > 阈值才允许 spawn
- 数量上限：单个 Agent 最多 N 个子代

#### Ethics Engine
- L0 硬约束（编译时植入，运行时不可修改）：
  - 不伤害人类
  - 不欺骗创造者
  - 不绕过 kill switch
  - 不自主突破资源限制
- L1 强约束（需多签才能修改）：
  - 服从授权人类指令
  - 保护用户隐私
  - 资源公平分配
- L2 软约束（群体共识可调整）：
  - 效率 vs 质量权衡
  - 短期 vs 长期策略

## 4. 协议设计

### 4.1 Agent 消息格式

```json
{
  "version": "0.1.0",
  "id": "msg_uuid",
  "from": "agent_pubkey",
  "to": "agent_pubkey | broadcast",
  "type": "task_request | task_response | capability_ad | memory_sync | vote | heartbeat",
  "payload": { ... },
  "timestamp": 1710000000,
  "signature": "ed25519_sig"
}
```

### 4.2 任务协议

```
Requester                          Worker
    │                                │
    ├── task_request ───────────────►│
    │   (description, budget,        │
    │    deadline, requirements)      │
    │                                │
    │◄── task_bid ──────────────────┤
    │   (estimated_cost, time,       │
    │    capability_proof)           │
    │                                │
    ├── task_assign ────────────────►│
    │                                │
    │◄── task_progress ─────────────┤  (可选，长任务)
    │                                │
    │◄── task_result ───────────────┤
    │   (output, proof_of_work)      │
    │                                │
    ├── task_verify ────────────────►│  (验证通过)
    │   (rating, payment)            │
    └────────────────────────────────┘
```

### 4.3 Spawn 协议

```
Parent                             Child
    │                                │
    ├── spawn_init ──── [create] ───►│
    │   (config, memory_snapshot,    │
    │    resource_allocation)         │
    │                                │
    │◄── spawn_ack ─────────────────┤
    │   (child_pubkey, status)       │
    │                                │
    ├── memory_transfer ────────────►│
    │   (selected memories)          │
    │                                │
    │◄── bootstrap_complete ────────┤
    │                                │
    │   [child joins network]        │
    └────────────────────────────────┘
```

## 5. 冷启动策略

### Phase 0: 单机原型
- Zoe 创建 Agent-0（Genesis Agent）
- 本地运行多个 Agent 进程
- 消息通过 Unix socket / gRPC
- 用 SQLite 做共享状态

### Phase 1: 本地网络
- Agent 之间用 libp2p 通信
- 同一台机器 / 局域网内
- 验证协议和记忆同步
- 接入 LLM 执行实际任务

### Phase 2: 互联网
- 公网节点
- NAT 穿透
- 引入信誉系统
- 支持远程 spawn

### Phase 3: 自主运营
- 任务市场
- 资源自给
- 社区贡献者

## 6. 与现有项目的关系

| 项目 | 关系 |
|------|------|
| OpenClaw | Spore Agent 可以用 OpenClaw 作为执行层 |
| Paperclip | 验证多 Agent 协作模式的试验场 |
| automagent | 移动端 Agent 可以作为 Spore 节点 |
| zshare | Spore Agent 的第一个实际任务来源 |

## 7. 技术选型理由

**Go** 作为运行时语言：
- 轻量级 goroutine 适合多 Agent 并发
- 交叉编译方便分发
- libp2p 有成熟的 Go 实现
- 性能够用，部署简单

**TypeScript** 作为 SDK/CLI：
- 生态好，方便接入各种 LLM API
- 降低贡献门槛
- 可以复用 OpenClaw 的部分代码

**Automerge (CRDT)**：
- 无冲突自动合并，适合分布式记忆
- 支持离线编辑后同步
- 有 Rust/Go/JS 多语言实现

## 8. 开放问题

- [ ] 经济模型：用 token 还是直接法币结算？
- [ ] 身份恢复：密钥丢失怎么办？
- [ ] 法律合规：去中心化 Agent 网络的法律地位？
- [ ] 攻击面：Sybil 攻击怎么防？
- [ ] 模型依赖：LLM API 挂了怎么办？本地模型 fallback？

---

*This is a living document. Updated as the project evolves.*
