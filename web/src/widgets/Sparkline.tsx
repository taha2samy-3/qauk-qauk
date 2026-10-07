/** Tiny dependency-free SVG sparkline. */
export function Sparkline({
  values,
  color = 'currentColor',
  className,
}: {
  values: number[]
  color?: string
  className?: string
}) {
  if (values.length < 2) return <div className={className} />
  const w = 100
  const h = 30
  let min = Math.min(...values)
  let max = Math.max(...values)
  if (max === min) {
    max += 1
    min -= 1
  }
  const pts = values.map(
    (v, i) => [(i / (values.length - 1)) * w, h - ((v - min) / (max - min)) * (h - 2) - 1] as const,
  )
  const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`).join(' ')
  const area = `${line} L${w},${h} L0,${h} Z`
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className={className} aria-hidden>
      <path d={area} fill={color} opacity={0.12} />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
      />
    </svg>
  )
}
