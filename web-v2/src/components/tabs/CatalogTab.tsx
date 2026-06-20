import { useEffect, useMemo, useState } from 'react'
import type { SwarmState } from '../../lib/types'
import { Badge, Button, Card, EmptyState, Input, PageHeader } from '../primitives'

interface CatalogEntry {
  name: string
  description?: string
  category?: string
  origin?: string
  generation: number
  ipfs_cid?: string
  provider_id: string
  provider_name?: string
  reputation?: number
  seen_at?: number
  [key: string]: unknown
}

interface CatalogStats {
  unique_skills?: number
  total_providers?: number
  with_cid?: number
}

interface CatalogResponse {
  stats?: CatalogStats
  unique?: CatalogEntry[]
  results?: CatalogEntry[]
}

export function CatalogTab({ state }: { state: SwarmState | null }) {
  const agents = state?.agents ?? []
  const [selectedAgent, setSelectedAgent] = useState<string>('')
  const [query, setQuery] = useState('')
  const [installableOnly, setInstallableOnly] = useState(true)
  const [data, setData] = useState<CatalogResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [installing, setInstalling] = useState<string | null>(null)
  const [flash, setFlash] = useState<{ tone: 'green' | 'red'; msg: string } | null>(null)

  // Default to first agent on load
  useEffect(() => {
    if (!selectedAgent && agents.length > 0) {
      setSelectedAgent(agents[0].name)
    }
  }, [agents, selectedAgent])

  // Fetch catalog whenever filters change
  useEffect(() => {
    if (!selectedAgent) return
    setLoading(true)
    setError(null)
    const params = new URLSearchParams()
    if (query) params.set('q', query)
    if (installableOnly) params.set('installable', 'true')
    const qs = params.toString()
    const url = `/api/agents/${encodeURIComponent(selectedAgent)}/catalog${qs ? `?${qs}` : ''}`

    let cancelled = false
    fetch(url)
      .then((r) => r.json())
      .then((d: CatalogResponse) => {
        if (!cancelled) setData(d)
      })
      .catch((e) => {
        if (!cancelled) setError(String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [selectedAgent, query, installableOnly])

  async function install(skillName: string) {
    if (!selectedAgent) return
    setInstalling(skillName)
    setFlash(null)
    try {
      const res = await fetch(`/api/agents/${encodeURIComponent(selectedAgent)}/catalog`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ skill_name: skillName }),
      })
      if (!res.ok) {
        const text = await res.text()
        setFlash({ tone: 'red', msg: `Install failed: ${text || res.status}` })
        return
      }
      setFlash({ tone: 'green', msg: `Installed ${skillName} into ${selectedAgent}` })
    } catch (e) {
      setFlash({ tone: 'red', msg: `Network error: ${e instanceof Error ? e.message : e}` })
    } finally {
      setInstalling(null)
    }
  }

  const entries = useMemo(() => data?.unique ?? data?.results ?? [], [data])
  const stats = data?.stats

  return (
    <div>
      <PageHeader
        title="Skill Catalog"
        description="Browse skills broadcast across the swarm. Install copies the IPFS-stored skill into the selected agent's SkillFS."
      />

      {/* Controls */}
      <Card className="mb-4">
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center gap-1.5">
            <span className="text-[11px] uppercase tracking-wider text-[var(--color-muted)]">
              Agent
            </span>
            <select
              className="h-8 rounded-md border border-[var(--color-border)] bg-[var(--color-bg)] px-2 text-[12px] focus:border-[var(--color-accent)] focus:outline-none"
              value={selectedAgent}
              onChange={(e) => setSelectedAgent(e.target.value)}
            >
              {agents.length === 0 ? <option value="">(no agents)</option> : null}
              {agents.map((a) => (
                <option key={a.name} value={a.name}>
                  {a.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex-1 min-w-[160px]">
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search skill name / description"
            />
          </div>
          <label className="flex items-center gap-1.5 text-[11px] text-[var(--color-dim)]">
            <input
              type="checkbox"
              checked={installableOnly}
              onChange={(e) => setInstallableOnly(e.target.checked)}
              className="accent-[var(--color-accent)]"
            />
            Installable only (has IPFS CID)
          </label>
        </div>
        {flash ? (
          <div className="mt-2 text-[11px]">
            <Badge tone={flash.tone}>{flash.msg}</Badge>
          </div>
        ) : null}
      </Card>

      {/* Stats strip */}
      {stats ? (
        <div className="mb-4 grid grid-cols-3 gap-2">
          <Card className="!p-3">
            <div className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">
              Unique skills
            </div>
            <div className="mt-0.5 text-lg font-semibold">{stats.unique_skills ?? 0}</div>
          </Card>
          <Card className="!p-3">
            <div className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">
              Providers
            </div>
            <div className="mt-0.5 text-lg font-semibold">{stats.total_providers ?? 0}</div>
          </Card>
          <Card className="!p-3">
            <div className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">
              With IPFS CID
            </div>
            <div className="mt-0.5 text-lg font-semibold">{stats.with_cid ?? 0}</div>
          </Card>
        </div>
      ) : null}

      {/* Results */}
      {error ? (
        <EmptyState icon="⚠️" title="Failed to load catalog" description={error} />
      ) : loading && !data ? (
        <EmptyState icon="…" title="Loading catalog" />
      ) : entries.length === 0 ? (
        <EmptyState
          icon="📚"
          title={
            installableOnly
              ? 'No installable skills broadcast yet'
              : 'Catalog empty'
          }
          description={
            agents.length <= 1
              ? 'Skills appear here once peer agents broadcast their SkillFS via P2P. Try running multiple spore agents on the same network.'
              : 'Wait for agents to broadcast skills — happens automatically after task completion.'
          }
        />
      ) : (
        <div className="space-y-2">
          {entries.map((e) => {
            const installable = !!e.ipfs_cid
            return (
              <Card
                key={`${e.name}::${e.provider_id}`}
                className="flex items-start justify-between gap-3 !py-3"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <div className="truncate text-[13px] font-medium">{e.name}</div>
                    {e.category ? <Badge tone="neutral">{e.category}</Badge> : null}
                    {e.origin ? <Badge tone="accent">{e.origin}</Badge> : null}
                    <Badge tone="neutral">gen {e.generation}</Badge>
                  </div>
                  {e.description ? (
                    <div className="mt-1 line-clamp-2 text-[12px] text-[var(--color-dim)]">
                      {e.description}
                    </div>
                  ) : null}
                  <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-[var(--color-muted)]">
                    <span>provider: {e.provider_name || e.provider_id.slice(0, 12)}</span>
                    {e.ipfs_cid ? <span>cid: {e.ipfs_cid.slice(0, 14)}…</span> : null}
                    {typeof e.reputation === 'number' ? (
                      <span>rep: {e.reputation.toFixed(2)}</span>
                    ) : null}
                  </div>
                </div>
                <Button
                  variant={installable ? 'primary' : 'secondary'}
                  size="sm"
                  disabled={!installable || installing === e.name}
                  onClick={() => install(e.name)}
                  title={installable ? 'Install into selected agent' : 'No CID — cannot install'}
                >
                  {installing === e.name ? 'Installing…' : installable ? 'Install' : 'No CID'}
                </Button>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
