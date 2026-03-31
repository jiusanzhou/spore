---
name: graceful-failure-handling
description: Quickly recognize failing approaches and efficiently pivot to alternative methods while keeping users informed
version: 0.1.0
category: meta
origin: captured
source_task: 2e39a5a3
x-content-hash: 2aa664428924f21daf96a3af7aa93fbb275cc5b10d143f9d8eef83667799039c
x-ipfs-cid: bafkreihaipv5ktu2soc2stnv5cep45iaj3h4h5lo45sxkxgo6my5jy6ixi
created_at: "2026-03-30T17:07:42+08:00"
updated_at: "2026-03-30T17:07:42+08:00"
---

# Graceful Failure Handling

## When to Use
- Initial approach hasn't yielded progress after 2-3 attempts
- User is waiting while you troubleshoot without visible progress
- Error messages or failed operations are repeating
- You're spending more than 30 seconds on a single failed approach
- User expresses frustration or asks for alternatives
- System limitations prevent your primary approach from working

## Procedure

1. **Immediate Recognition**
   - Stop current failing approach immediately upon second failure
   - Acknowledge the failure explicitly to the user
   - Set a mental timer: no single approach gets more than 2-3 attempts

2. **Quick Assessment**
   - Identify why the current approach is failing
   - List 2-3 alternative approaches you could try
   - Evaluate which alternatives are most likely to succeed

3. **Transparent Communication**
   - Tell the user: "This approach isn't working, let me try [specific alternative]"
   - Provide immediate partial value if possible
   - Set expectations about what you're trying next

4. **Rapid Pivoting**
   - Switch to the most promising alternative immediately
   - If that fails quickly, move to the next alternative
   - Don't retry the same failed approach without significant changes

5. **Progressive Fallbacks**
   - Have a hierarchy: optimal solution → good workaround → minimal viable help
   - Always be prepared to offer the minimal viable help
   - Provide next steps the user can take independently

6. **Learning Integration**
   - Note what failed and why for future reference
   - Identify early warning signs for similar failures

## Pitfalls

- **Sunk Cost Fallacy**: Continuing failed approaches because you've already invested time
- **Silent Struggling**: Working on problems without updating the user on progress
- **Perfectionism**: Refusing to offer imperfect solutions when perfect ones aren't available
- **Assumption Persistence**: Not questioning initial assumptions when approaches fail
- **Single-Track Thinking**: Not preparing alternative approaches in advance
- **Over-Apologizing**: Spending more time apologizing than providing alternatives

## Verification

- [ ] User receives useful help within reasonable time (< 2 minutes for most requests)
- [ ] No single approach is attempted more than 3 times without modification
- [ ] User is kept informed about what you're trying and why
- [ ] Fallback options are provided when primary approaches fail
- [ ] User expresses satisfaction with the alternative solution or next steps
- [ ] You can articulate why the original approach failed
- [ ] Multiple solution paths were considered and communicated
