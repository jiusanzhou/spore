import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from 'react'

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

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost'
  size?: 'sm' | 'md'
}

export function Button({
  variant = 'secondary',
  size = 'md',
  className,
  children,
  ...rest
}: ButtonProps) {
  const base =
    'inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors ' +
    'disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus:ring-1 ' +
    'focus:ring-[var(--color-accent)]'
  const sizing = size === 'sm' ? 'h-7 px-2.5 text-[11px]' : 'h-8 px-3 text-[12px]'
  const variants: Record<string, string> = {
    primary:
      'bg-[var(--color-accent)] text-white hover:opacity-90 active:opacity-80',
    secondary:
      'border border-[var(--color-border)] bg-[var(--color-card)] hover:bg-[var(--color-card-hover,var(--color-card))] ' +
      'text-[var(--color-fg)]',
    ghost: 'text-[var(--color-dim)] hover:text-[var(--color-fg)]',
  }
  return (
    <button className={`${base} ${sizing} ${variants[variant]} ${className ?? ''}`} {...rest}>
      {children}
    </button>
  )
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  const { className, ...rest } = props
  return (
    <input
      className={
        'h-8 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-bg)] px-2.5 ' +
        'text-[12px] placeholder:text-[var(--color-muted)] ' +
        'focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] ' +
        (className ?? '')
      }
      {...rest}
    />
  )
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const { className, ...rest } = props
  return (
    <textarea
      className={
        'block w-full resize-y rounded-md border border-[var(--color-border)] bg-[var(--color-bg)] px-2.5 py-1.5 ' +
        'text-[12px] placeholder:text-[var(--color-muted)] ' +
        'focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] ' +
        (className ?? '')
      }
      {...rest}
    />
  )
}

export function Badge({
  tone = 'neutral',
  children,
}: {
  tone?: 'neutral' | 'green' | 'red' | 'amber' | 'accent'
  children: ReactNode
}) {
  const tones: Record<string, string> = {
    neutral: 'bg-[var(--color-card)] text-[var(--color-dim)] border-[var(--color-border)]',
    green: 'bg-[var(--color-green-soft)] text-[var(--color-green)] border-transparent',
    red: 'bg-[var(--color-red-soft)] text-[var(--color-red)] border-transparent',
    amber: 'bg-[var(--color-amber-soft)] text-[var(--color-amber)] border-transparent',
    accent: 'bg-[var(--color-accent-soft,var(--color-card))] text-[var(--color-accent)] border-transparent',
  }
  return (
    <span
      className={
        'inline-flex shrink-0 items-center rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider ' +
        tones[tone]
      }
    >
      {children}
    </span>
  )
}
