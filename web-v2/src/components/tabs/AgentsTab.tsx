import type { AgentInfo, SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { DrivesRadar } from '../DrivesRadar'
import { cn } from '../../lib/utils'

export function AgentsTab({ state }: { state: SwarmState | null }) {
  const agents = state?.agents ?? []

  return (
    <div>
      <PageHeader
        title="Agents"
        description="Active agents in this swarm. Each agent shows its intrinsic drives, role, and current state."
      />
      {agents.length === 0 ? (
        <EmptyState
          icon="🦠"
          title="No agents yet"
          description="Start an agent with `spore run` or join an existing swarm via `spore swarm`."
        />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-4">
          {agents.map((a, i) => (
            <AgentCard key={a.id ?? a.name ?? i} agent={a} />
          ))}
        </div>
      )}
    </div>
  )
}

function AgentCard({ agent }: { agent: AgentInfo }) {
  const status = (agent.status ?? 'idle').toString().toLowerCase()
  const role = agent.role ?? 'worker'
  const mood = agent.awareness?.mood ?? '—'
  const fitness = agent.awareness?.fitness
  const balance = agent.balance ?? 0

  return (
    <Card className="flex flex-col gap-3">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{agent.name}</div>
          <div className="text-[11px] text-[var(--color-muted)]">{role}</div>
        </div>
        <StatusBadge status={status} />
      </div>

      <DrivesRadar drives={agent.drives ?? {}} size={180} />

      <div className="grid grid-cols-3 gap-2 text-center">
        <Metric label="Mood" value={mood} />
        <Metric
          label="Fitness"
          value={fitness != null ? Number(fitness).toFixed(2) : '—'}
        />
        <Metric label="Credits" value={balance.toFixed(2)} />
      </div>

      {agent.skills?.length ? (
        <div className="flex flex-wrap gap-1">
          {agent.skills.slice(0, 6).map((s) => (
            <span
              key={s}
              className="rounded-full bg-[var(--color-surface)] px-2 py-0.5 text-[10px] text-[var(--color-dim)]"
            >
              {s}
            </span>
          ))}
          {agent.skills.length > 6 ? (
            <span className="px-1 text-[10px] text-[var(--color-muted)]">+{agent.skills.length - 6}</span>
          ) : null}
        </div>
      ) : null}
    </Card>
  )
}

function StatusBadge({ status }: { status: string }) {
  const palette: Record<string, string> = {
    idle: 'bg-[var(--color-cyan-soft)] text-[var(--color-cyan)]',
    busy: 'bg-[var(--color-amber-soft)] text-[var(--color-amber)]',
    completed: 'bg-[var(--color-green-soft)] text-[var(--color-green)]',
    failed: 'bg-[var(--color-red-soft)] text-[var(--color-red)]',
    starting: 'bg-[var(--color-accent-soft)] text-[var(--color-accent)]',
    stopped: 'bg-[var(--color-card)] text-[var(--color-muted)]',
  }
  const cls = palette[status] ?? 'bg-[var(--color-card)] text-[var(--color-dim)]'
  return (
    <span className={cn('shrink-0 rounded-full px-2 py-0.5 text-[10px] uppercase tracking-wider', cls)}>
      {status}
    </span>
  )
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-[var(--radius-sm)] bg-[var(--color-surface)] py-1.5">
      <div className="text-[12px] font-semibold tabular-nums">{value}</div>
      <div className="text-[9px] uppercase tracking-wider text-[var(--color-muted)]">{label}</div>
    </div>
  )
}
