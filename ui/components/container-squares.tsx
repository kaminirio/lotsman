import { type Container } from '@/lib/api'
import { containerStatusStyle } from '@/lib/styles'

/**
 * Lens-style per-container status indicator: one small square per container,
 * colored by the container's live state (emerald=running&ready, amber=pending/
 * unready, red=crash/back-off/error, slate=completed/unknown). Each square has a
 * native tooltip and aria-label of `name — status` so hover and screen readers
 * both surface which container is which. Used in the Pods list "Containers"
 * column and echoed on the pod detail page.
 */
export function ContainerSquares({ containers }: { containers: Container[] }) {
  if (!containers || containers.length === 0) {
    return <span className="text-slate-600">—</span>
  }
  return (
    <span className="inline-flex items-center gap-1" role="img" aria-label="Container statuses">
      {containers.map((c) => {
        const { cls, label } = containerStatusStyle(c)
        const title = `${c.name} — ${label}`
        return (
          <span
            key={c.name}
            title={title}
            aria-label={title}
            className={`h-2 w-2 shrink-0 rounded-[2px] ${cls}`}
          />
        )
      })}
    </span>
  )
}
