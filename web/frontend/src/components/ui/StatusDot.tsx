import { Component } from 'solid-js'

interface StatusDotProps {
  status: 'healthy' | 'unhealthy' | 'unknown'
  title?: string
}

const StatusDot: Component<StatusDotProps> = (props) => {
  const dotStyle = () => {
    if (props.status === 'healthy') {
      return {
        'background-color': 'var(--success)',
        'box-shadow': '0 0 0 3px var(--success-subtle)'
      }
    }
    if (props.status === 'unhealthy') {
      return {
        'background-color': 'var(--error)',
        'box-shadow': '0 0 0 3px var(--error-subtle)'
      }
    }
    return {
      'background-color': 'var(--text-tertiary)'
    }
  }

  return (
    <span
      class="inline-block w-2 h-2 rounded-full"
      style={dotStyle()}
      title={props.title ?? props.status}
    />
  )
}

export default StatusDot
