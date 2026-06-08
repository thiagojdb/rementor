import { Component, ParentComponent } from 'solid-js'
import Sidebar from './Sidebar'
import ToastContainer from '../ui/Toast'

const Layout: ParentComponent = (props) => {
  return (
    <div class="flex min-h-screen overflow-x-hidden" style={{ 'background-color': 'var(--bg-primary)' }}>
      <Sidebar />
      <main class="flex-1 min-h-screen ml-16 flex flex-col" style={{ 'background-color': 'var(--bg-primary)' }}>
        {props.children}
      </main>
      <ToastContainer />
    </div>
  )
}

export default Layout
