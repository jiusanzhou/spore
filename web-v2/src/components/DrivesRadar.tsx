import { useId } from 'react'

interface DrivesRadarProps {
  drives: {
    survival?: number
    exploration?: number
    consolidation?: number
    transmission?: number
    creativity?: number
  }
  size?: number
}

const AXES: { key: keyof DrivesRadarProps['drives']; label: string }[] = [
  { key: 'survival', label: 'Survival' },
  { key: 'exploration', label: 'Explore' },
  { key: 'consolidation', label: 'Consolidate' },
  { key: 'transmission', label: 'Transmit' },
  { key: 'creativity', label: 'Creativity' },
]

export function DrivesRadar({ drives, size = 200 }: DrivesRadarProps) {
  const uid = useId().replace(/:/g, '')
  const cx = size / 2
  const cy = size / 2
  const r = size * 0.32
  const labelR = r + 18

  const points = AXES.map((axis, i) => {
    const v = Number(drives[axis.key] ?? 0)
    const clamped = Math.max(0, Math.min(1, v))
    const angle = -Math.PI / 2 + (i * 2 * Math.PI) / AXES.length
    return {
      ...axis,
      v: clamped,
      x: cx + Math.cos(angle) * r * clamped,
      y: cy + Math.sin(angle) * r * clamped,
      ax: cx + Math.cos(angle) * r,
      ay: cy + Math.sin(angle) * r,
      lx: cx + Math.cos(angle) * labelR,
      ly: cy + Math.sin(angle) * labelR,
    }
  })

  const polygon = points.map((p) => `${p.x},${p.y}`).join(' ')

  return (
    <svg viewBox={`-20 -10 ${size + 40} ${size + 20}`} className="block w-full" aria-label="agent drives">
      <defs>
        <radialGradient id={`g-${uid}`} cx="50%" cy="50%" r="50%">
          <stop offset="0%" stopColor="var(--color-accent)" stopOpacity="0.5" />
          <stop offset="100%" stopColor="var(--color-accent)" stopOpacity="0.15" />
        </radialGradient>
      </defs>
      {[0.25, 0.5, 0.75, 1].map((s) => (
        <polygon
          key={s}
          points={AXES.map((_, i) => {
            const angle = -Math.PI / 2 + (i * 2 * Math.PI) / AXES.length
            return `${cx + Math.cos(angle) * r * s},${cy + Math.sin(angle) * r * s}`
          }).join(' ')}
          fill="none"
          stroke="var(--color-border)"
          strokeWidth="0.5"
        />
      ))}
      {points.map((p) => (
        <line
          key={p.key as string}
          x1={cx}
          y1={cy}
          x2={p.ax}
          y2={p.ay}
          stroke="var(--color-border)"
          strokeWidth="0.5"
        />
      ))}
      <polygon
        points={polygon}
        fill={`url(#g-${uid})`}
        stroke="var(--color-accent)"
        strokeWidth="1.5"
      />
      {points.map((p) => (
        <circle key={`d-${p.key as string}`} cx={p.x} cy={p.y} r={2} fill="var(--color-accent)" />
      ))}
      {points.map((p) => (
        <text
          key={`l-${p.key as string}`}
          x={p.lx}
          y={p.ly}
          textAnchor="middle"
          dominantBaseline="middle"
          fontSize="9"
          fill="var(--color-dim)"
        >
          {p.label}
        </text>
      ))}
    </svg>
  )
}
