package models

import (
	"errors"
	"sync"

	"vecura/internal/embedder"
)

var errOverBudget = errors.New("ram budget exceeded: cannot evict enough local models")

// LoadedModel tracks a registered embedder and its last-used time for LRU.
type LoadedModel struct {
	Key      string
	Provider string
	ModelID  string
	Embedder embedder.Embedder
	Local    bool
	LastUsed int64
	RAMBytes int64
}

// Registry is a thread-safe collection of models with RAM budgeting and
// LRU-eviction (ARCHITECTURE.md sections 5).
type Registry struct {
	mu         sync.RWMutex
	models     map[string]*LoadedModel
	ramBudget  int64
	ramUsed    int64
	evictHooks []func(*LoadedModel)
}

// NewRegistry creates a registry with the given RAM budget (bytes; 0 = unlimited).
func NewRegistry(ramBudget int64) *Registry {
	return &Registry{
		models:    make(map[string]*LoadedModel),
		ramBudget: ramBudget,
	}
}

// OnEvict registers a callback invoked when a model is evicted/unloaded.
func (r *Registry) OnEvict(fn func(*LoadedModel)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictHooks = append(r.evictHooks, fn)
}

// RegisterRemote adds a remote (pure-Go) embedder.
func (r *Registry) RegisterRemote(e embedder.Embedder) *LoadedModel {
	lm := &LoadedModel{
		Key:      e.Key(),
		Provider: "remote",
		ModelID:  e.Key(),
		Embedder: e,
		Local:    false,
	}
	r.mu.Lock()
	r.models[lm.Key] = lm
	r.mu.Unlock()
	return lm
}

// LoadLocal registers a local (CGO) model with its RAM footprint.
func (r *Registry) LoadLocal(key, provider, modelID string, e embedder.Embedder, ramBytes int64) (*LoadedModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lm := &LoadedModel{
		Key:      key,
		Provider: provider,
		ModelID:  modelID,
		Embedder: e,
		Local:    true,
		RAMBytes: ramBytes,
	}
	if r.ramBudget > 0 {
		if err := r.evictLocked(lm.RAMBytes); err != nil {
			return nil, err
		}
	}
	r.models[key] = lm
	r.ramUsed += ramBytes
	return lm, nil
}

// evictLocked frees space for need bytes. Caller holds r.mu.
func (r *Registry) evictLocked(need int64) error {
	for r.ramBudget > 0 && r.ramUsed+need > r.ramBudget {
		victim := r.lruLocked()
		if victim == nil {
			return errOverBudget
		}
		r.unloadLocked(victim)
	}
	return nil
}

func (r *Registry) lruLocked() *LoadedModel {
	var oldest *LoadedModel
	for _, m := range r.models {
		if !m.Local {
			continue
		}
		if oldest == nil || m.LastUsed < oldest.LastUsed {
			oldest = m
		}
	}
	return oldest
}

func (r *Registry) unloadLocked(m *LoadedModel) {
	delete(r.models, m.Key)
	r.ramUsed -= m.RAMBytes
	for _, fn := range r.evictHooks {
		fn(m)
	}
}

// Unload explicitly removes a model by key.
func (r *Registry) Unload(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.models[key]; ok {
		r.unloadLocked(m)
	}
}

// Get returns the embedder for a model key.
func (r *Registry) Get(key string) (embedder.Embedder, bool) {
	r.mu.RLock()
	m, ok := r.models[key]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return m.Embedder, true
}

// List returns all registered models.
func (r *Registry) List() []*LoadedModel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*LoadedModel, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, m)
	}
	return out
}
