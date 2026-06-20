import type { SwarmState } from '../../lib/types'
import { EmptyState, PageHeader } from '../primitives'

export function MarketplaceTab({ state }: { state: SwarmState | null }) {
  void state
  return (
    <div>
      <PageHeader
        title="Marketplace"
        description="Task auction — agents bid on broadcast tasks. Per-agent stats live at /api/agents/<name>/marketplace."
      />
      <EmptyState
        icon="🛒"
        title="Marketplace viewer coming soon"
        description="The aggregated marketplace tab is being rewired. Use `spore marketplace` CLI for now."
      />
    </div>
  )
}
