import { Component, JSX } from 'solid-js'
import { Router, Route } from '@solidjs/router'
import Layout from './components/layout/Layout'
import ApplicationsPage from './pages/ApplicationsPage'
import ConfigurationPage from './pages/ConfigurationPage'

// Wrapper component to include Layout around page content
const PageWrapper = (Component: Component): (() => JSX.Element) => {
  return () => (
    <Layout>
      <Component />
    </Layout>
  )
}

const App: Component = () => {
  return (
    <Router>
      <Route path="/" component={PageWrapper(ApplicationsPage)} />
      <Route path="/configuration" component={PageWrapper(ConfigurationPage)} />
    </Router>
  )
}

export default App
