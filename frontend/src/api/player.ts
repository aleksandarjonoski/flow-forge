import type { TraceEvent } from './client.ts'

// Plays a run's trace back at a readable pace (docs/design.md, "Live
// execution view", playback speed). Only the display is slowed: events are
// queued as they arrive and released one by one.

export type Speed = 'realtime' | 'normal' | 'slow'

export const SPEEDS: readonly { value: Speed; label: string }[] = [
  { value: 'realtime', label: 'Real time' },
  { value: 'normal', label: 'Normal' },
  { value: 'slow', label: 'Slow' },
]

/** Roughly how long one node takes on screen, per speed. */
const STEP_MS: Record<Speed, number> = { realtime: 0, normal: 600, slow: 1500 }

/**
 * How long to wait after showing an event before showing the next one. A
 * started node stays "running" for most of a step; a finished node, and the
 * trigger, get a short pause so the hand-over to the next node is visible.
 */
export function holdAfter(kind: TraceEvent['kind'], speed: Speed): number {
  const step = STEP_MS[speed]
  switch (kind) {
    case 'execution_started':
      return step / 2
    case 'node_started':
      return step
    case 'node_completed':
    case 'node_failed':
      return step / 4
    default:
      return 0
  }
}

/**
 * Releases pushed trace events to `show` one at a time, holding each for
 * holdAfter(kind, speed). Events that arrive later than the pace allows are
 * shown as soon as they arrive.
 */
export class TracePlayer {
  private queue: TraceEvent[] = []
  private timer?: ReturnType<typeof setTimeout>
  private waiters: (() => void)[] = []
  private stopped = false
  private readonly show: (event: TraceEvent) => void
  private speed: Speed

  constructor(show: (event: TraceEvent) => void, speed: Speed) {
    this.show = show
    this.speed = speed
  }

  push(event: TraceEvent) {
    if (this.stopped) return
    this.queue.push(event)
    this.pump()
  }

  /** Changes the pace; Real time shows everything queued at once. */
  setSpeed(speed: Speed) {
    this.speed = speed
    if (speed === 'realtime' && this.timer !== undefined) {
      clearTimeout(this.timer)
      this.timer = undefined
      this.pump()
    }
  }

  /** Resolves once every pushed event has been shown and held. */
  drained(): Promise<void> {
    if (this.idle()) return Promise.resolve()
    return new Promise((resolve) => this.waiters.push(resolve))
  }

  /** Drops queued events and stops; drained() resolves. */
  stop() {
    this.stopped = true
    this.queue = []
    clearTimeout(this.timer)
    this.timer = undefined
    this.notifyIfIdle()
  }

  private pump() {
    while (this.timer === undefined) {
      const event = this.queue.shift()
      if (!event) break
      this.show(event)
      const hold = holdAfter(event.kind, this.speed)
      if (hold > 0) {
        this.timer = setTimeout(() => {
          this.timer = undefined
          this.pump()
        }, hold)
      }
    }
    this.notifyIfIdle()
  }

  private idle(): boolean {
    return this.timer === undefined && this.queue.length === 0
  }

  private notifyIfIdle() {
    if (!this.idle()) return
    const waiters = this.waiters
    this.waiters = []
    for (const resolve of waiters) resolve()
  }
}
