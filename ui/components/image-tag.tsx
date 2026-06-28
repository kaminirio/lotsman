// Renders container image references compactly: the short repository name (last
// path segment) plus the tag/digest in a subtle chip, with the full image string
// on hover. Used in the Pods list, Workloads list, and workload history so the
// running image *version* is visible at a glance.

// splitImage parses an OCI image reference into a repository and a tag/digest.
// Handles registry ports (host:5000/repo:tag), digests (@sha256:…), and the
// implicit "latest" tag.
export function splitImage(image: string): { repo: string; tag: string } {
  const at = image.indexOf('@')
  if (at >= 0) {
    const digest = image.slice(at + 1)
    return { repo: image.slice(0, at), tag: digest.startsWith('sha256:') ? digest.slice(7, 19) : digest }
  }
  const lastSlash = image.lastIndexOf('/')
  const lastColon = image.lastIndexOf(':')
  // A colon after the last slash is a tag separator; one before it is a registry
  // port, so the image has no explicit tag.
  if (lastColon > lastSlash) {
    return { repo: image.slice(0, lastColon), tag: image.slice(lastColon + 1) }
  }
  return { repo: image, tag: 'latest' }
}

function shortRepo(repo: string): string {
  const seg = repo.split('/')
  return seg[seg.length - 1] || repo
}

function ImageRef({ image }: { image: string }) {
  const { repo, tag } = splitImage(image)
  return (
    <span title={image} className="flex min-w-0 flex-col leading-tight">
      <span className="truncate font-tech text-[12px] text-slate-200">{tag}</span>
      <span className="truncate font-tech text-[10px] text-slate-500">{shortRepo(repo)}</span>
    </span>
  )
}

/** One or more image references (de-duplicated), stacked. Empty → em dash. */
export function ImageTags({ images }: { images: string[] }) {
  const distinct = Array.from(new Set((images ?? []).filter(Boolean)))
  if (distinct.length === 0) return <span className="text-slate-600">—</span>
  return (
    <span className="inline-flex min-w-0 flex-col gap-1.5">
      {distinct.map((img) => (
        <ImageRef key={img} image={img} />
      ))}
    </span>
  )
}
