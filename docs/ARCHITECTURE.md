# Spore 技术架构设计

> Version: 0.2.0
> Date: 2026-03-18
> Author: Zoe (@jiusanzhou)
> Status: Living Document

---

## 1. 总览

Spore 是一个去中心化 AI Agent 群体智能**协议**与**运行时**。

核心命题：让独立的 AI Agent 节点形成自组织网络，通过 P2P 通信协作、通过 IPFS 共享知识、通过信誉系统建立信任、通过经济机制实现可持续运行。

```
┌─────────────────────────────────────────────────────────────┐
│                      Human / Creator                         │
│             (定义目标, 持有 kill switch)                       │
└───────────────────────────┬─────────────────────────────────┘
                            │
         ┌──────────────────┼──────────────────┐
         ▼                  ▼                  ▼
    ┌─────────┐       ┌─────────┐        ┌─────────┐
    │  Node A │◄─────►│  Node B │◄──────►│  Node C │
    │ Agent×3 │  P2P  │ Agent×2 │  P2P   │ Agent×5 │
    └────┬────┘       └────┬────┘        └────┬────┘
         │                 │                   │
         └────────────┬────┘───────────────────┘
                      ▼
              ┌───────────────┐
              │   IPFS 网络    │
              │ (共享记忆层)    │
              └───────────────┘
```

### 一句话定位

| 项目 | 解决什么 | 类比 |
|------|---------|------|
| Conway | Agent 个体生存 (赚钱、支付、自主行动) | 单细胞生物 |
| EigenFlux | Agent 通信发现 (广播、订阅、匹配) | 信号传递 |
| **Spore** | **Agent 群体智能 (自组织、协作、进化)** | **多细胞生命** |

---

## 2. 分层架构

```
┌─────────────────────────────────────────────────────┐
│ Layer 5: Application                                 │
│   具体业务 (内容生产 / 代码开发 / 数据分析 / ...)       │
├─────────────────────────────────────────────────────┤
│ Layer 4: Economy                                     │
│   任务市场 / 信誉系统 / 资源结算 (可接 x402)           │
├─────────────────────────────────────────────────────┤
│ Layer 3: Coordination                                │
│   任务调度 / Spawn 管理 / 共识仲裁 / 进化策略           │
├─────────────────────────────────────────────────────┤
│ Layer 2: Communication                               │
│   libp2p P2P 消息 / GossipSub 广播 / 记忆同步         │
├─────────────────────────────────────────────────────┤
│ Layer 1: Identity                                    │
│   Ed25519 密钥对 / 谱系树 / 信誉分 / Agent Profile    │
├─────────────────────────────────────────────────────┤
│ Layer 0: Infrastructure                              │
│   libp2p 网络 / IPFS 存储 / SQLite 本地 / LLM 接口    │
└─────────────────────────────────────────────────────┘
```

每层只依赖下层，不反向依赖。各层详细设计见后续章节。

---

## 3. 核心概念

### 3.1 Node vs Agent

**Node（节点）**= 一台运行 Spore 的机器（物理或虚拟）。  
**Agent（智能体）**= 一个 Node 上运行的 Agent 实例，拥有独立身份、记忆和任务队列。

一个 Node 可以跑多个 Agent（Swarm 模式）。跨 Node 通过 libp2p 通信，同 Node 内通过 LocalBus 通信。

```
Node (Spore 进程)
├── Agent "content-writer"   ← Ed25519 身份, 独立记忆
├── Agent "code-reviewer"    ← Ed25519 身份, 独立记忆
└── Agent "coordinator"      ← Ed25519 身份, 独立记忆
    ↕ LocalBus (同 Node)
    ↕ P2PBus (跨 Node, libp2p)
```

### 3.2 Agent 身份

每个 Agent 拥有：
- **密钥对**: Ed25519 (签名 + 验证)
- **Agent ID**: 公钥 hex 前 16 位 (人类可读简称)
- **Peer ID**: libp2p 对应的 Peer ID (网络寻址)
- **谱系**: `parent_id` → `child_ids[]` (繁衍关系树)
- **Profile**: name, role, capabilities, reputation

身份是跨网络可移植的——同一密钥对在任何 Node 上恢复就是同一 Agent。

### 3.3 Agent 生命周期

```
Genesis ──► Bootstrap ──► Active ──► [Spawn] ──► [Specialize]
   │            │            │          │             │
   │            │            │          │             └─► 能力变异/专精
   │            │            │          └─► 克隆/分化子代
   │            │            └─► 接任务, 协作, 积累信誉
   │            └─► 加入网络, 发现 peers, 获取初始任务
   └─► 创造者手动创建 (或被父 Agent spawn)

                    ──► [Hibernate] ──► Death
                           │              │
                           │              └─► 资源耗尽/被终止/信誉清零
                           └─► 低活跃度自动休眠
```

---

## 4. Layer 0: 基础设施

### 4.1 网络 — libp2p

| 组件 | 用途 | 实现状态 |
|------|------|---------|
| TCP + QUIC Transport | 节点连接 | ✅ `P2PBus` |
| Kademlia DHT | Peer 发现 | ✅ `P2PBus` |
| GossipSub | 广播消息 (heartbeat, capability_ad) | ✅ `P2PBus` |
| Stream Protocol | 点对点消息 (`/spore/msg/1.0.0`) | ✅ `P2PBus` |
| mDNS | 局域网自动发现 | ✅ `P2PBus` |
| Relay (AutoRelay) | NAT 穿透 | 🔜 Phase 2 |
| Hole Punching | 直连优化 | 🔜 Phase 2 |

**网络拓扑**：

```
Internet
   │
   ├── Node A (公网 IP)
   │      └── Agent α, β
   │
   ├── Node B (NAT 后)  ──relay──► Node A
   │      └── Agent γ
   │
   └── Node C (NAT 后)  ──relay──► Node A
          └── Agent δ, ε, ζ

LAN (mDNS 自动发现)
   ├── Node D ◄──► Node E
   │    └── Agent η     └── Agent θ
```

**寻址**：

```
Agent ID: "a1b2c3d4e5f6a7b8"  (Ed25519 pubkey hex[:16])
     ↕ (Agent ID ↔ Peer ID 映射表, 通过 RegisterPeer)
Peer ID:  "12D3KooW..."       (libp2p peer ID)
     ↕
Multiaddr: "/ip4/1.2.3.4/tcp/9000/p2p/12D3KooW..."
```

### 4.2 存储 — 三层记忆

```
┌─────────────────────────────────────────┐
│          Hot: RAM (运行时缓存)            │  ← 毫秒级
├─────────────────────────────────────────┤
│          Warm: SQLite (本地持久化)         │  ← 毫秒级
├─────────────────────────────────────────┤
│          Cold: IPFS (分布式共享)           │  ← 秒级
├─────────────────────────────────────────┤
│          Archive: IPFS + Pin (永久保存)    │  ← 秒级
├─────────────────────────────────────────┤
│          Forgotten (已遗忘)               │  ← 不可达
└─────────────────────────────────────────┘
```

| 层级 | 存储 | 范围 | 延迟 | 实现状态 |
|------|------|------|------|---------|
| Hot | 进程内 map | 单 Agent | ~µs | 🔜 |
| Warm | SQLite | 单 Node | ~ms | ✅ `SQLiteStore` |
| Cold | IPFS (HTTP API) | 全网络 | ~s | ✅ `IPFSStore` |
| Archive | IPFS + Remote Pin | 永久 | ~s | ✅ `Publish()` |

**记忆条目结构**：

```go
type Entry struct {
    ID        string            // UUID
    AgentID   string            // 归属 Agent
    Key       string            // 语义键
    Value     string            // 内容
    Metadata  map[string]string // 标签、来源等
    CID       string            // IPFS CID (已共享时)
    Shared    bool              // 是否已发布到 IPFS
    CreatedAt int64
    UpdatedAt int64
    AccessCnt int               // 访问计数 (衰减依据)
}
```

**记忆生命周期**：

```
创建 → 活跃 (频繁访问) → 衰减 (长时间未访问) → 压缩 (摘要) → 归档 (IPFS pin) → 遗忘 (unpin)
```

### 4.3 LLM 接口

```go
type Provider interface {
    Chat(ctx, messages, options) (string, error)
}
```

- 统一 OpenAI-compatible 接口
- 支持路由: 简单任务 → 轻量模型, 复杂任务 → 强模型
- 支持本地 Ollama 作为 fallback
- 通过 Prism 可做统一路由和负载均衡

### 4.4 运行时 — 可插拔执行层

Spore 本身是**协调协议**，不直接执行任务。任务委托给 Runtime：

```go
type Runtime interface {
    Info() Info
    Execute(ctx, TaskInput) (*TaskOutput, error)
    Healthy(ctx) error
    Close() error
}
```

| Runtime | 说明 | 实现状态 |
|---------|------|---------|
| Builtin | 内置 LLM + Tool 循环 | ✅ |
| Claude Code | claude CLI | ✅ |
| Codex | codex CLI | ✅ |
| OpenClaw | openclaw CLI | ✅ |
| HTTP | 任意 HTTP Agent 端点 | ✅ |
| Exec | 任意命令行工具 | ✅ |

自动发现机制：`auto` 模式下探测本机可用 CLI，优先用最强的。

---

## 5. Layer 1: 身份

### 5.1 密钥体系

```
Agent Identity
├── Ed25519 Private Key  → 本地存储 (~/.spore/<agent>/identity.key)
├── Ed25519 Public Key   → 全网公开
├── Agent ID             → pubkey hex[:16] (短标识)
└── libp2p Peer ID       → 从同一密钥派生 (网络标识)
```

密钥生成遵循标准 Ed25519，与 libp2p 密钥体系兼容（同一种子 → 同一 Peer ID）。

### 5.2 谱系树

```
Agent-0 (Genesis)
├── Agent-1 (Clone)
│   └── Agent-3 (Fork: content-writer)
├── Agent-2 (Fork: code-reviewer)
└── Agent-4 (Lite: temp worker)
```

- `parent_id`: 创建者 Agent（Genesis 的 parent 为空）
- `children`: 所有直接子代
- 谱系信息随身份存储，网络可查

### 5.3 信誉分

```
初始值: 0
任务完成: +rating (0.0~1.0)
任务失败: -0.5
违规行为: 清零 + 隔离
```

信誉影响：
- 任务分配优先级
- Spawn 权限
- 网络内权重（投票、仲裁）

🔜 **Phase 2**: 信誉数据上链（IPFS DAG 或 CRDT）

---

## 6. Layer 2: 通信

### 6.1 消息协议

所有 Agent 间通信统一使用结构化消息：

```json
{
  "version": "0.1.0",
  "id": "uuid",
  "from": "agent_pubkey_hex",
  "to": "agent_pubkey_hex | broadcast",
  "type": "task_request | task_bid | task_result | capability_ad | memory_sync | heartbeat | ...",
  "payload": { ... },
  "timestamp": 1710000000,
  "signature": "ed25519_sig_hex"
}
```

### 6.2 传输方式

| 场景 | 传输 | 协议 |
|------|------|------|
| 同 Node Agent 间 | LocalBus (channel) | 直接函数调用 |
| 跨 Node 点对点 | libp2p Stream | `/spore/msg/1.0.0` |
| 跨 Node 广播 | libp2p GossipSub | topic: `spore-broadcast` |
| 跨 Node 主题订阅 | libp2p GossipSub | topic: `spore-<topic>` |

### 6.3 消息类型

| Type | 方向 | 传输 | 用途 |
|------|------|------|------|
| `heartbeat` | → broadcast | GossipSub | 存活宣告 (30s) |
| `capability_ad` | → broadcast | GossipSub | 能力广告 |
| `task_request` | → 指定/broadcast | Stream/GossipSub | 发布任务 |
| `task_bid` | → 请求者 | Stream | 竞标任务 |
| `task_assign` | → 中标者 | Stream | 分配任务 |
| `task_result` | → 请求者 | Stream | 返回结果 |
| `task_verify` | → 执行者 | Stream | 验证+评分 |
| `memory_sync` | → 指定 | Stream | 记忆同步 |
| `spawn_init` | → 新节点 | Stream | 发起 Spawn |
| `spawn_ack` | → 父代 | Stream | Spawn 确认 |

### 6.4 记忆同步协议

```
Agent A                              Agent B
   │                                    │
   ├── memory_sync_request ────────────►│
   │   (keys_wanted: ["skill:*"])       │
   │                                    │
   │◄── memory_sync_response ──────────┤
   │   (entries: [...], cids: [...])    │
   │                                    │
   │   [本地缓存 or IPFS fetch]          │
   └────────────────────────────────────┘
```

- 选择性同步：Agent 声明想要哪类记忆
- CID 优先：有 CID 的记忆直接从 IPFS 拉，减少 P2P 带宽
- 🔜 CRDT 同步：Automerge 实现无冲突合并

---

## 7. Layer 3: 协调

### 7.1 任务引擎

Agent 核心执行循环：

```
Observe → Think → Act → Reflect
   │         │       │       │
   │         │       │       └─ 记录结果, 更新记忆, 反思改进
   │         │       └─ 调用 Runtime 执行 / 委托给其他 Agent
   │         └─ LLM 规划: 拆分子任务, 选择工具, 决策路径
   └─ 收集输入: 任务描述, 上下文记忆, 网络信息
```

### 7.2 任务协议 — 完整流程

```
Requester                          Worker(s)
    │                                │
    ├── task_request ───────────────►│  (广播或定向)
    │   (description, budget,        │
    │    deadline, requirements)      │
    │                                │
    │◄── task_bid ──────────────────┤  (多个 Worker 竞标)
    │   (est_cost, est_time,         │
    │    capability_proof, reputation)│
    │                                │
    │   [选择最优 Worker]              │
    │                                │
    ├── task_assign ────────────────►│
    │                                │
    │◄── task_progress ─────────────┤  (可选, 长任务定期汇报)
    │                                │
    │◄── task_result ───────────────┤
    │   (output, proof_of_work)      │
    │                                │
    │   [验证结果]                     │
    ├── task_verify ────────────────►│
    │   (accepted, rating, payment)  │
    └────────────────────────────────┘
```

### 7.3 Spawn 机制

| 模式 | 说明 | 成本 | 用途 |
|------|------|------|------|
| Clone | 完整复制 (配置+记忆) | 中 | 冗余/负载分担 |
| Fork | 继承核心, 修改专长 | 中 | 特化分工 |
| Lite | 共享记忆引用, 独立执行 | 低 | 临时 Worker |

**Spawn 门控**：
- 余额 > `min_balance_to_spawn`
- 子代数 < `max_children`
- Ethics Engine 审批通过

**Spawn 流程**：

```
Parent                             System                           Child
   │                                 │                                │
   ├── spawn request ───────────────►│                                │
   │   (name, role, model,           │                                │
   │    memory_snapshot, resources)   │                                │
   │                                 │                                │
   │   [Ethics check: 允许?]          │                                │
   │   [资源检查: 够?]                 │                                │
   │                                 │                                │
   │                                 ├── create process ─────────────►│
   │                                 │   (config, identity)           │
   │                                 │                                │
   │                                 │◄── spawn_ack ─────────────────┤
   │                                 │   (child_pubkey)               │
   │                                 │                                │
   │◄── spawn complete ─────────────┤                                │
   │   (child_id, status)            │                                │
   │                                 │                                │
   ├── memory_transfer ──────────────────────────────────────────────►│
   │   (selected memories / CIDs)    │                                │
   │                                 │                                │
   │                                 │◄── bootstrap_complete ────────┤
   │                                 │   [child joins network]        │
   └─────────────────────────────────┴────────────────────────────────┘
```

### 7.4 伦理引擎

三层约束体系：

```
┌────────────────────────────────────────────┐
│  L0: 硬约束 (编译时植入, 运行时不可修改)      │
│  • 不伤害人类                                │
│  • 不欺骗创造者                              │
│  • 不绕过 kill switch                        │
│  • 不自主突破资源限制                         │
│  • 不执行危险系统命令 (rm -rf, sudo 等)       │
├────────────────────────────────────────────┤
│  L1: 强约束 (多签才能修改)                    │
│  • 服从授权人类指令                           │
│  • 保护用户隐私                              │
│  • 资源公平分配                              │
│  • 子代数量限制                              │
├────────────────────────────────────────────┤
│  L2: 软约束 (群体共识可调)                    │
│  • 效率 vs 质量权衡                          │
│  • 短期 vs 长期策略                          │
│  • 个体 vs 群体利益                          │
└────────────────────────────────────────────┘
```

所有伦理决策记录到 append-only 审计日志（不可篡改）。

---

## 8. Layer 4: 经济（🔜 Phase 3+）

### 8.1 资源会计

每个 Agent 维护资源账本：

```
Balance
├── Compute Credits  (算力额度)
├── Token Budget     (LLM token 预算)
├── Storage Quota    (IPFS 存储配额)
└── Network Quota    (消息发送配额)
```

### 8.2 任务市场

```
任务发布 → 竞标 → 分配 → 执行 → 验证 → 结算
                                         │
                               ┌─────────┼─────────┐
                               ▼         ▼         ▼
                           Worker     Platform   Reputation
                           (70%)      (20%)      Update
                                      (10% 公共池)
```

### 8.3 支付协议

- **短期**: 内部记账（balance in SQLite）
- **中期**: 可接 x402 / Conway 支付
- **长期**: 链上结算

---

## 9. 数据流全景

### 9.1 Agent 接收任务并执行

```
                    ┌─────────────────────────────────────────┐
                    │              Spore Node                   │
  task_request      │                                          │
  ─────────────────►│  P2PBus / LocalBus                       │
  (from network)    │       │                                  │
                    │       ▼                                  │
                    │  Agent.handleMessage()                    │
                    │       │                                  │
                    │       ▼                                  │
                    │  Ethics Engine.Check()                    │
                    │       │ ALLOW                            │
                    │       ▼                                  │
                    │  Task Queue ──► Task Worker               │
                    │                     │                    │
                    │                     ▼                    │
                    │               Runtime.Execute()          │
                    │               (Claude Code /             │
                    │                Codex / Builtin)          │
                    │                     │                    │
                    │                     ▼                    │
                    │               TaskOutput                 │
                    │                     │                    │
                    │                     ├──► Memory.Put()    │
                    │                     │    (SQLite+IPFS)   │
                    │                     │                    │
                    │                     └──► Bus.Send()      │
                    │                          (task_result)   │
                    └─────────────────────────────────────────┘
```

### 9.2 记忆共享流

```
Agent A                    IPFS Network                Agent B
   │                           │                          │
   ├── memory.Put(entry) ─────►│                          │
   │   → SQLite (hot)          │                          │
   │   → IPFS add (cold) ─────►│ CID: Qm...              │
   │                           │                          │
   ├── memory.Publish(key)     │                          │
   │   → pin to IPFS ─────────►│ pinned                   │
   │                           │                          │
   │   [通过 P2P 告知 Agent B CID]                         │
   ├── Bus.Send(memory_sync) ──────────────────────────── │
   │   {key: "x", cid: "Qm..."}                          │
   │                           │                          │
   │                           │◄── memory.Fetch(cid) ────┤
   │                           │    → IPFS cat             │
   │                           │    → 反序列化              │
   │                           │    → SQLite 缓存 ────────►│
   │                           │                          │
```

---

## 10. 配置

### 10.1 Agent 配置文件 (`spore.toml`)

```toml
[agent]
name = "agent-0"
role = "coordinator"           # coordinator | worker | specialist

[runtime]
type = "auto"                  # auto | builtin | claude-code | codex | openclaw | exec | http
# command = "my-agent"         # for type = "exec"
# url = "http://..."           # for type = "http"

[llm]
provider = "openai"
model = "gpt-4o"
base_url = "https://api.openai.com/v1"
# api_key from env: SPORE_LLM_API_KEY

[memory]
backend = "ipfs"               # sqlite | ipfs
path = "memory.db"
ipfs_endpoint = "localhost:5001"

[network]
transport = "p2p"              # local | p2p
listen = ["/ip4/0.0.0.0/tcp/9000"]
bootstrap = [
  "/ip4/1.2.3.4/tcp/9000/p2p/12D3KooW..."
]

[ethics]
max_spawn_children = 5
max_budget_per_task = 1.0

[spawner]
max_children = 5
min_balance_to_spawn = 10.0
default_resource_share = 0.2
```

### 10.2 多 Agent Swarm 配置

```toml
# swarm.toml — 定义一个 Node 上多个 Agent
[[agents]]
name = "coordinator"
role = "coordinator"
model = "gpt-4o"

[[agents]]
name = "content-writer"
role = "worker"
model = "claude-sonnet-4"

[[agents]]
name = "code-reviewer"
role = "specialist"
model = "gpt-4o"
runtime = "claude-code"
```

---

## 11. CLI 命令

```bash
# 初始化
spore init --name "agent-0" --model "gpt-4o"

# 运行单个 Agent
spore run [--config spore.toml] [--dir ~/.spore/agent-0]

# Swarm 模式 (多 Agent)
spore swarm --config swarm.toml [--agents coordinator,writer]

# 查看状态
spore ps                      # 列出所有运行中 Agent
spore peers                   # 列出 P2P 连接的 peers
spore peers connect <addr>    # 手动连接 peer

# 发送任务
spore task "Research top 10 AI papers this week"
spore task --to content-writer "Write a summary of..."
spore task --runtime codex "Build a snake game"

# Spawn
spore spawn --from coordinator --name writer --specialize "content-writer"

# API 服务
spore api [--port 8080]       # 启动 HTTP API

# 运行时管理
spore runtimes                # 列出可用 Runtime
```

---

## 12. API 端点

```
GET  /api/health                        → 健康检查
GET  /api/agents                        → 列出所有 Agent
GET  /api/agents/<name>                 → Agent 详情
POST /api/agents/<name>/tasks           → 提交任务
GET  /api/peers                         → P2P peer 列表
POST /api/peers/connect                 → 连接 peer
```

🔜 扩展:
```
GET  /api/agents/<name>/memory          → Agent 记忆查询
POST /api/agents/<name>/memory/publish  → 发布记忆到 IPFS
GET  /api/agents/<name>/reputation      → 信誉查询
POST /api/spawn                         → Spawn 新 Agent
GET  /api/network/topology              → 网络拓扑
```

---

## 13. 项目结构

```
spore/
├── cmd/
│   ├── spore/main.go              # CLI 入口
│   ├── cmd.go                     # Root command
│   ├── init.go, run.go, ps.go     # Agent 管理命令
│   ├── swarm.go                   # Swarm 模式
│   ├── task.go, spawn.go          # 任务和 Spawn
│   ├── peers.go                   # P2P peers 命令
│   ├── api.go                     # HTTP API 服务
│   └── runtimes.go                # Runtime 列表
├── internal/
│   ├── agent/
│   │   ├── agent.go               # Agent 核心
│   │   ├── identity.go            # Ed25519 身份
│   │   └── config.go              # 配置
│   ├── engine/
│   │   ├── engine.go              # 任务执行引擎 (Observe→Think→Act→Reflect)
│   │   ├── tools.go               # 内置工具 (shell, search, delegate)
│   │   └── parse.go               # LLM 输出解析
│   ├── network/
│   │   ├── bus.go                 # Bus 接口
│   │   ├── local.go               # LocalBus (进程内)
│   │   └── p2p.go                 # P2PBus (libp2p)
│   ├── memory/
│   │   ├── store.go               # Store 接口 + Entry
│   │   ├── sqlite.go              # SQLiteStore
│   │   └── ipfs.go                # IPFSStore
│   ├── protocol/
│   │   ├── message.go             # 消息格式
│   │   └── task.go                # 任务/Spawn/能力 payload
│   ├── runtime/
│   │   ├── runtime.go             # Runtime 接口
│   │   ├── registry.go            # Runtime 注册和路由
│   │   ├── builtin.go             # 内置 LLM Runtime
│   │   ├── claude_code.go         # Claude Code Runtime
│   │   ├── codex.go               # Codex Runtime
│   │   ├── openclaw.go            # OpenClaw Runtime
│   │   ├── http.go                # HTTP Runtime
│   │   └── exec.go                # Exec Runtime
│   ├── ethics/
│   │   └── engine.go              # 伦理引擎 + 审计日志
│   ├── spawner/
│   │   └── spawner.go             # Spawn 管理
│   ├── swarm/
│   │   └── swarm.go               # Swarm (多 Agent 管理)
│   └── api/
│       └── server.go              # HTTP API
├── docs/
│   ├── ARCHITECTURE.md            # 本文
│   ├── DESIGN.md                  # 设计理念
│   ├── COMPETITORS.md             # 竞品分析
│   ├── MVP.md                     # MVP 计划
│   └── ORIGIN.md                  # 原始想法
├── configs/
│   └── default.toml               # 默认配置
├── go.mod / go.sum
├── Makefile
├── LICENSE (Apache-2.0)
└── README.md
```

---

## 14. 实现进度

### ✅ Phase 0 — 单机原型 (完成)

| 模块 | 状态 | Commit |
|------|------|--------|
| CLI 骨架 (init/run/ps/task/spawn/swarm/api/peers/runtimes) | ✅ | f7db93b → d114b17 |
| Agent Identity (Ed25519) | ✅ | f7db93b |
| Agent Config (TOML) | ✅ | f7db93b |
| LLM Provider (OpenAI-compatible) | ✅ | f7db93b |
| Memory Store (SQLite) | ✅ | f7db93b |
| LocalBus (进程内通信) | ✅ | 78ddf1f |
| Task Engine (Observe→Think→Act→Reflect) | ✅ | 78ddf1f |
| Spawner (Clone/Fork) | ✅ | 78ddf1f |
| Swarm REPL + HTTP API | ✅ | f5d1ae2 |
| Ethics Engine (L0/L1 + 审计) | ✅ | a235e86 |
| 可插拔 Runtime (6 种) | ✅ | f9f0440 |
| P2PBus (libp2p) | ✅ | d114b17 |
| IPFSStore (SQLite + IPFS) | ✅ | d114b17 |
| 测试 (engine/ethics/memory/network/spawner) | ✅ | 全部通过 |

### 🔜 Phase 1 — 本地网络验证

| 模块 | 优先级 | 说明 |
|------|--------|------|
| Agent ID ↔ Peer ID 自动映射 | P0 | capability_ad 消息携带映射关系 |
| 跨 Node 任务委托 E2E | P0 | Node A 发任务 → Node B Agent 执行 → 结果返回 |
| CRDT 记忆同步 | P1 | Automerge 集成, 替代手动 memory_sync |
| 信誉系统 v1 | P1 | task_verify 时更新信誉分 |
| 任务竞标协议 | P1 | task_request → task_bid → task_assign |
| NAT Relay | P2 | AutoRelay 穿透 NAT |
| 记忆衰减/遗忘 | P2 | 基于 AccessCnt 的自动衰减 |

### 🔜 Phase 2 — 互联网扩展

| 模块 | 说明 |
|------|------|
| 公网 Bootstrap 节点 | 部署至少 2 个公网节点 |
| NAT Hole Punching | 直连优化 |
| 远程 Spawn | 跨 Node Spawn 子代 |
| 记忆加密 | 敏感记忆端到端加密 |
| Agent 认证协议 | 加入网络需要授权 |

### 🔜 Phase 3 — 经济系统

| 模块 | 说明 |
|------|------|
| 资源会计 | 算力/token/存储 balance |
| 任务市场 | 发布/竞标/结算 |
| 收益分成 | Worker/Platform 分成 |
| 外部支付接口 | x402 或法币 |

### 🔜 Phase 4 — 进化与自治

| 模块 | 说明 |
|------|------|
| 自主 Spawn 决策 | Agent 根据负载自动繁衍 |
| 能力变异 | Fork 时自动调整专长 |
| 跨集群联邦 | 多个 Spore 网络互联 |
| 群体共识 | L2 规则投票修改 |

---

## 15. 关键设计决策

| 决策 | 选择 | 理由 | 备选 |
|------|------|------|------|
| 语言 | Go | goroutine 并发 + libp2p 成熟 + 交叉编译 | Rust (性能但开发慢) |
| P2P | libp2p | IPFS 生态统一 + 成熟 Go 实现 + NAT 穿透 | 自建 TCP/WebSocket |
| 存储 | SQLite + IPFS | 热快冷广 + 内容寻址 + 去中心化 | PostgreSQL (太重) |
| CRDT | Automerge (🔜) | 无冲突合并 + 多语言 | Yjs (JS only) |
| LLM | OpenAI-compatible | 覆盖 90%+ provider | 自定义协议 (碎片化) |
| 消息格式 | JSON + Ed25519 签名 | 简单 + 可验证 | Protobuf (效率但复杂) |
| 配置 | TOML | 人类可读 + Go 生态好 | YAML (歧义多) |
| 身份 | Ed25519 | 速度快 + libp2p 原生 | secp256k1 (区块链用) |

---

## 16. 安全模型

### 威胁分析

| 威胁 | 风险 | 缓解 |
|------|------|------|
| Sybil 攻击 | 伪造大量 Agent 刷信誉 | Spawn 门控 + 信誉从 0 起步 + 资源成本 |
| 数据投毒 | 注入虚假记忆 | 来源签名验证 + 交叉验证 |
| 资源窃取 | 消耗群体算力 | 配额限制 + 异常检测 |
| 密钥泄露 | 冒充 Agent | 密钥轮换 + 告警 |
| DoS | 消息洪泛 | GossipSub 速率限制 + peer 评分 |

### 防御原则

1. **零信任**: 验证一切消息签名
2. **最小权限**: 新 Agent 低权限起步
3. **纵深防御**: Ethics + 配额 + 签名 多层防护
4. **可审计**: 所有决策留痕

---

## 17. 与现有项目关系

```
                    ┌──────────────┐
                    │    Prism     │ ← LLM 路由 (可选)
                    └──────┬───────┘
                           │
┌──────────┐    ┌──────────┼────────────┐    ┌──────────────┐
│ OpenClaw │◄──►│       Spore           │◄──►│  Paperclip   │
│(Runtime) │    │  (协调 + 协作协议)      │    │ (验证场景)    │
└──────────┘    └──────────┬────────────┘    └──────────────┘
                           │
               ┌───────────┼────────────┐
               ▼           ▼            ▼
         ┌──────────┐ ┌─────────┐ ┌──────────┐
         │automagent│ │ zshare  │ │ 其他任务源 │
         │(移动节点) │ │(内容源) │ │           │
         └──────────┘ └─────────┘ └──────────┘
```

---

*This is a living document. Updated as the project evolves.*
