VERSION ?= dev
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test vet fmt audit run-server run-agent ui-dev ui-build docker clean \
	local-up local-core local-down local-logs local-investigate

build: ## Build all Go binaries into bin/
	go build $(LDFLAGS) -o bin/lotsman-server ./cmd/server
	go build $(LDFLAGS) -o bin/lotsman-agent ./cmd/agent
	go build $(LDFLAGS) -o bin/lotsman ./cmd/lotsman

test: ## Run Go tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format Go sources
	gofmt -w .

audit: ## Scan Go + UI dependencies for known vulnerabilities
	govulncheck ./...
	cd ui && npm audit --audit-level=moderate

run-server: ## Run the control plane (direct mode, in-memory store + seed data)
	LOTSMAN_DIRECT_MODE=1 LOTSMAN_CLUSTER=local go run ./cmd/server

run-agent: ## Run the in-cluster agent (dials the control plane)
	go run ./cmd/agent

ui-dev: ## Run the Next.js dev server against the API on :8080
	cd ui && npm install && NEXT_PUBLIC_API_URL=http://localhost:8080 npm run dev

ui-build: ## Build the UI static export into the Go embed dir
	cd ui && npm install && npm run build
	rm -rf internal/ui/dist && mkdir -p internal/ui/dist && cp -r ui/out/* internal/ui/dist/

docker: ## Build the server and agent images
	docker build --target server -t lotsman-server:$(VERSION) .
	docker build --target agent -t lotsman-agent:$(VERSION) .

clean: ## Remove build artifacts
	rm -rf bin

local-up: ## Build + run the FULL local stack (control plane + Loki + VictoriaMetrics + demo)
	docker compose --profile full up --build -d
	@echo "UI:   http://localhost:8080   |   mock ArgoCD: http://localhost:8081/api/v1/applications"
	@echo "Try:  make local-investigate"

local-core: ## Build + run just the control plane + Postgres (seed data, fast)
	docker compose up --build -d
	@echo "UI: http://localhost:8080"

local-down: ## Stop the local stack and remove volumes
	docker compose --profile full down -v

local-logs: ## Tail control-plane + demo logs
	docker compose --profile full logs -f control-plane demo

local-investigate: ## Run a live investigation over the demo resource and pretty-print it
	curl -s -XPOST localhost:8080/api/v1/investigate \
		-d '{"cluster":"local","namespace":"demo","kind":"Deployment","name":"checkout"}' | jq
