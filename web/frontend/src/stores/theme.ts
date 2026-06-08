import { createSignal, createEffect } from 'solid-js'

function getStoredTheme(): 'light' | 'dark' {
  const stored = localStorage.getItem('theme')
  if (stored === 'dark' || stored === 'light') return stored
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

const [theme, setThemeSignal] = createSignal<'light' | 'dark'>(getStoredTheme())

export { theme }

export function initTheme(): void {
  const root = document.documentElement
  const currentTheme = theme()

  // Set data attribute for CSS to pick up
  root.setAttribute('data-theme', currentTheme)
}

export function toggleTheme(): void {
  const next = theme() === 'dark' ? 'light' : 'dark'
  setThemeSignal(next)
  localStorage.setItem('theme', next)

  const root = document.documentElement
  root.setAttribute('data-theme', next)
}

// Auto-initialize on import in browser
if (typeof document !== 'undefined') {
  createEffect(() => {
    initTheme()
  })
}
