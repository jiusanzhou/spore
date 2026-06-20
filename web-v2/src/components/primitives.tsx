import type { ReactNode } from 'react'

export function EmptyState({
  icon,
  title,
  description,
}: {
  icon?: ReactNode
  title: string
  description?: string
}) {
  return (
    <div className="flex h-full min-h-[300px] flex-col items-center justify-center text-center">
      {icon ? <div className="mb-3 text-3xl text-[var(--color-muted)]">{icon}</div> : null}
      <h3 className="text-base font-medium text-[var(--color-dim)]">{title}</h3>
      {description ? (
        <p className="mt-1 max-w-md text-sm text-[var(--color-muted)]">{description}</p>
      ) : null}
    </div>
  )
}

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={
        'rounded-[var(--radius)] border border-[var(--color-border)] bg-[var(--color-card)] p-4 ' +
        (className ?? '')
      }
    >
      {children}
    </div>
  )
}

export function PageHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-6">
      <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
      {description ? <p className="mt-1 text-sm text-[var(--color-dim)]">{description}</p> : null}
    </div>
  )
}
