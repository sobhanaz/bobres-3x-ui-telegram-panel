// A countdown for the screens that give a customer a limited time: a price quote, a USDT payment.
import { onBeforeUnmount, ref } from 'vue'

/** monotonic is the device's own clock in seconds: a wrong date on the phone does not move it (a time that is counted from now). */
export const monotonic = () => performance.now() / 1000
/** wall is the phone's date and time as unix seconds (a moment that core named, like a payment's last minute). */
export const wall = () => Date.now() / 1000

/**
 * useCountdown counts whole seconds down to a deadline, given in the units of the clock it is made with (monotonic unless said
 * otherwise), and runs onDone once, when it is reached. It reads the clock at every tick instead of counting ticks, so a timer
 * the browser held back while the app was away is right again at the next one. It stops by itself when the screen goes.
 */
export function useCountdown(onDone: () => void, now: () => number = monotonic) {
  const left = ref(0)
  const running = ref(false)
  let timer: ReturnType<typeof setInterval> | undefined
  let deadline = 0

  function stop() {
    if (timer) clearInterval(timer)
    timer = undefined
    running.value = false
  }

  function tick() {
    left.value = Math.max(0, Math.ceil(deadline - now()))
    if (left.value > 0) return
    stop()
    onDone()
  }

  /** start counts down to the deadline; one that is already behind does not run (and does not call onDone: the caller sees left is 0). */
  function start(at: number) {
    stop()
    deadline = at
    left.value = Math.max(0, Math.ceil(deadline - now()))
    if (left.value === 0) return
    running.value = true
    timer = setInterval(tick, 1000)
  }

  onBeforeUnmount(stop)
  return { left, running, start, stop }
}
