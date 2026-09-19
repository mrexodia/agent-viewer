.PHONY: build test test-quick test-unit test-ui clean server watcher watcher-all watcher-pi watcher-claude

# Build all binaries
build:
	cd server && go build .
	cd watcher && go build .

# Run unit and integration tests
test: build
	cd server && go test -v -timeout 60s ./...
	cd watcher && go test -v -timeout 60s ./...
	cd tests && go test -v -timeout 120s ./...

# Run quick tests (no verbose)
test-quick: build test-unit
	cd tests && go test -timeout 120s ./...

test-unit:
	cd server && go test -timeout 60s ./...
	cd watcher && go test -timeout 60s ./...

# UI stream-state tests; requires Node.js 18+ (no npm dependencies).
test-ui:
	node --test tests/stream.test.cjs

# Clean build artifacts
clean:
	rm -f server/server watcher/watcher
	rm -rf test-sessions

# Start server (for manual testing)
server: build
	cd server && ./server

# Start watcher (for manual testing)
# Usage: make watcher WATCH_DIR=/path/to/sessions
watcher: build
	cd watcher && ./watcher --watch $(WATCH_DIR)

# Start watcher with both Pi and Claude sessions
watcher-all: build
	cd watcher && ./watcher --pi --claude

# Start watcher with Pi sessions only
watcher-pi: build
	cd watcher && ./watcher --pi

# Start watcher with Claude sessions only
watcher-claude: build
	cd watcher && ./watcher --claude

# Generate test data
test-data:
	./scripts/generate-test-data.sh
