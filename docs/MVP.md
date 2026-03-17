# Spore MVP — Phase 1 Implementation Plan

> 目标：单机多 Agent 运行时，能跑起来、能协作、能 spawn

## 里程碑

### M1: 骨架 (Week 1)
- [ ] Go 项目初始化（go mod, 目录结构）
- [ ] Agent 配置文件格式（TOML）
- [ ] Agent Identity（Ed25519 密钥对生成）
- [ ] CLI 基础命令：`spore init`, `spore version`

### M2: 单 Agent 运行 (Week 2)
- [ ] Agent 主循环（Observe → Think → Act → Reflect）
- [ ] LLM Provider 抽象层（OpenAI-compatible）
- [ ] 本地记忆存储（SQLite）
- [ ] CLI：`spore run`, `spore ps`

### M3: 多 Agent 通信 (Week 3)
- [ ] 本地消息总线（Unix socket / gRPC）
- [ ] 消息协议实现（task_request/response）
- [ ] Agent 发现（本地注册表）
- [ ] CLI：`spore task`, `spore send`

### M4: Spawn 机制 (Week 4)
- [ ] Clone spawn（复制配置 + 记忆快照）
- [ ] Fork spawn（继承 + 修改）
- [ ] 资源配额管理
- [ ] CLI：`spore spawn`, `spore kill`

### M5: 协作验证 (Week 5-6)
- [ ] 任务拆分和委托
- [ ] 结果验证
- [ ] 基础信誉计分
- [ ] 一个端到端 demo：多 Agent 协作完成内容生产任务

## 项目结构

```
spore/
├── cmd/
│   └── spore/
│       └── main.go              # CLI 入口
├── internal/
│   ├── agent/
│   │   ├── agent.go             # Agent 核心
│   │   ├── identity.go          # 身份管理
│   │   ├── lifecycle.go         # 生命周期
│   │   └── config.go            # 配置
│   ├── engine/
│   │   ├── task.go              # 任务引擎
│   │   ├── planner.go           # 规划器
│   │   ├── executor.go          # 执行器
│   │   └── reflector.go         # 反思器
│   ├── memory/
│   │   ├── store.go             # 记忆存储接口
│   │   ├── sqlite.go            # SQLite 实现
│   │   └── crdt.go              # CRDT 同步（Phase 2）
│   ├── network/
│   │   ├── bus.go               # 消息总线接口
│   │   ├── local.go             # 本地实现
│   │   └── p2p.go               # libp2p 实现（Phase 2）
│   ├── llm/
│   │   ├── provider.go          # LLM Provider 接口
│   │   ├── openai.go            # OpenAI 兼容
│   │   └── router.go            # 模型路由
│   ├── spawner/
│   │   ├── spawner.go           # Spawn 管理
│   │   ├── clone.go             # 克隆
│   │   └── fork.go              # 分叉
│   ├── ethics/
│   │   ├── engine.go            # 伦理引擎
│   │   ├── rules.go             # 规则定义
│   │   └── audit.go             # 审计日志
│   └── protocol/
│       ├── message.go           # 消息格式
│       └── task.go              # 任务协议
├── pkg/
│   └── sdk/                     # 外部 SDK（供第三方集成）
├── configs/
│   └── default.toml             # 默认配置
├── docs/
│   ├── DESIGN.md
│   ├── ORIGIN.md
│   └── PROTOCOL.md
├── examples/
│   └── content-swarm/           # Demo: 多 Agent 内容生产
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── Makefile
```

## 技术决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 语言 | Go | goroutine 天然适合多 Agent，交叉编译方便分发 |
| 通信（Phase 1）| gRPC | 类型安全，性能好，后续容易迁移到 P2P |
| 通信（Phase 2）| libp2p | 去中心化标准，NAT 穿透，DHT |
| 存储 | SQLite | 嵌入式，零依赖，够用 |
| 记忆同步 | Automerge | 成熟的 CRDT 实现 |
| LLM 接口 | OpenAI-compatible | 覆盖 90% 的模型 provider |
| 配置 | TOML | 人类可读，Go 生态支持好 |
| CLI | cobra | Go CLI 标准 |

## 第一个 Demo 场景

**内容生产 Swarm**（对接 zshare）：

```
[Coordinator Agent]
    ├── 分配来源给采集 Agent
    │
[Collector Agent × 3]
    ├── 分别采集 GitHub Trending / X / HN
    ├── 去重，提取链接和描述
    │
[Writer Agent]
    ├── 接收原始数据
    ├── 生成标题、摘要、标签
    │
[Publisher Agent]
    ├── 调用 zshare API 发布
    ├── 报告结果
    │
[Coordinator Agent]
    └── 汇总、记录、调整策略
```

这个 demo 直接对接 zshare，验证 Spore 的同时给公司产出内容。一举两得。
