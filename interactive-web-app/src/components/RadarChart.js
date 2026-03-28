import React from 'react';

export default function RadarChart({ drives }) {
  const size = 120;
  const cx = size / 2;
  const cy = size / 2;
  const r = 44;
  const keys = ['survive', 'explore', 'connect', 'transcend', 'create'];
  const labels = ['Sur', 'Exp', 'Con', 'Tra', 'Cre'];
  const n = keys.length;

  function polar(i, val) {
    const angle = (Math.PI * 2 * i / n) - Math.PI / 2;
    return [cx + r * val * Math.cos(angle), cy + r * val * Math.sin(angle)];
  }

  if (!drives) return null;

  const ringColor = 'var(--border)';
  const vals = keys.map(k => Math.min(1, Math.max(0, drives[k] || 0)));

  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      {/* Background rings */}
      {[0.33, 0.66, 1].map(s => {
        const pts = keys.map((_, i) => polar(i, s).join(',')).join(' ');
        return <polygon key={s} points={pts} fill="none" stroke={ringColor} strokeWidth="0.5" />;
      })}

      {/* Axes */}
      {keys.map((_, i) => {
        const [x, y] = polar(i, 1);
        return <line key={i} x1={cx} y1={cy} x2={x} y2={y} stroke={ringColor} strokeWidth="0.5" />;
      })}

      {/* Data polygon */}
      <polygon
        points={vals.map((v, i) => polar(i, v).join(',')).join(' ')}
        fill="rgba(124,58,237,0.15)"
        stroke="var(--accent)"
        strokeWidth="1.5"
      />

      {/* Data dots + labels */}
      {vals.map((v, i) => {
        const [x, y] = polar(i, v);
        const [lx, ly] = polar(i, 1.22);
        return (
          <React.Fragment key={i}>
            <circle cx={x} cy={y} r="2.5" fill="var(--accent)" />
            <text
              x={lx} y={ly}
              textAnchor="middle"
              dominantBaseline="central"
              fill="var(--dim)"
              fontSize="8"
              fontFamily="var(--font)"
            >
              {labels[i]}
            </text>
          </React.Fragment>
        );
      })}
    </svg>
  );
}
