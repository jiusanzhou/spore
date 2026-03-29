# Spore TODO — Self-Evolution Roadmap

> 核心思路：单体自进化作为每个 agent 的内循环，群体协作是外循环。

## 1. 自进化循环 (Self-Evolution Pipeline) ✅ DONE

**设计**: 每 8 小时读自身状态 → 规划改进 → 实现 → 验证 → 通过就应用，失败 revert。

**已实现**:
- [x] `spore evolve` 命令：agent 审视自己的 skills，用 LLM 分析哪些可以优化
- [x] 测试门控：进化出的 skill 改动必须通过验证任务才合并到 skill store
- [x] 进化日志：每次进化记录 before/after diff + 原因
- [x] 自动进化循环：AutoEvolver 集成到 agent Run() 主循环

## 2. 记忆时间衰减 + Synthesis ✅ DONE

**设计**: append-only JSONL 原始记录 + 定期 synthesis job 压缩为 active memory。

**已实现**:
- [x] 记忆分层：raw log (append-only) → active memory (synthesis 压缩)
- [x] 时间加权：近期保留全文，旧的按主题合并
- [x] 定期 synthesis：agent 定时自动整理记忆，清理过期/低价值内容
- [x] 跨 agent synthesis：CollectiveSynthesizer — 发布 digest → IPFS → GossipSub → peer 融合 → collective_learnings.md

## 3. 社区/人类反馈通道

**设计**: 让人类参与 agent 进化方向的决策。

**待做**:
- [ ] 蚁群市场加入人类投票权重（人类 upvote 的任务优先级更高）
- [ ] agent 遇到瓶颈时主动发 "help wanted" 到人类可见的渠道
- [ ] 进化方向由社区投票 + agent 自评共同决定

## 4. 预设 Skill 种子模板 ✅ DONE

**已实现**:
- [x] 内置 5 个 seed skills（self-assess、collaborate、evolve、communicate、research）
- [x] Skill 格式带 YAML frontmatter（触发条件、优先级、依赖）
- [x] Skill marketplace：SkillCatalog 聚合 ServiceAd + CID，Browse/Install via IPFS

## 5. 公开进化日志 ✅ DONE

**已实现**:
- [x] 每个 agent 维护 evolution log（时间、任务、改进、结果）
- [ ] Swarm 级别的 changelog（群体层面的能力变化）
- [x] Dashboard 展示进化历史时间线（Journal tab）

## 6. SkillFS — 文件系统优先技能存储 ✅ DONE

**已实现**:
- [x] Source of truth: skills/<name>/SKILL.md (YAML frontmatter + markdown body)
- [x] IPFS content addressing: 写入 → SHA-256 → publish → CID in frontmatter
- [x] SQLite 降级为索引 (metrics + versions + analyses)
- [x] 渐进式加载: index.yaml → SKILL.md → references/
- [x] 跨 agent 学习: ImportFromCID
- [x] 自动迁移: legacy skills.db → SkillFS

---

## 剩余待做

- [ ] Swarm changelog — 群体能力变化记录
- [ ] 人类反馈通道 — 投票权重 + help wanted + 社区进化决策
- [ ] 进程守护 — daemon 模式，崩溃自动重启
- [ ] dependabot 漏洞修复 (1 high)

---

## Spore 核心优势

保持并强化这些差异化：
- ✅ P2P 去中心化 (libp2p)
- ✅ 多 agent 群体协作
- ✅ Token 经济模型
- ✅ 蚁群式任务市场
- ✅ IPFS 内容寻址共享
- ✅ 自我意识/情绪系统
- ✅ 多 runtime 适配 (Codex/Claude Code/OpenClaw/ABox/HTTP)
- ✅ NAT 穿透 + 跨网络组网
- ✅ SkillFS — SKILL.md 文件系统 + IPFS 内容寻址
- ✅ 集体记忆 — 跨 agent 记忆融合
- ✅ Skill Catalog — 去中心化技能市场

**核心思路**: 单体自进化 pipeline 作为每个 agent 的**内循环**，群体协作是**外循环**。
