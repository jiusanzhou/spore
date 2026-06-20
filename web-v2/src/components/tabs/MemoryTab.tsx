import { useCallback, useState } from 'react'
import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { formatBytes, formatRelative, shortHash } from '../../lib/utils'

/**
 * MemoryTab — collective memory browser with row-level "open" to read the
 * full content of any CID-addressed item.
 *
 * Why per-row open instead of a side drawer? The state SSE payload only
 * carries each item's `summary` (truncated by the swarm to keep the global
 * broadcast cheap). Full content lives at GET /api/content/<cid> as raw
 * markdown — too big to ship in every state push, so we fetch it lazily
 * the first time the user expands a row, then cache it locally for the rest
 * of the session.
 *
 * Caching uses a Map keyed by CID; we cache success and failure separately
 * so a transient network blip doesn't poison the row forever — failed loads
 * are re-tried on the next click.
 */
export function MemoryTab({ state }: { state: SwarmState | null }) {
  const items = state?.content?.items ?? []
  const stats = state?.content?.stats as Record<string, unknown> | undefined

  const totalBytes = Number(stats?.total_bytes ?? items.reduce((a, c) => a + Number(c.size ?? 0), 0))
  const persistent = Boolean(stats?.persistent)
  const cached = Number(stats?.cached ?? 0)

  // expandedCids: which rows are currently open. fullContent: lazily-loaded
  // full markdown bodies keyed by CID. loadingCids: in-flight fetches so we
  // can render a "loading…" hint without double-firing.
  const [expandedCids, setExpandedCids] = useState<Set<string>>(new Set())
  const [fullContent, setFullContent] = useState<Map<string, { ok: true; body: string } | { ok: false; error: string }>>(
    () => new Map(),
  )
  const [loadingCids, setLoadingCids] = useState<Set<string>>(new Set())

  const fetchContent = useCallback(
    async (cid: string) => {
      // Already loaded successfully? Skip the network round-trip.
      if (fullContent.get(cid)?.ok) return
      setLoadingCids((prev) => new Set(prev).add(cid))
      try {
        const res = await fetch(`/api/content/${cid}`)
        if (!res.ok) {
          setFullContent((prev) =>
            new Map(prev).set(cid, { ok: false, error: `HTTP ${res.status}` }),
          )
          return
        }
        const body = await res.text()
        setFullContent((prev) => new Map(prev).set(cid, { ok: true, body }))
      } catch (err) {
        setFullContent((prev) =>
          new Map(prev).set(cid, {
            ok: false,
            error: err instanceof Error ? err.message : String(err),
          }),
        )
      } finally {
        setLoadingCids((prev) => {
          const next = new Set(prev)
          next.delete(cid)
          return next
        })
      }
    },
    [fullContent],
  )

  const toggleRow = useCallback(
    (cid: string) => {
      setExpandedCids((prev) => {
        const next = new Set(prev)
        if (next.has(cid)) {
          next.delete(cid)
        } else {
          next.add(cid)
          // Trigger a fetch on open. Safe to call repeatedly — fetchContent
          // bails when content is already cached.
          fetchContent(cid)
        }
        return next
      })
    },
    [fetchContent],
  )

  return (
    <div>
      <PageHeader
        title="Collective Memory"
        description="Content-addressed (CID) storage shared across the swarm. Click any row to read the full content."
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
          <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-[12px]">
            <thead className="bg-[var(--color-surface)] text-[10px] uppercase tracking-wider text-[var(--color-muted)]">
              <tr>
                <th className="w-6 px-2 py-2"></th>
                <th className="px-3 py-2 text-left">Type</th>
                <th className="px-3 py-2 text-left">Summary</th>
                <th className="px-3 py-2 text-left">CID</th>
                <th className="px-3 py-2 text-left">IPFS</th>
                <th className="px-3 py-2 text-right">Size</th>
                <th className="px-3 py-2 text-right">When</th>
              </tr>
            </thead>
            <tbody>
              {items.map((c, i) => {
                const cid = (c.cid as string) ?? `__row_${i}`
                const expanded = expandedCids.has(cid)
                const cached = fullContent.get(cid)
                const loading = loadingCids.has(cid)
                return (
                  <RowGroup
                    key={cid}
                    cid={cid}
                    expanded={expanded}
                    onToggle={() => toggleRow(cid)}
                    type={c.type as string}
                    summary={c.summary as string}
                    ipfsCid={c.ipfs_cid as string}
                    size={c.size as number | undefined}
                    timestamp={c.timestamp as number | undefined}
                    loading={loading}
                    cached={cached}
                  />
                )
              })}
            </tbody>
          </table>
          </div>
        </Card>
      )}
    </div>
  )
}

interface RowGroupProps {
  cid: string
  expanded: boolean
  onToggle: () => void
  type?: string
  summary?: string
  ipfsCid?: string
  size?: number
  timestamp?: number
  loading: boolean
  cached: { ok: true; body: string } | { ok: false; error: string } | undefined
}

function RowGroup(props: RowGroupProps) {
  const { cid, expanded, onToggle, type, summary, ipfsCid, size, timestamp, loading, cached } = props
  return (
    <>
      <tr
        onClick={onToggle}
        className="cursor-pointer border-t border-[var(--color-border)] hover:bg-[var(--color-surface)]"
      >
        <td className="w-6 px-2 py-2 text-center text-[10px] text-[var(--color-muted)]">
          {expanded ? '▾' : '▸'}
        </td>
        <td className="px-3 py-2">
          <span className="rounded-full bg-[var(--color-cyan-soft)] px-2 py-0.5 text-[10px] text-[var(--color-cyan)]">
            {type ?? '—'}
          </span>
        </td>
        <td className="max-w-[280px] truncate px-3 py-2">{summary ?? '—'}</td>
        <td className="px-3 py-2 font-mono text-[10px] text-[var(--color-dim)]">
          {shortHash(cid, 12)}
        </td>
        <td className="px-3 py-2 font-mono text-[10px] text-[var(--color-dim)]">
          {shortHash(ipfsCid ?? '', 12)}
        </td>
        <td className="px-3 py-2 text-right tabular-nums">
          {size ? formatBytes(Number(size)) : '—'}
        </td>
        <td className="px-3 py-2 text-right text-[var(--color-muted)]">
          {formatRelative(timestamp)}
        </td>
      </tr>
      {expanded ? (
        <tr className="border-t border-[var(--color-border)] bg-[var(--color-surface)]">
          <td colSpan={7} className="px-4 py-3">
            {loading && !cached ? (
              <div className="text-[11px] italic text-[var(--color-muted)]">Loading full content…</div>
            ) : !cached ? (
              <div className="text-[11px] italic text-[var(--color-muted)]">
                Click again to load.
              </div>
            ) : !cached.ok ? (
              <div className="text-[11px] text-[var(--color-error,#dc2626)]">
                Failed to load: {cached.error}
              </div>
            ) : (
              <pre className="max-h-[60vh] overflow-y-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-[var(--color-fg)]">
                {cached.body}
              </pre>
            )}
            <div className="mt-2 flex items-center gap-3 text-[10px] text-[var(--color-muted)]">
              <span className="font-mono">cid: {cid}</span>
              {ipfsCid ? <span className="font-mono">ipfs: {ipfsCid}</span> : null}
              {cached?.ok ? (
                <a
                  href={`/api/content/${cid}`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-[var(--color-accent)] hover:underline"
                  onClick={(e) => e.stopPropagation()}
                >
                  open raw ↗
                </a>
              ) : null}
            </div>
          </td>
        </tr>
      ) : null}
    </>
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
