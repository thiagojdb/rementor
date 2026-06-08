import { render } from 'solid-js/web'
import './styles.css'
import App from './App'
import { initTheme } from './stores/theme'

// Initialize theme from localStorage/prefers-color-scheme
initTheme()

const root = document.getElementById('root')
if (!root) throw new Error('Root element not found')

render(() => <App />, root)
