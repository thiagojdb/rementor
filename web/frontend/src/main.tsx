import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { initializeTheme } from './hooks/use-theme-transition'
import './styles.css'

initializeTheme()

const root = document.getElementById('root')
if (!root) throw new Error('Root element not found')

createRoot(root).render(<StrictMode><App /></StrictMode>)
