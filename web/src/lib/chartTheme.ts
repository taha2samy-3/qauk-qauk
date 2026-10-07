import type { ResolvedTheme } from './theme'

/**
 * ECharts (zrender) cannot parse oklch() CSS colors, so charts use this
 * hex palette that mirrors the CSS tokens in index.css.
 */
export interface ChartPalette {
  series: string[]
  text: string
  muted: string
  grid: string
  axis: string
  track: string
  tooltipBg: string
  tooltipBorder: string
  ok: string
  warn: string
  bad: string
}

const light: ChartPalette = {
  series: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6'],
  text: '#1f2430',
  muted: '#6b7280',
  grid: '#eceef2',
  axis: '#d5d8de',
  track: '#eceef2',
  tooltipBg: '#ffffff',
  tooltipBorder: '#e4e6eb',
  ok: '#10b981',
  warn: '#f59e0b',
  bad: '#ef4444',
}

const dark: ChartPalette = {
  series: ['#60a5fa', '#34d399', '#fbbf24', '#f87171', '#a78bfa'],
  text: '#eef0f4',
  muted: '#9aa0ad',
  grid: '#262a33',
  axis: '#353a45',
  track: '#2a2e38',
  tooltipBg: '#1d2029',
  tooltipBorder: '#323743',
  ok: '#34d399',
  warn: '#fbbf24',
  bad: '#f87171',
}

export function chartPalette(theme: ResolvedTheme): ChartPalette {
  return theme === 'dark' ? dark : light
}

/** Named colors offered in threshold pickers. */
export const THRESHOLD_COLORS: { name: string; light: string; dark: string }[] = [
  { name: 'green', light: '#10b981', dark: '#34d399' },
  { name: 'blue', light: '#3b82f6', dark: '#60a5fa' },
  { name: 'yellow', light: '#f59e0b', dark: '#fbbf24' },
  { name: 'orange', light: '#f97316', dark: '#fb923c' },
  { name: 'red', light: '#ef4444', dark: '#f87171' },
  { name: 'purple', light: '#8b5cf6', dark: '#a78bfa' },
]

export function resolveColor(color: string, theme: ResolvedTheme): string {
  const named = THRESHOLD_COLORS.find((c) => c.name === color)
  if (named) return theme === 'dark' ? named.dark : named.light
  return color
}
