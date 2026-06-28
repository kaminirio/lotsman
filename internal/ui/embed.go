// Package ui embeds the built Next.js static export and serves it (with SPA
// fallback) from the control-plane binary — the single-binary deploy model.
package ui

import "embed"

// Files contains the static UI export. In development dist/ holds a placeholder
// (the UI is served by `next dev` separately). In production the Dockerfile
// copies the Next.js export into dist/ before compiling the Go binary.
//
//go:embed all:dist
var Files embed.FS
