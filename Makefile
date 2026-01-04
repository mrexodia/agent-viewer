.PHONY: build test clean server watcher

# Build all binaries
build:
	cd server && go build -o server .
	cd watcher && go build -o watcher .

# Run integration tests
test: build
	cd tests && go test -v -timeout 60s ./...

# Run quick tests (no verbose)
test-quick: build
	cd tests && go test -timeout 60s ./...

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

# Generate test data
test-data:
	./scripts/generate-test-data.sh
