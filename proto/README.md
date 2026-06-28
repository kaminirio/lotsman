# proto

`lotsman.proto` is the design source of truth for `internal/agentlink` (the agent
↔ control-plane gRPC bidi-stream transport, ADR-0002).

## Regenerating Go code

Generated Go (`internal/agentlink/pb/*.pb.go`) is committed so `go build ./...`
works offline. Regenerate after editing `lotsman.proto`:

```sh
# one-time toolchain (no system protoc needed — buf embeds the compiler):
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/bufbuild/buf/cmd/buf@latest

# regenerate (run from the repo root; ensure $(go env GOBIN) is on PATH):
buf generate
```

Config lives in `buf.yaml` (module) + `buf.gen.yaml` (plugins). The
`module=lotsman` plugin option strips the module prefix from the proto's
`go_package` (`lotsman/internal/agentlink/pb;pb`) so output lands at
`internal/agentlink/pb/`.
