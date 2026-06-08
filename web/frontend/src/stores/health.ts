import { createSignal } from 'solid-js'
import { Code, ConnectError } from '@connectrpc/connect'
import { watchHealth } from '../api/client'
import { updateAppHealth } from './workspaces'

const [connected, setConnected] = createSignal(false)

export { connected }

let abortController: AbortController | null = null

export function connectHealthEvents(wsId: string): () => void {
  if (abortController) {
    abortController.abort()
    abortController = null
  }

  abortController = new AbortController()
  const currentController = abortController

  void (async () => {
    try {
      for await (const update of watchHealth(wsId, { signal: currentController.signal })) {
        if (update.type === 'connected') {
          setConnected(true)
          continue
        }
        if (update.workspaceId && update.applicationName) {
          updateAppHealth(
            update.workspaceId,
            update.applicationName,
            update.localOk ?? false,
            update.remoteOk ?? false
          )
        }
      }
    } catch (error) {
      const connectError = ConnectError.from(error)
      if (connectError.code !== Code.Canceled) {
        setConnected(false)
      }
    }
  })()

  return () => {
    if (abortController === currentController) {
      abortController.abort()
      abortController = null
      setConnected(false)
    }
  }
}
