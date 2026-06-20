import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** shadcn-style className merger: clsx → tailwind-merge to dedupe conflicts. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Format bytes as human-readable string. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  const v = bytes / Math.pow(1024, i)
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

/** Format seconds as compact duration: 1d 2h, 1h 5m, 5m 20s, 30s. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const s = Math.floor(seconds)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

/** Truncate hash to first N + ellipsis. */
export function shortHash(s: string | undefined | null, n = 8): string {
  if (!s) return ''
  return s.length <= n + 2 ? s : `${s.slice(0, n)}…`
}

/** Format an absolute timestamp (ms or s) as relative — "5s ago", "2m ago". */
export function formatRelative(ts: number | string | undefined): string {
  if (!ts) return ''
  const t = typeof ts === 'string' ? new Date(ts).getTime() : ts > 1e12 ? ts : ts * 1000
  const diff = Math.max(0, Date.now() - t) / 1000
  if (diff < 5) return 'now'
  if (diff < 60) return `${Math.floor(diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}
