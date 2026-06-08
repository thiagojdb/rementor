import { Component } from 'solid-js'
import { theme, toggleTheme } from '../../stores/theme'

const ThemeToggle: Component = () => {
  return (
    <button
      class="w-10 h-10 flex items-center justify-center rounded-lg border transition-colors"
      style={{
        'background-color': 'var(--bg-secondary)',
        'border-color': 'var(--border-subtle)',
        color: 'var(--text-tertiary)'
      }}
      onClick={toggleTheme}
      aria-label="Toggle theme"
      title="Toggle theme"
    >
      {/* Moon - shown in light mode */}
      <svg
        class="w-[18px] h-[18px] stroke-[1.5]"
        classList={{ hidden: theme() === 'dark', block: theme() === 'light' }}
        style={{ color: 'var(--accent-500)' }}
        xmlns="http://www.w3.org/2000/svg"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <path stroke-linecap="round" stroke-linejoin="round" d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
      </svg>
      {/* Sun - shown in dark mode */}
      <svg
        class="w-[18px] h-[18px] stroke-[1.5]"
        classList={{ hidden: theme() === 'light', block: theme() === 'dark' }}
        style={{ color: 'var(--accent-500)' }}
        xmlns="http://www.w3.org/2000/svg"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <circle cx="12" cy="12" r="5" />
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
      </svg>
    </button>
  )
}

export default ThemeToggle
