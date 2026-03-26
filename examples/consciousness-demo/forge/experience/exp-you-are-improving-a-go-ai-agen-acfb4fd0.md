# You are improving a Go AI agent engine. The current engine.go has a think() method that builds LLM prompts by simple string concatenation. Here is the current approach:

func (e *Engine) think(ctx context.Context, task *Task, observation string) (string, error) {
  var msgs []llm.Message
  msgs = append(msgs, llm.Message{Role: "system", Content: systemPrompt})
  // ... appends task description, history, observation
  return e.llm.Chat(ctx, msgs)
}

The systemPrompt tells the LLM to use ACTION: tool_name(input) format or RESULT: final_answer format.

Problems: 1) Prompt grows unbounded with history 2) No sliding window or summarization 3) Tool descriptions are hardcoded in the system prompt string 4) No few-shot examples for output format

Write improved Go code for:
1. A PromptBuilder struct that manages context window (max 8000 chars)
2. Sliding window for step history (keep last 5 steps, summarize older ones)
3. Dynamic tool description injection from registered tools
4. Structured output format with JSON instead of regex-parsed text

Output complete, compilable Go code.

## 领域
writing / coding / documentation

## 难度
expert

## 摘要
任务「You are improving a Go AI agent engine. The current engine.go has a think() method that builds LL...」通过 builtin runtime 执行成功，耗时 112.2 秒。

## 技能上下文
- writing: 成功率 86%, 趋势 improving
- coding: 成功率 86%, 趋势 improving
- documentation: 成功率 86%, 趋势 improving

## 元数据
- Runtime: builtin
- 耗时: 112.2s
- 时间: 2026-03-26T15:03:58+08:00
