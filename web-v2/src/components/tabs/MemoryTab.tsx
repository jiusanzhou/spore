import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { formatBytes, formatRelative, shortHash } from '../../lib/utils'

export function MemoryTab({ state }: { state: SwarmState | null }) {
  const items = state?.content?.items ?? []
  const stats = state?.content?.stats as Record<string, unknown> | undefined

  const totalBytes = Number(stats?.total_bytes ?? items.reduce((a, c) => a + Number(c.size ?? 0), 0))
  const persistent = Boolean(stats?.persistent)
  const cached = Number(stats?.cached ?? 0)

  return (
    <div>
      <PageHeader
        title="Collective Memory"
        description="Content-addressed (CID) storage shared across the swarm — IPFS or local cache."
      />

      <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Items" value={items.length} />
        <StatCard label="Bytes" value={formatBytes(totalBytes)} />
        <StatCard label="Persistent" value={persistent ? 'Yes' : 'No'} />
        <StatCard label="Cached" value={cached} />
      </div>

      {items.length === 0 ? (
        <EmptyState icon="💾" title="No content stored yet" />
      ) : (
        <Card className="!p-0 overflow-hidden">
          <table className="w-full text-[12px]">
            <thead className="bg-[var(--color-surface)] text-[10px] uppercase tracking-wider text-[var(--color-muted)]">
              <tr>
                <th className="px-3 py-2 text-left">Type</th>
                <th className="px-3 py-2 text-left">Summary</th>
                <th className="px-3 py-2 text-left">CID</th>
                <th className="px-3 py-2 text-left">IPFS</th>
                <th className="px-3 py-2 text-right">Size</th>
                <th className="px-3 py-2 text-right">When</th>
              </tr>
            </thead>
            <tbody>
              {items.map((c, i) => (
                <tr
                  key={(c.cid as string) ?? i}
                  className="border-t border-[var(--color-border)] hover:bg-[var(--color-surface)]"
                >
                  <td className="px-3 py-2">
                    <span className="rounded-full bg-[var(--color-cyan-soft)] px-2 py-0.5 text-[10px] text-[var(--color-cyan)]">
                      {(c.type as string) ?? '—'}
                    </span>
                  </td>
                  <td className="max-w-[280px] truncate px-3 py-2">{(c.summary as string) ?? '—'}</td>
                  <td className="px-3 py-2 font-mono text-[10px] text-[var(--color-dim)]">
                    {shortHash(c.cid as string, 12)}
                  </td>
                  <td className="px-3 py-2 font-mono text-[10px] text-[var(--color-dim)]">
                    {shortHash(c.ipfs_cid as string, 12)}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">{c.size ? formatBytes(Number(c.size)) : '—'}</td>
                  <td className="px-3 py-2 text-right text-[var(--color-muted)]">
                    {formatRelative(c.timestamp as number | undefined)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </div>
  )
}

function StatCard({ label, value }: { label: string; value: string | number }) {
  return (
    <Card>
      <div className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </Card>
  )
}
