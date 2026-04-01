# Marvel — agent orchestration control plane
# Probe: marvel-mvp-probe | Confidence: frontier

default:
    @just --list

# Build all binaries (marvel + simulator)
build:
    go build -o bin/marvel ./cmd/marvel/
    go build -o bin/simulator ./cmd/simulator/

# Build simulator only
build-sim:
    go build -o bin/simulator ./cmd/simulator/

# Run all tests
test:
    go test ./... -v

# Run tests with race detector
test-race:
    go test ./... -race -v

# Format code
fmt:
    gofumpt -w .

# Lint code
lint:
    golangci-lint run ./...

# Start the marvel daemon (foreground)
start: build
    ./bin/marvel daemon

# Start the daemon in the background
start-bg: build
    ./bin/marvel daemon &
    @sleep 1
    @echo "marvel daemon started"

# Stop the daemon and clean up all tmux sessions
stop:
    ./bin/marvel stop || true

# Load the demo manifest
demo: build
    @echo "==> Loading demo manifest..."
    ./bin/marvel work examples/demo.toml
    @sleep 2
    @echo ""
    @echo "==> Workspaces:"
    ./bin/marvel get workspaces
    @echo ""
    @echo "==> Teams:"
    ./bin/marvel get teams
    @echo ""
    @echo "==> Sessions:"
    ./bin/marvel get sessions
    @echo ""
    @echo "==> Endpoints:"
    ./bin/marvel get endpoints
    @echo ""
    @echo "Demo running. Use 'just stop' to tear down."
    @echo "Attach to tmux: tmux attach -t marvel-demo"

# Show running state
status: build
    @echo "==> Workspaces:"
    @./bin/marvel get workspaces
    @echo ""
    @echo "==> Teams:"
    @./bin/marvel get teams
    @echo ""
    @echo "==> Sessions:"
    @./bin/marvel get sessions

# Scale a team role: just scale demo/squad worker 5
scale team role replicas: build
    ./bin/marvel scale {{team}} --role {{role}} --replicas {{replicas}}

# Clean up everything (kill all marvel tmux sessions)
clean:
    -tmux kill-session -t marvel-demo 2>/dev/null
    -rm -f /tmp/marvel.sock
    -rm -rf bin/
