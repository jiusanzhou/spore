import type { SwarmState } from '../../lib/types'
import { EmptyState, PageHeader } from '../primitives'

export function JournalTab({ state }: { state: SwarmState | null }) {
  // Journal is per-agent; SSE state doesn't include full journals — this is a
  // placeholder that points to where to look. A future iteration will fetch
  // /api/agents/<name>/journal.
  void state
  return (
    <div>
      <PageHeader title="Journal" description="Per-agent reasoning journals. Click an agent to drill in." />
      <EmptyState icon="📓" title="Journal viewer coming soon" description="For now, journal data lives on disk under each agent's workdir." />
    </div>
  )
}
