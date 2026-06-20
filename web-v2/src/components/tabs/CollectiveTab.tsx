import type { SwarmState } from '../../lib/types'
import { EmptyState, PageHeader } from '../primitives'

export function CollectiveTab({ state }: { state: SwarmState | null }) {
  void state
  return (
    <div>
      <PageHeader
        title="Collective"
        description="Multi-agent emergent properties: shared beliefs, group consensus, peer reputation."
      />
      <EmptyState
        icon="🧠"
        title="Collective view coming soon"
        description="Per-agent collective state lives at /api/agents/<name>/collective. Aggregation UI in progress."
      />
    </div>
  )
}
