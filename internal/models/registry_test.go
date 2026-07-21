package models

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry(0)
	fake := &fakeEmbed{key: "remote/m", dim: 8}
	lm := r.RegisterRemote(fake)
	if lm.Key != "remote/m" {
		t.Fatalf("key %q", lm.Key)
	}
	e, ok := r.Get("remote/m")
	if !ok || e.Dim() != 8 {
		t.Fatalf("get failed: %v %d", ok, e.Dim())
	}
	if len(r.List()) != 1 {
		t.Fatalf("list len %d", len(r.List()))
	}
}

func TestEviction(t *testing.T) {
	var freed int64
	r := NewRegistry(100)
	r.OnEvict(func(m *LoadedModel) {
		atomic.AddInt64(&freed, m.RAMBytes)
	})

	// Load two local models of 60 bytes each (budget 100 => only one fits).
	if _, err := r.LoadLocal("l1", "local", "m1", &fakeEmbed{key: "l1"}, 60); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LoadLocal("l2", "local", "m2", &fakeEmbed{key: "l2"}, 60); err != nil {
		t.Fatalf("eviction should have freed space but got %v", err)
	}
	// l1 should have been evicted.
	if _, ok := r.Get("l1"); ok {
		t.Fatal("l1 should be evicted")
	}
	if _, ok := r.Get("l2"); !ok {
		t.Fatal("l2 should remain")
	}
	if atomic.LoadInt64(&freed) != 60 {
		t.Fatalf("freed %d", freed)
	}
}

func TestThreadSafeRegistry(t *testing.T) {
	r := NewRegistry(0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "remote/m" + string(rune('a'+i%26))
			r.RegisterRemote(&fakeEmbed{key: key, dim: i})
			_, _ = r.Get(key)
		}(i)
	}
	wg.Wait()
	if len(r.List()) == 0 {
		t.Fatal("no models registered")
	}
}

type fakeEmbed struct {
	key string
	dim int
}

func (f *fakeEmbed) Key() string { return f.key }
func (f *fakeEmbed) Dim() int    { return f.dim }
func (f *fakeEmbed) Embed(t []string) ([][]float32, error) {
	out := make([][]float32, len(t))
	for i := range out {
		out[i] = make([]float32, f.dim)
	}
	return out, nil
}
