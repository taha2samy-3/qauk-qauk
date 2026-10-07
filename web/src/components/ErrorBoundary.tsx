import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  /** rendered instead of the children after an error; `reset` re-mounts them */
  fallback: (error: Error, reset: () => void) => ReactNode
  /** changing this value resets the boundary */
  resetKey?: unknown
  children: ReactNode
}

/** Contains render errors so one broken widget cannot take down the page. */
export class ErrorBoundary extends Component<Props, { error: Error | null; key: unknown }> {
  state = { error: null as Error | null, key: this.props.resetKey }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  static getDerivedStateFromProps(props: Props, state: { error: Error | null; key: unknown }) {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Widget crashed', error, info.componentStack)
  }

  render() {
    if (this.state.error) return this.props.fallback(this.state.error, () => this.setState({ error: null }))
    return this.props.children
  }
}
