import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LoadingState, ErrorState, EmptyState } from './view-states'

describe('LoadingState', () => {
  it('renders the default label', () => {
    render(<LoadingState />)
    expect(screen.getByText('Loading…')).toBeInTheDocument()
  })

  it('renders a custom label and marks itself busy', () => {
    const { container } = render(<LoadingState label="Loading pods…" />)
    expect(screen.getByText('Loading pods…')).toBeInTheDocument()
    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull()
  })
})

describe('ErrorState', () => {
  it('renders the label + error inside an alert region', () => {
    render(<ErrorState label="Failed to load pods" error="network down" />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Failed to load pods: network down')
  })
})

describe('EmptyState', () => {
  it('renders its label', () => {
    render(<EmptyState label="No pods in this namespace." />)
    expect(screen.getByText('No pods in this namespace.')).toBeInTheDocument()
  })
})
