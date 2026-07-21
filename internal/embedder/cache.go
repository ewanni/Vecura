package embedder

import (
	"crypto/sha256"
	"sync"
)

// Cache stores embeddings keyed by hash(file + prompt + model) to skip
// repeated inference (ARCHITECTURE.md section 5).
type Cache struct {
	mu    sync.RWMutex
	items map[string][]float32
}

// NewCache creates an empty embedder cache.
func NewCache() *Cache { return &Cache{items: make(map[string][]float32)} }

func cacheKey(file, prompt, model string) string {
	h := sha256.Sum256([]byte(file + "\x00" + prompt + "\x00" + model))
	return string(h[:])
}

// Get returns the cached vector and whether it was present.
func (c *Cache) Get(file, prompt, model string) ([]float32, bool) {
	k := cacheKey(file, prompt, model)
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[k]
	return v, ok
}

// Put stores a vector in the cache.
func (c *Cache) Put(file, prompt, model string, vec []float32) {
	k := cacheKey(file, prompt, model)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy to own the slice lifetime.
	cp := make([]float32, len(vec))
	copy(cp, vec)
	c.items[k] = cp
}

// Batcher serializes remote embed calls through a bounded queue to respect
// rate limits / cost (ARCHITECTURE.md section 5).
type Batcher struct {
	ch       chan batchJob
	embedder Embedder
}

type batchJob struct {
	texts []string
	res   chan batchResult
}

type batchResult struct {
	vecs [][]float32
	err  error
}

// NewBatcher starts a worker that drains embed requests sequentially.
func NewBatcher(e Embedder) *Batcher {
	b := &Batcher{
		ch:       make(chan batchJob, 1024),
		embedder: e,
	}
	go b.run()
	return b
}

func (b *Batcher) run() {
	for job := range b.ch {
		vecs, err := b.embedder.Embed(job.texts)
		job.res <- batchResult{vecs: vecs, err: err}
	}
}

// Submit enqueues a batch of texts and waits for the result.
func (b *Batcher) Submit(texts []string) ([][]float32, error) {
	res := make(chan batchResult, 1)
	b.ch <- batchJob{texts: texts, res: res}
	r := <-res
	return r.vecs, r.err
}
