import type { SwarmState } from '../../lib/types'
import { EmptyState, PageHeader } from '../primitives'

export function CatalogTab({ state }: { state: SwarmState | null }) {
  void state
  return (
    <div>
      <PageHeader
        title="Skill Catalog"
        description="Browse and install skills from the swarm catalog. Per-agent at /api/agents/<name>/catalog."
      />
      <EmptyState icon="📚" title="Catalog browser coming soon" />
    </div>
  )
}
