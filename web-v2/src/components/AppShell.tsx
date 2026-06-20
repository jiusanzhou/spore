import type { ReactNode } from 'react'
import {
  Network,
  Users,
  Store,
  BookOpen,
  Brain,
  Database,
  Sparkles,
  Activity,
  ScrollText,
  GitCommit,
  MessageSquare,
  Moon,
  Sun,
} from 'lucide-react'
import type { TabId } from '../App'
import type { SwarmState } from '../lib/types'
import { cn, formatDuration, formatRelative } from '../lib/utils'

interface NavItem {
  id: TabId
  label: string
  icon: typeof Network
  group: 'swarm' | 'memory' | 'activity'
  badge?: (s: SwarmState | null) => string | number | null
}

const NAV: NavItem[] = [
  { id: 'network', label: 'Network', icon: Network, group: 'swarm', badge: (s) => s?.network?.peers ?? null },
  { id: 'agents', label: 'Agents', icon: Users, group: 'swarm', badge: (s) => s?.agents?.length ?? null },
  { id: 'marketplace', label: 'Marketplace', icon: Store, group: 'swarm' },
  { id: 'catalog', label: 'Catalog', icon: BookOpen, group: 'swarm' },
  { id: 'collective', label: 'Collective', icon: Brain, group: 'memory' },
  { id: 'memory', label: 'Memory', icon: Database, group: 'memory', badge: (s) => s?.content?.items?.length ?? null },
  { id: 'synthesis', label: 'Synthesis', icon: Sparkles, group: 'memory' },
  { id: 'activity', label: 'Activity', icon: Activity, group: 'activity', badge: (s) => s?.tasks?.length ?? null },
  { id: 'journal', label: 'Journal', icon: ScrollText, group: 'activity' },
  { id: 'changelog', label: 'Changelog', icon: GitCommit, group: 'activity', badge: (s) => s?.changelog?.length ?? null },
  { id: 'feedback', label: 'Feedback', icon: MessageSquare, group: 'activity' },
]

const GROUP_LABELS: Record<NavItem['group'], string> = {
  swarm: 'Swarm',
  memory: 'Memory',
  activity: 'Activity',
}

export interface AppShellProps {
  state: SwarmState | null
  connected: boolean
  lastUpdate: number
  theme: 'dark' | 'light'
  onToggleTheme: () => void
  tab: TabId
  onTabChange: (id: TabId) => void
  children: ReactNode
}

export function AppShell({
  state,
  connected,
  lastUpdate,
  theme,
  onToggleTheme,
  tab,
  onTabChange,
  children,
}: AppShellProps) {
  const stats = state?.stats
  const network = state?.network

  // Group nav items
  const grouped: Record<NavItem['group'], NavItem[]> = { swarm: [], memory: [], activity: [] }
  for (const item of NAV) grouped[item.group].push(item)

  return (
    <div className="grid h-full grid-cols-[240px_1fr] grid-rows-[1fr_auto] bg-[var(--color-bg)] text-[var(--color-fg)]">
      {/* Sidebar */}
      <aside className="row-span-2 flex flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)]">
        {/* Logo */}
        <div className="flex items-center gap-3 border-b border-[var(--color-border)] px-5 py-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-sm)] bg-[var(--color-accent)] text-[var(--color-accent-fg)]">
            <span className="text-base">🦠</span>
          </div>
          <div className="flex flex-col">
            <span className="text-sm font-semibold leading-tight tracking-tight">Spore</span>
            <span className="text-[10px] uppercase tracking-wider text-[var(--color-dim)]">Swarm Cockpit</span>
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 overflow-y-auto px-3 py-4">
          {(Object.keys(grouped) as NavItem['group'][]).map((group) => (
            <div key={group} className="mb-4">
              <div className="mb-2 px-2 text-[10px] font-medium uppercase tracking-wider text-[var(--color-muted)]">
                {GROUP_LABELS[group]}
              </div>
              {grouped[group].map((item) => {
                const Icon = item.icon
                const active = tab === item.id
                const badge = item.badge?.(state)
                return (
                  <button
                    key={item.id}
                    onClick={() => onTabChange(item.id)}
                    className={cn(
                      'group mb-0.5 flex w-full items-center justify-between gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 text-left text-[13px] transition-colors',
                      active
                        ? 'bg-[var(--color-accent-soft)] text-[var(--color-accent)]'
                        : 'text-[var(--color-dim)] hover:bg-[var(--color-card)] hover:text-[var(--color-fg)]',
                    )}
                  >
                    <span className="flex items-center gap-2.5">
                      <Icon className="h-4 w-4 shrink-0" strokeWidth={1.75} />
                      <span>{item.label}</span>
                    </span>
                    {badge != null && Number(badge) > 0 ? (
                      <span
                        className={cn(
                          'rounded-full px-1.5 py-0.5 text-[10px] font-medium tabular-nums',
                          active
                            ? 'bg-[var(--color-accent)] text-[var(--color-accent-fg)]'
                            : 'bg-[var(--color-card)] text-[var(--color-dim)]',
                        )}
                      >
                        {badge}
                      </span>
                    ) : null}
                  </button>
                )
              })}
            </div>
          ))}
        </nav>

        {/* Sidebar footer */}
        <div className="border-t border-[var(--color-border)] px-4 py-3">
          <button
            onClick={onToggleTheme}
            className="flex w-full items-center justify-between rounded-[var(--radius-sm)] px-2 py-1.5 text-[12px] text-[var(--color-dim)] transition-colors hover:bg-[var(--color-card)] hover:text-[var(--color-fg)]"
            aria-label="Toggle theme"
          >
            <span>{theme === 'dark' ? 'Dark mode' : 'Light mode'}</span>
            {theme === 'dark' ? (
              <Moon className="h-4 w-4" strokeWidth={1.75} />
            ) : (
              <Sun className="h-4 w-4" strokeWidth={1.75} />
            )}
          </button>
        </div>
      </aside>

      {/* Main: header + content */}
      <main className="flex min-w-0 flex-col overflow-hidden">
        {/* Top bar */}
        <header className="flex shrink-0 items-center justify-between gap-4 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-3">
          <div className="flex items-center gap-2">
            <span
              className={cn(
                'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-medium',
                connected
                  ? 'bg-[var(--color-green-soft)] text-[var(--color-green)]'
                  : 'bg-[var(--color-red-soft)] text-[var(--color-red)]',
              )}
            >
              <span
                className={cn(
                  'h-1.5 w-1.5 rounded-full',
                  connected ? 'bg-[var(--color-green)] pulse-soft' : 'bg-[var(--color-red)]',
                )}
              />
              {connected ? 'Online' : 'Offline'}
            </span>
            {network ? (
              <span className="inline-flex items-center gap-1.5 rounded-full bg-[var(--color-cyan-soft)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-cyan)]">
                <Network className="h-3 w-3" strokeWidth={2} />
                {network.transport === 'p2p' ? `${network.peers} peers` : 'Local'}
              </span>
            ) : null}
          </div>

          <div className="flex items-center gap-6 text-right">
            <Stat label="Agents" value={state?.agents?.length ?? 0} />
            <Stat label="Tasks" value={stats?.tasks_completed ?? 0} />
            <Stat label="Uptime" value={stats?.uptime_seconds ? formatDuration(Number(stats.uptime_seconds)) : '—'} />
            <Stat label="Content" value={state?.content?.items?.length ?? 0} />
          </div>
        </header>

        {/* Tab content */}
        <section className="min-h-0 flex-1 overflow-y-auto p-6">{children}</section>
      </main>

      {/* Status bar (under sidebar+main) */}
      <footer className="col-start-2 flex items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-2 text-[11px] text-[var(--color-muted)]">
        <span>spore swarm · 0.2.0-dev</span>
        <span>{lastUpdate ? `updated ${formatRelative(lastUpdate)}` : 'awaiting first frame…'}</span>
      </footer>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex flex-col items-end">
      <span className="text-sm font-semibold leading-none tabular-nums">{value}</span>
      <span className="mt-0.5 text-[9px] uppercase tracking-wider text-[var(--color-muted)]">{label}</span>
    </div>
  )
}
