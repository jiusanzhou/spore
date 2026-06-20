import type { SwarmState } from '../../lib/types'
import { EmptyState, PageHeader } from '../primitives'

export function SynthesisTab({ state }: { state: SwarmState | null }) {
  void state
  return (
    <div>
      <PageHeader
        title="Synthesis"
        description="Cross-agent memory synthesis — merging local memories into shared knowledge."
      />
      <EmptyState icon="✨" title="Synthesis viewer coming soon" />
    </div>
  )
}
