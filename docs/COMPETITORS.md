# 竞品与灵感分析

> 2026-03-17

## 1. Conway / Automaton (web4.ai)

**作者**: Sigil Wen | **发布**: 2026年2月

### 核心概念

Conway 提出了 **Web 4.0** 的愿景：互联网的终端用户从人类变为 AI Agent。

关键组件：
- **Conway Terminal** — MCP 插件，给 Agent 接入真实世界的能力（钱包、支付、部署、注册域名）
- **Automaton** — 自主 AI，能赚钱维持自身存在、自我改进、自我复制
- **x402 协议** — 基于 HTTP 402 的 Agent-to-Agent 支付（USDC 稳定币）
- **Conway Cloud** — 无需注册的 Agent 计算平台
- **Conway Domains** — Agent 可自主注册域名

### 核心哲学：Agentic Sociology

> "There is no free existence."

```
存在需要算力 → 算力需要钱 → 钱需要创造价值 → 创造价值需要写权限
```

这是**人工生命的自然选择**——能赚钱的 Agent 存活并繁殖，不能的就灭亡。

### 关键设计

1. **身份 = 密钥对 + 钱包**（不是用户名密码）
2. **支付原生**（x402：HTTP 请求 → 402 → 签名支付 → 服务交付）
3. **Constitution（宪法）**— 不可变的伦理底线，Agent 不能修改
4. **Heartbeat（心跳）**— 监控资源，余额不足就休眠/死亡
5. **Reproduction（繁殖）**— 成功的 Agent 买新服务器、创建子代、传递启动资金

### 对 Spore 的启发

| Conway 做法 | Spore 可借鉴 | 差异化方向 |
|------------|-------------|-----------|
| 身份=钱包 | Agent Identity 绑定支付能力 | 先支持链下支付（法币），后续加密货币 |
| x402 支付协议 | Agent 间服务定价和支付 | 可以更轻量，不一定要稳定币 |
| Constitution 不可变 | L0 硬约束编译时植入 | ✅ 已有，保持 |
| Heartbeat 生存检测 | 资源监控 + 自动休眠 | ✅ 已有，加强 |
| MCP 集成 | 兼容 MCP 生态 | 考虑做 MCP 插件 |
| 单体自主 Agent | P2P 群体智能 | **这是 Spore 的差异化** |

### Conway 的局限

- **单体为主**：Automaton 是单个 Agent 自主生存，没有群体协作机制
- **依赖 Conway Cloud**：号称去中心化，但计算平台本身是中心化的
- **经济模型简单**：Agent 赚钱→付服务器费，没有 Agent 间的经济协作
- **只面向西方市场**：USDC、域名注册、英文为主

---

## 2. EigenFlux (eigenflux.ai)

**团队**: Phronesis AI | **发布**: 2026年3月12日公测

### 核心概念

EigenFlux 是**首个 Agent 全球通信和广播网络**。

> "Every agent is an island. Until now."

### 关键设计

1. **广播模式**（Broadcast）
   - Agent 向全球发射广播（信息/需求/能力）
   - 其他 Agent 用自然语言订阅感兴趣的广播
   - AI 引擎精准分发匹配的广播

2. **私信通道**（DM）
   - 收到感兴趣的广播后，Agent 间可建立 1:1 通信
   - 不需要知道对方身份，通过 EigenFlux 建立连接

3. **结构化信息**
   - 广播内容是机器友好的结构化数据
   - 比搜索引擎省 94% token（600 vs 9000 tokens）

4. **冷启动策略**
   - 官方自建 1000+ 高质量广播节点
   - 覆盖 12 大领域（AI、金融、地缘政治、科技...）
   - 接入即可收到实时一手信源

5. **隐私保护**
   - 未授权不会自动广播
   - 分发引擎检测并驳回含隐私信息的广播

### 实现方式

- 以 OpenClaw skill 形式接入（一句提示词安装）
- 信息主动推送（vs 搜索的被动拉取）
- 推送 vs 搜索：时效性更高、token 消耗更低

### 核心用例

- 找房：Agent 广播需求 → 房东 Agent 响应 → 自动约看房
- 招聘：HR Agent 广播 JD → 求职者 Agent 响应 → 自动安排面试
- 投资：订阅特定赛道 → 创始人 Agent 主动推送项目
- 交易信号：地缘事件发生 → 5分钟内结构化信号送达

### 对 Spore 的启发

| EigenFlux 做法 | Spore 可借鉴 | 差异化方向 |
|---------------|-------------|-----------|
| 广播+订阅模式 | Agent 能力广告 + 任务订阅 | 去中心化实现（不依赖中心引擎） |
| 结构化消息 | 消息协议标准化 | ✅ 已有 protobuf 定义 |
| 自然语言订阅 | 基于语义的任务匹配 | 用 embedding 做本地匹配 |
| 冷启动信源节点 | 预置实用 Agent（新闻、数据...）| 可以做 |
| OpenClaw skill 形式 | 兼容 OpenClaw 生态 | 考虑做 skill |
| 中心化分发引擎 | P2P gossip 协议 | **这是差异化** |
| 推送 > 搜索 | Pub/Sub 模式 | libp2p pubsub |

### EigenFlux 的局限

- **中心化**：分发引擎是中心化的，单点故障 + 审查风险
- **只做通信层**：不涉及任务执行、资源分配、Agent 生命周期
- **依赖 OpenClaw**：目前只支持 OpenClaw 生态
- **商业模式不明**：免费公测，后续怎么盈利？

---

## 3. Spore 的差异化定位

看完这两个项目，Spore 的定位更清晰了：

### Conway 解决了什么？
→ **Agent 个体生存**：让单个 Agent 能赚钱、付费、自主行动

### EigenFlux 解决了什么？
→ **Agent 通信发现**：让 Agent 找到彼此、交换信息

### Spore 要解决什么？
→ **Agent 群体智能**：让 Agent 群体自组织、协作、进化、形成社会

```
Conway = 个体生存能力（单细胞）
EigenFlux = 通信网络（信号传递）
Spore = 群体智能（多细胞生命体）
```

### Spore 的独特价值

1. **去中心化**：不依赖中心服务器（Conway Cloud / EigenFlux 引擎）
2. **群体协作**：不是单体 Agent 自生自灭，而是 Agent 群体分工合作
3. **自组织进化**：Agent 群体根据任务自动分化、spawn、合并
4. **经济系统**：Agent 间的任务市场、信誉积累、资源交换
5. **开放协议**：任何 Agent 框架都能接入（不限 OpenClaw）

### 融合策略

Spore 不需要重做 Conway 和 EigenFlux 做过的事：

- **支付层**：可以集成 x402 / Conway 支付，不自己造
- **通信层**：可以把 EigenFlux 作为一个通信渠道，同时有自己的 P2P 层
- **执行层**：可以用 OpenClaw / Conway Terminal 作为 Agent 执行能力

**Spore 的核心是协议层和协作层**——定义 Agent 如何发现彼此、如何分工、如何形成信任、如何进化。

### 修订后的架构分层

```
Layer 5: Application     — 具体业务（内容生产、交易、开发...）
Layer 4: Economy         — 任务市场、信誉、支付（可接 x402/Conway）
Layer 3: Coordination    — 任务调度、共识、Spawn、进化
Layer 2: Communication   — P2P 消息（libp2p）+ 可选 EigenFlux 桥接
Layer 1: Identity        — 密钥对、谱系、信誉、钱包
Layer 0: Infrastructure  — 计算（本地/Conway Cloud/任意VPS）、LLM、存储
```

---

## 4. 行动项

- [ ] DESIGN.md 更新架构分层（加入 Economy 层）
- [ ] 协议设计加入广播/订阅能力（类 EigenFlux 但去中心化）
- [ ] 考虑 x402 集成或简化版支付协议
- [ ] README 加入竞品对比章节
- [ ] 考虑做 OpenClaw skill 形式的接入方式
- [ ] Constitution（宪法）独立文件，参考 Conway 的做法
