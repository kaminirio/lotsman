// Build definition for the three Lotsman images. They share the common UI +
// Go build stages (see Dockerfile), so a single `docker buildx bake` compiles
// the UI and the Go binaries ONCE and emits all three images from it.
//
// CI drives tags/labels via docker/metadata-action (which generates extra bake
// files merged on top of this one). For a quick local build:
//   docker buildx bake                       # all three, multi-arch
//   docker buildx bake server --set '*.platform=linux/amd64' --load

variable "VERSION" { default = "dev" }
variable "REGISTRY" { default = "ghcr.io" }
variable "OWNER" { default = "kaminirio" }

group "default" {
  targets = ["server", "agent", "cli"]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {
    VERSION = VERSION
  }
}

target "server" {
  inherits = ["_common"]
  target   = "server"
  tags     = ["${REGISTRY}/${OWNER}/lotsman-server:${VERSION}"]
}

target "agent" {
  inherits = ["_common"]
  target   = "agent"
  tags     = ["${REGISTRY}/${OWNER}/lotsman-agent:${VERSION}"]
}

target "cli" {
  inherits = ["_common"]
  target   = "cli"
  tags     = ["${REGISTRY}/${OWNER}/lotsman-cli:${VERSION}"]
}
