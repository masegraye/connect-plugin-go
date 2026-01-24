# Build Instructions for Claude

## Important: Use Taskfile for All Builds

**All build operations must use `task` commands.** Do not use `go build`, `go run`, `protoc`, or other build tools directly.

## Prerequisites

- Go 1.25+
- protoc (install via: `brew install protobuf` on macOS)
- task (install via: `brew install go-task/tap/go-task`)

## Common Tasks

### Install Build Dependencies

```bash
task install-deps
```

Installs:
- `protoc-gen-go` (protobuf Go code generator)
- `protoc-gen-connect-go` (Connect RPC code generator)

### Build Everything

```bash
task build
```

This automatically:
1. Runs `go mod tidy`
2. Generates protobuf code (`task proto`)
3. Builds all Go packages

### Generate Protobuf Code

```bash
task proto
```

Generates code from `.proto` files in `examples/*/proto/` to `examples/*/gen/`.

**Note:** Generated code is not committed to git (see `.gitignore`).

### Build Example Binaries

```bash
task build-examples
```

Builds example binaries to `dist/`:
- `dist/kv-server` - KV plugin server
- `dist/kv-host` - KV plugin client/host

### Run Examples

**Terminal 1 (server):**
```bash
task example-server
```

**Terminal 2 (client):**
```bash
task example-client
```

### Run Tests

```bash
task test
```

### Run Tests with Coverage

```bash
task test:coverage
```

Generates `coverage.html` for viewing coverage report.

### Clean Build Artifacts

```bash
task clean
```

Removes:
- Generated protobuf code (`examples/*/gen/`)
- Built binaries (`dist/`)
- Coverage files
- Build cache

## Directory Structure

```
connect-plugin-go/
├── dist/              # Built binaries (gitignored)
├── examples/
│   └── kv/
│       ├── gen/       # Generated proto code (gitignored)
│       ├── proto/     # Proto definitions (committed)
│       ├── plugin/    # Plugin implementation (committed)
│       ├── impl/      # KV store implementation (committed)
│       ├── server/    # Server binary (committed)
│       └── host/      # Client binary (committed)
├── docs/              # Design documentation
├── Taskfile.yml       # Build configuration
└── *.go               # Core library code
```

## Build Rules

1. **Always use `task`** - Never run `go build`, `protoc`, etc. directly
2. **Generated code** - Proto code is generated, not committed
3. **Build outputs** - All binaries go to `dist/` (gitignored)
4. **Clean state** - Run `task clean` before committing
5. **Dependencies** - Run `task install-deps` once on new checkout

## Development Workflow

```bash
# Initial setup
task install-deps

# Make changes to code
# ...

# Build and test
task build
task test

# Run example
task example-server  # Terminal 1
task example-client  # Terminal 2

# Clean before commit
task clean

# Commit (generated code and binaries not included)
git add -A
git commit
```
