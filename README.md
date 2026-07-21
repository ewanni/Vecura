# Vecura

Wails v2 desktop app for semantic image search over your local gallery, using an
in-memory, model-aware vector engine (pure Go) and pluggable embedding providers
(OpenAI/OpenRouter-compatible HTTP, plus optional local `llama.cpp` via CGO).

## Architecture

- `internal/db` — SQLite (modernc.org/sqlite, pure Go) persistence of images,
  tags and raw embedding BLOBs, with WAL transactional batch insert.
- `internal/vector` — in-memory model-aware flat store. Vectors are preallocated
  with a `Count` cursor (no `append`-driven reallocation on reads), `InvNorm`
  precomputed at write time, and searches run over an `atomic.Pointer` snapshot
  with per-CPU local min-heaps merged into the final Top-K.
- `internal/embedder` — `Embedder` interface with an `OpenAICompatibleEmbedder`
  (pure Go HTTP, batched, pooled connections) plus an inference cache keyed by
  `hash(file+prompt+model)` and a rate-limit batcher.
- `internal/models` — registry of models with a RAM budget and LRU-eviction.
- `internal/scan` — folder watcher/pipeline: image -> embed -> batch insert ->
  vector add, with 256x256 thumbnails generated for the UI.
- `internal/api` — Wails `App` exposing `Search`, `AddModel`, `ListModels`,
  `ScanFolder`, `GetThumbnails`, and scan-progress events.

## Build & Run

Requires Go 1.21+ and the Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).

```bash
go mod tidy
wails build      # desktop binary
wails dev        # live dev mode
```

Pure-Go cross compilation (no local llama.cpp) is CGO-free:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
```

## Tests

```bash
go test ./...
go test -bench=. -benchmem ./...
```

## Data

State lives in `~/.vecura/gallery.db` with thumbnails under
`~/.vecura/thumbnails/`.
