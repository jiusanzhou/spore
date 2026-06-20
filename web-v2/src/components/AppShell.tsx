import { useEffect, useState, type ReactNode } from 'react'
import {
  Network,
  Users,
  Store,
  BookOpen,
  Brain,
  Database,
  Sparkles,
  MessagesSquare,
  Activity,
  ScrollText,
  GitCommit,
  MessageSquare,
  Moon,
  Sun,
  Menu,
  X,
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
  { id: 'chat', label: 'Chat', icon: MessagesSquare, group: 'activity' },
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
  const [drawerOpen, setDrawerOpen] = useState(false)

  // Close drawer on tab change
  const handleTabChange = (id: TabId) => {
    onTabChange(id)
    setDrawerOpen(false)
  }

  // Lock body scroll when drawer open on mobile
  useEffect(() => {
    if (drawerOpen) {
      document.body.style.overflow = 'hidden'
    } else {
      document.body.style.overflow = ''
    }
    return () => {
      document.body.style.overflow = ''
    }
  }, [drawerOpen])

  // Close drawer on Escape
  useEffect(() => {
    if (!drawerOpen) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawerOpen(false)
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [drawerOpen])

  // Group nav items
  const grouped: Record<NavItem['group'], NavItem[]> = { swarm: [], memory: [], activity: [] }
  for (const item of NAV) grouped[item.group].push(item)

  const sidebarContent = (
    <>
      {/* Logo */}
      <div className="flex items-center justify-between gap-3 border-b border-[var(--color-border)] px-5 py-4">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-sm)] bg-[var(--color-accent)] text-[var(--color-accent-fg)]">
            <span className="text-base">🦠</span>
          </div>
          <div className="flex flex-col">
            <span className="text-sm font-semibold leading-tight tracking-tight">Spore</span>
            <span className="text-[10px] uppercase tracking-wider text-[var(--color-dim)]">Swarm Cockpit</span>
          </div>
        </div>
        {/* Close button — mobile only */}
        <button
          onClick={() => setDrawerOpen(false)}
          className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-sm)] text-[var(--color-dim)] hover:bg-[var(--color-card)] hover:text-[var(--color-fg)] md:hidden"
          aria-label="Close menu"
        >
          <X className="h-4 w-4" strokeWidth={1.75} />
        </button>
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
                  onClick={() => handleTabChange(item.id)}
                  className={cn(
                    'group mb-0.5 flex w-full items-center justify-between gap-2 rounded-[var(--radius-sm)] px-2 py-2 text-left text-[13px] transition-colors md:py-1.5',
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
    </>
  )

  return (
    <div className="flex h-full flex-col bg-[var(--color-bg)] text-[var(--color-fg)] md:grid md:grid-cols-[240px_1fr] md:grid-rows-[1fr_auto]">
      {/* Desktop sidebar */}
      <aside className="hidden md:row-span-2 md:flex md:flex-col md:border-r md:border-[var(--color-border)] md:bg-[var(--color-surface)]">
        {sidebarContent}
      </aside>

      {/* Mobile drawer */}
      {drawerOpen && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/60 backdrop-blur-sm md:hidden"
            onClick={() => setDrawerOpen(false)}
            aria-hidden="true"
          />
          <aside className="fixed inset-y-0 left-0 z-50 flex w-[280px] max-w-[85vw] flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] md:hidden">
            {sidebarContent}
          </aside>
        </>
      )}

      {/* Main: header + content */}
      <main className="flex min-w-0 flex-1 flex-col overflow-hidden md:flex-initial">
        {/* Top bar */}
        <header className="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3 md:gap-4 md:px-6">
          <div className="flex min-w-0 items-center gap-2">
            {/* Hamburger — mobile only */}
            <button
              onClick={() => setDrawerOpen(true)}
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--radius-sm)] text-[var(--color-dim)] hover:bg-[var(--color-card)] hover:text-[var(--color-fg)] md:hidden"
              aria-label="Open menu"
            >
              <Menu className="h-5 w-5" strokeWidth={1.75} />
            </button>

            {/* Mobile-only logo (when drawer closed) */}
            <div className="flex items-center gap-2 md:hidden">
              <span className="text-base">🦠</span>
              <span className="text-sm font-semibold tracking-tight">Spore</span>
            </div>

            <span
              className={cn(
                'inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-medium',
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
              <span className="hidden shrink-0 items-center gap-1.5 rounded-full bg-[var(--color-cyan-soft)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-cyan)] sm:inline-flex">
                <Network className="h-3 w-3" strokeWidth={2} />
                {network.transport === 'p2p' ? `${network.peers} peers` : 'Local'}
              </span>
            ) : null}
          </div>

          {/* Stats — full set on lg+, condensed on md, single on mobile */}
          <div className="flex shrink-0 items-center gap-3 text-right md:gap-6">
            <Stat label="Agents" value={state?.agents?.length ?? 0} />
            <Stat
              label="Tasks"
              value={stats?.tasks_completed ?? 0}
              className="hidden sm:flex"
            />
            <Stat
              label="Uptime"
              value={stats?.uptime_seconds ? formatDuration(Number(stats.uptime_seconds)) : '—'}
              className="hidden lg:flex"
            />
            <Stat
              label="Content"
              value={state?.content?.items?.length ?? 0}
              className="hidden lg:flex"
            />
          </div>
        </header>

        {/* Tab content */}
        <section className="min-h-0 flex-1 overflow-y-auto p-4 md:p-6">{children}</section>
      </main>

      {/* Status bar */}
      <footer className="flex shrink-0 items-center justify-between gap-3 border-t border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2 text-[10px] text-[var(--color-muted)] md:col-start-2 md:px-6 md:text-[11px]">
        <span className="truncate">spore swarm · 0.2.0-dev</span>
        <span className="shrink-0">{lastUpdate ? `updated ${formatRelative(lastUpdate)}` : 'awaiting first frame…'}</span>
      </footer>
    </div>
  )
}

function Stat({ label, value, className }: { label: string; value: string | number; className?: string }) {
  return (
    <div className={cn('flex flex-col items-end', className)}>
      <span className="text-sm font-semibold leading-none tabular-nums">{value}</span>
      <span className="mt-0.5 text-[9px] uppercase tracking-wider text-[var(--color-muted)]">{label}</span>
    </div>
  )
}
