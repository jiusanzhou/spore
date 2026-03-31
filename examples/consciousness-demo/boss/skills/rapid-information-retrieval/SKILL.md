---
name: rapid-information-retrieval
description: Quickly retrieves specific factual information using prioritized sources with built-in timeouts and fallbacks
version: 0.1.0
origin: derived
generation: 1
parent_ids:
    - efficient-research-synthesis
source_task: 2e39a5a3
x-content-hash: e848ede35b83e1a251d6c1c9a06a548221d96972d862e31ad9a50d2f1d34766c
x-ipfs-cid: bafkreihreczcyhizkzw4cul2xle766kjlkqhvsrar6hqsga5r4miqs52ym
created_at: "2026-03-30T17:07:25+08:00"
updated_at: "2026-03-30T17:07:25+08:00"
---

# rapid-information-retrieval

## When to Use
Use this skill when you need to quickly obtain specific, factual information (weather, current events, technical specifications, etc.) rather than conducting deep research or analysis. Ideal for straightforward queries that require current or precise data.

## Procedure
1. **Quick Classification** (10 seconds): Determine if this is a simple factual query or complex research need
2. **Source Prioritization** (20 seconds): Identify the 2-3 most reliable and accessible sources for this type of information
3. **Rapid Access** (60 seconds max): Use direct API calls, official databases, or authoritative websites first
4. **Timeout Strategy**: If primary sources fail within 60 seconds, immediately switch to cached knowledge or alternative sources
5. **Structured Response** (30 seconds): Present findings using clear headings, bullet points, and essential details only
6. **Fallback Protocol**: If all external sources fail, clearly state limitations and provide best available knowledge with timestamp

## Pitfalls
- Spending more than 2 minutes total on simple factual queries
- Trying multiple unreliable sources instead of focusing on authoritative ones
- Over-researching when basic information suffices
- Not implementing clear timeouts for source access attempts
- Failing to communicate when information may be outdated

## Verification
- Total response time under 2 minutes for simple queries
- Successfully identifies most appropriate sources within 20 seconds
- Has clear fallback when primary sources are unavailable
- Provides appropriately detailed response without over-researching
