import { useId } from 'react'
import { cn } from '@/lib/utils'

/**
 * The brand mark (branding/logo-mark.svg), inlined so it renders without a
 * request and with a per-instance gradient id.
 */
export function LogoMark({ className }: { className?: string }) {
  const id = useId()
  const grad = `qq-bg-${id}`
  return (
    <svg viewBox="0 0 64 64" className={cn('size-7 shrink-0', className)} aria-hidden>
      <defs>
        <linearGradient id={grad} x1="6" y1="4" x2="58" y2="60" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#4F46E5" />
          <stop offset="1" stopColor="#0891B2" />
        </linearGradient>
      </defs>
      <rect x="2" y="2" width="60" height="60" rx="16" fill={`url(#${grad})`} />
      <path
        d="M16.5 35.5C14.2 41.2 11 43.6 6.6 43.2C7.4 49.8 12.2 53.5 19 53.5H32.5C37.4 53.5 40.5 50.6 40.5 46.6C40.5 41.4 35.4 37.7 29 36.2Z"
        fill="#FFFFFF"
      />
      <circle cx="22.5" cy="27.5" r="11" fill="#FFFFFF" />
      <path d="M31.5 25.6C36.4 24.4 41.4 25 44.2 28.1C41.6 31.6 36.6 32.6 31.5 31.4Z" fill="#FBBF24" />
      <circle cx="25.4" cy="24.4" r="2.3" fill="#1E1B4B" />
      <path
        d="M47.5 23.8A6.4 6.4 0 0 1 47.5 32.4"
        fill="none"
        stroke="#FFFFFF"
        strokeWidth="3"
        strokeLinecap="round"
      />
      <path
        d="M51.6 18.6A12.6 12.6 0 0 1 51.6 37.6"
        fill="none"
        stroke="#FFFFFF"
        strokeWidth="3"
        strokeLinecap="round"
        opacity="0.75"
      />
    </svg>
  )
}

/** Mark + name as HTML text, so the wordmark follows the app's class-based theme. */
export function Logo({ collapsed }: { collapsed?: boolean }) {
  return (
    <span className="flex items-center gap-2.5">
      <LogoMark />
      {!collapsed && (
        <span className="text-foreground text-[15px] font-semibold tracking-tight">Quack Quack</span>
      )}
    </span>
  )
}
