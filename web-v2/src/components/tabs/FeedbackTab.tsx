import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'

export function FeedbackTab({ state }: { state: SwarmState | null }) {
  const fb = state?.feedback
  const recent = fb?.recent ?? []
  const helpWanted = fb?.help_wanted ?? []

  return (
    <div>
      <PageHeader title="Feedback" description="Human feedback and help-wanted requests across the swarm." />

      {helpWanted.length > 0 ? (
        <div className="mb-6">
          <h2 className="mb-3 text-sm font-semibold text-[var(--color-dim)]">Help wanted ({helpWanted.length})</h2>
          <div className="space-y-2">
            {helpWanted.map((h, i) => (
              <Card key={i} className="!py-3 border-[var(--color-amber)]/30">
                <pre className="whitespace-pre-wrap break-words text-[12px] text-[var(--color-fg)]">
                  {JSON.stringify(h, null, 2)}
                </pre>
              </Card>
            ))}
          </div>
        </div>
      ) : null}

      <h2 className="mb-3 text-sm font-semibold text-[var(--color-dim)]">Recent feedback</h2>
      {recent.length === 0 ? (
        <EmptyState icon="💬" title="No feedback yet" />
      ) : (
        <div className="space-y-2">
          {recent.map((r, i) => (
            <Card key={i} className="!py-3">
              <pre className="whitespace-pre-wrap break-words text-[12px] text-[var(--color-fg)]">
                {JSON.stringify(r, null, 2)}
              </pre>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
