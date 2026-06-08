/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_CONNECT_JSON?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
