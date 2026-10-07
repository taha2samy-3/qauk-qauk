/** Minimal controllable WebSocket double for realtime tests. */
export class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  readonly url: string
  readyState = 0
  sent: Record<string, unknown>[] = []
  onopen: ((e: Event) => void) | null = null
  onclose: ((e: CloseEvent) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  static reset() {
    FakeWebSocket.instances = []
  }
  static get last(): FakeWebSocket {
    const ws = FakeWebSocket.instances.at(-1)
    if (!ws) throw new Error('no socket created')
    return ws
  }

  send(data: string) {
    if (this.readyState !== 1) throw new Error('socket not open')
    this.sent.push(JSON.parse(data))
  }
  close() {
    this.readyState = 3
  }

  // --- test controls ---
  serverOpen() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }
  serverSend(frame: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }
  serverClose(code = 1006) {
    this.readyState = 3
    this.onclose?.(new CloseEvent('close', { code }))
  }
  sentOfType(type: string) {
    return this.sent.filter((f) => f.type === type)
  }
}
