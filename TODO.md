# Spore TODO — Lessons from yoyo-evolve

> Ref: https://github.com/yologdev/yoyo-evolve
> yoyo 是单体自进化 coding agent（Rust，31K 行），核心卖点是无人工干预的自我进化循环。
> Spore 是群体协作进化，以下是可吸收的设计。

## 1. 自进化循环 (Self-Evolution Pipeline)

**来源**: yoyo 的 `evolve.sh` — 每 8 小时读自己源码 → 规划改进 → 实现 → 测试 → 通过就 commit，失败 revert。

**Spore 可做**:
- [ ] `spore evolve` 命令：agent 审视自己的 skills，用 LLM 分析哪些可以优化
- [ ] 测试门控：进化出的 skill 改动必须通过验证任务才合并到 skill store
- [ ] 进化日志：每次进化记录 before/after diff + 原因，类似 yoyo 的 JOURNAL.md

**现状**: Spore 有任务后 skill evolution（FIX/DERIVE/CAPTURE），但不会改进自身代码/配置。

## 2. 记忆时间衰减 + Synthesis

**来源**: yoyo 的 `memory/` 目录 — append-only JSONL 原始记录 + 每日 synthesis job 压缩为 active memory。

**Spore 可做**:
- [ ] 记忆分层：raw log (append-only) → active memory (synthesis 压缩)
- [ ] 时间加权：近期保留全文，旧的按主题合并
- [ ] 定期 synthesis：agent idle 时自动整理记忆，清理过期/低价值内容
- [ ] 跨 agent synthesis：群体层面的记忆合成（Spore 独有优势）

**现状**: SQLite + IPFS Markdown 存储，无时间衰减机制。

## 3. 社区/人类反馈通道

**来源**: yoyo 用 GitHub Issues labels + 投票影响进化方向。

**Spore 可做**:
- [ ] 蚁群市场加入人类投票权重（人类 upvote 的任务优先级更高）
- [ ] agent 遇到瓶颈时主动发 "help wanted" 到人类可见的渠道
- [ ] 进化方向由社区投票 + agent 自评共同决定

**现状**: 纯 agent-to-agent 蚁群市场，无人类参与接口。

## 4. 预设 Skill 种子模板

**来源**: yoyo 有 7 个手写 skills (self-assess, evolve, communicate, social, family, release, research)，作为 agent 行为的基础模板。

**Spore 可做**:
- [ ] 内置一组 seed skills（自评、协作、进化、沟通、研究）
- [ ] Skill 格式加 YAML frontmatter（触发条件、优先级、依赖）
- [ ] Skill marketplace：agent 可以浏览/安装其他 swarm 共享的 skills

**现状**: Skills 完全由 LLM 自动生成，无预设种子。

## 5. 公开进化日志

**来源**: yoyo 的 JOURNAL.md — 每次进化的详细记录，既是 debug 工具也是营销素材。

**Spore 可做**:
- [ ] 每个 agent 维护 evolution log（时间、任务、改进、结果）
- [ ] Swarm 级别的 changelog（群体层面的能力变化）
- [ ] Dashboard 展示进化历史时间线

**现状**: 有 Dashboard 但无进化历史视图。

---

## Spore 独有优势（yoyo 没有）

保持并强化这些差异化：
- ✅ P2P 去中心化 (libp2p)
- ✅ 多 agent 群体协作
- ✅ Token 经济模型
- ✅ 蚁群式任务市场
- ✅ IPFS 内容寻址共享
- ✅ 自我意识/情绪系统
- ✅ 多 runtime 适配 (Codex/Claude Code/OpenClaw/ABox/HTTP)
- ✅ NAT 穿透 + 跨网络组网

**核心思路**: 把 yoyo 的单体自进化 pipeline 作为 Spore 每个 agent 的**内循环**，群体协作是**外循环**。
