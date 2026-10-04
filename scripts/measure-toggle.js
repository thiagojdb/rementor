// Run in the Rementor page's browser console. Results include the successful
// RPC, workspace refresh and DOM update; pending feedback is not completion.
// Example: await measureToggle('job', 40, 250)
async function measureToggle(applicationName, samples = 40, pauseMs = 250) {
  if (!Number.isInteger(samples) || samples < 1) throw new Error('samples must be positive')
  const button = () => {
    const label = `Route ${applicationName} locally`
    const result = [...document.querySelectorAll('[role="switch"]')].find(element => element.getAttribute('aria-label') === label)
    if (!result) throw new Error(`Route switch not found: ${applicationName}`)
    return result
  }
  const state = () => button().getAttribute('aria-checked')
  const initial = state()
  const results = []
  const toggle = () => new Promise((resolve, reject) => {
    const before = state()
    if (button().disabled) return reject(new Error('Toggle is already pending'))
    const start = performance.now()
    const observer = new MutationObserver(() => {
      const current = button()
      if (!current.disabled && state() !== before) {
        clearTimeout(timeout)
        observer.disconnect()
        resolve(performance.now() - start)
      }
    })
    const timeout = setTimeout(() => { observer.disconnect(); reject(new Error('Toggle failed or exceeded 10 seconds')) }, 10000)
    observer.observe(document.querySelector('main') || document.body, { subtree: true, attributes: true, childList: true })
    button().click()
  })
  try {
    for (let i = 0; i < samples; i++) {
      results.push(await toggle())
      await new Promise(resolve => setTimeout(resolve, pauseMs))
    }
  } finally {
    if (!button().disabled && state() !== initial) await toggle()
  }
  const sorted = [...results].sort((a, b) => a - b)
  const percentile = p => sorted[Math.ceil(p * sorted.length) - 1]
  return { samples: results.length, p50: percentile(.5), p80: percentile(.8), p95: percentile(.95), max: sorted.at(-1), pass: percentile(.8) <= 200, restored: state() === initial, timings: results }
}
