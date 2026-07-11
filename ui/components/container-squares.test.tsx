import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ContainerSquares } from './container-squares'
import type { Container } from '@/lib/api'

function container(over: Partial<Container>): Container {
  return { name: 'app', image: 'app:1', state: 'running', ready: true, restart_count: 0, ...over }
}

describe('ContainerSquares', () => {
  it('renders an em dash when there are no containers', () => {
    render(<ContainerSquares containers={[]} />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('exposes each square status textually, not by color alone (UI-5)', () => {
    render(
      <ContainerSquares
        containers={[
          container({ name: 'web', state: 'running', ready: true }),
          container({ name: 'sidecar', state: 'waiting', ready: false, reason: 'CrashLoopBackOff' }),
        ]}
      />,
    )
    // Each square carries a name + human status label via aria-label/title so
    // screen readers and hover convey state without relying on the fill color.
    expect(screen.getByLabelText('web — Running, ready')).toBeInTheDocument()
    expect(screen.getByLabelText('sidecar — Waiting: CrashLoopBackOff')).toBeInTheDocument()
  })
})
