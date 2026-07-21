# AGENTS.md

## Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Benchmarks: `go test -bench=. -benchmem ./...`
- Lint: `go vet ./...`
- Format: `go fmt ./...`

## Conventions

- Pure Go where possible; CGO only allowed in `internal/embedder` (local llama.cpp) and nowhere else.
- Vectors never use `append` growing the base pointer during reads; use preallocate + `Count` + atomic snapshot.
- Store `InvNorm` precomputed at write time; never recompute cosine denominator per search.
- Decode BLOB to `[]float32` via `unsafe.Slice` with no copy.
- Persist embeddings in WAL transactional batch inserts.
