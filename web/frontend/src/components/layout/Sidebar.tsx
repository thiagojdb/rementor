import { Component } from 'solid-js'
import { A } from '@solidjs/router'
import ThemeToggle from './ThemeToggle'

const Sidebar: Component = () => {
  return (
    <aside
      class="fixed left-0 top-0 h-full w-16 flex flex-col items-center py-4 z-30"
      style={{
        'background-color': 'var(--bg-primary)',
        'border-right': '1px solid var(--border-subtle)'
      }}
    >
      {/* Logo */}
      <div
        class="w-8 h-8 flex items-center justify-center mb-6"
        style={{ color: 'var(--accent-500)' }}
        title="Rementor"
      >
        <svg viewBox="0 0 32 32" width="32" height="32" fill="none" xmlns="http://www.w3.org/2000/svg">
          <line x1="5" y1="16" x2="15" y2="16" stroke="currentColor" stroke-opacity="0.5" stroke-width="2" stroke-linecap="round" />
          <line x1="15" y1="16" x2="25" y2="9" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
          <line x1="15" y1="16" x2="25" y2="23" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
          <circle cx="15" cy="16" r="3" fill="currentColor" />
          <circle cx="25" cy="9" r="2" fill="currentColor" />
          <circle cx="25" cy="23" r="2" fill="currentColor" />
        </svg>
      </div>

      {/* Navigation */}
      <nav class="flex flex-col items-center gap-1 flex-1">
        <A
          href="/"
          class="w-10 h-10 flex items-center justify-center rounded-lg transition-colors"
          style={{
            color: 'var(--text-tertiary)',
          }}
          activeClass="active-nav"
          inactiveClass="hover:text-accent hover:bg-accent-subtle"
          end={true}
          title="Applications"
        >
          {/* Grid icon */}
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor" class="w-5 h-5 stroke-[1.5]">
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
        </A>

        <A
          href="/configuration"
          class="w-10 h-10 flex items-center justify-center rounded-lg transition-colors"
          style={{
            color: 'var(--text-tertiary)',
          }}
          activeClass="active-nav"
          inactiveClass="hover:text-accent hover:bg-accent-subtle"
          title="Configuration"
        >
          {/* Settings/sliders icon */}
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor" class="w-5 h-5 stroke-[1.5]">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 0 0 2.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 0 0 1.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 0 0-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 0 0-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 0 0-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 0 0-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 0 0 1.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
        </A>
      </nav>

      {/* Bottom section */}
      <div class="flex flex-col items-center gap-1 mt-auto">
        <ThemeToggle />
      </div>

      <style>{`
        .active-nav {
          background-color: var(--accent-500);
          color: var(--bg-primary);
        }
        .active-nav:hover {
          background-color: var(--accent-400);
          color: var(--bg-primary);
        }
      `}</style>
    </aside>
  )
}

export default Sidebar
