import { flushSync } from 'react-dom'
import { useCallback, useState, type MouseEvent } from 'react'

type ViewTransition = {
  ready: Promise<void>
  finished: Promise<void>
}

type ViewTransitionDocument = Document & {
  startViewTransition?: (update: () => void) => ViewTransition
}

function initialTheme(): boolean {
  const saved = localStorage.getItem('theme')
  if (saved === 'dark') return true
  if (saved === 'light') return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function initializeTheme() {
  document.documentElement.classList.toggle('dark', initialTheme())
}

export function useThemeTransition() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))

  const toggleTheme = useCallback(async (event: MouseEvent<HTMLButtonElement>) => {
    const root = document.documentElement
    const apply = () => {
      flushSync(() => setDark((value) => !value))
      const nextDark = !root.classList.contains('dark')
      root.classList.toggle('dark', nextDark)
      localStorage.setItem('theme', nextDark ? 'dark' : 'light')
    }

    const transitionDocument = document as ViewTransitionDocument
    if (!transitionDocument.startViewTransition || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      apply()
      return
    }

    const bounds = event.currentTarget.getBoundingClientRect()
    const x = bounds.left + bounds.width / 2
    const y = bounds.top + bounds.height / 2
    const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y))
    const expandingDark = !root.classList.contains('dark')
    const circle = [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`]
    root.dataset.themeTransition = expandingDark ? 'expand' : 'retract'
    const transition = transitionDocument.startViewTransition(apply)

    try {
      await transition.ready
      await root.animate(
        { clipPath: expandingDark ? circle : [...circle].reverse() },
        {
          duration: 620,
          easing: 'cubic-bezier(0.65, 0, 0.35, 1)',
          fill: 'both',
          pseudoElement: expandingDark ? '::view-transition-new(root)' : '::view-transition-old(root)',
        },
      ).finished
    } catch {
      // A backgrounded tab can interrupt the animation after the theme has changed.
    } finally {
      await transition.finished.catch(() => undefined)
      delete root.dataset.themeTransition
    }
  }, [])

  return { dark, toggleTheme }
}
