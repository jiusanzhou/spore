/* API types — derived from internal/api/server.go buildStatePayload().
 *
 * Hand-typed because the Go side uses map[string]interface{} so OpenAPI
 * generation isn't trivial. Kept loose with optional fields — UI must be
 * defensive. Where we don't know exact shape, we use Record<string, unknown>.
 */

export type AgentStatus = 'idle' | 'busy' | 'completed' | 'failed' | 'starting' | 'stopped' | string

/** From swarm.AgentInfo — flat shape exposed to API. Names are lowercased by Go json tags. */
export interface AgentInfo {
  id?: string
  name: string
  role?: string
  status?: AgentStatus
  runtime?: string
  model?: string
  balance?: number
  description?: string
  /** Self-awareness vector — 0..1 scalars on each axis. */
  drives?: {
    survival?: number
    exploration?: number
    consolidation?: number
    transmission?: number
    creativity?: number
  }
  /** Subjective evaluation of own state. */
  awareness?: {
    mood?: string
    confidence?: number
    fitness?: number
  }
  skills?: string[]
  uptime_seconds?: number
  [key: string]: unknown
}

export interface SwarmStats {
  agents?: number
  tasks_completed?: number
  tasks_failed?: number
  uptime_seconds?: number
  [key: string]: unknown
}

export interface TaskLogEntry {
  id?: string
  description?: string
  agent?: string
  status?: string
  result?: string
  error?: string
  created_at?: number
  completed_at?: number
  duration_seconds?: number
  [key: string]: unknown
}

export interface ContentRef {
  cid?: string
  ipfs_cid?: string
  agent_id?: string
  type?: string
  summary?: string
  size?: number
  timestamp?: number
  [key: string]: unknown
}

export interface NetworkInfo {
  transport: 'local' | 'p2p' | string
  peers: number
}

export interface ContentBlock {
  items?: ContentRef[]
  stats?: Record<string, unknown>
}

export interface ChangelogEntry {
  id?: string
  type?: string
  agent?: string
  summary?: string
  ts?: number
  [key: string]: unknown
}

export interface FeedbackBlock {
  stats?: Record<string, unknown>
  recent?: Array<Record<string, unknown>>
  help_wanted?: Array<Record<string, unknown>>
}

export interface SwarmState {
  type?: 'state'
  agents?: AgentInfo[]
  stats?: SwarmStats
  tasks?: TaskLogEntry[]
  network?: NetworkInfo
  content?: ContentBlock
  changelog?: ChangelogEntry[]
  feedback?: FeedbackBlock
  ts?: number
}
