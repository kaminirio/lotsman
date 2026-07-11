package api

import (
	_ "embed"
	"net/http"
)

// openAPISpec is the embedded OpenAPI 3.1 document describing this REST surface.
// It is authored by hand in openapi.yaml and served verbatim; keep it in sync
// with the routes in router.go and the handlers in handlers.go/sse.go. Serving
// the raw spec (no bundled Swagger UI) keeps the API std-lib only.
//
//go:embed openapi.yaml
var openAPISpec []byte

// handleOpenAPISpec serves the embedded OpenAPI document as YAML.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	// application/yaml is the IANA-registered media type for YAML (RFC 9512).
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}
