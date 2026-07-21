package embedder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func mockServer(t *testing.T, wantDim int, wantModel string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Model != wantModel {
			t.Errorf("model = %q want %q", req.Model, wantModel)
		}
		var resp embedResponse
		for _, in := range req.Input {
			v := make([]float32, wantDim)
			// deterministic pseudo-embedding from input length
			for i := range v {
				v[i] = float32(len(in)) + float32(i)*0.001
			}
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
			}{Embedding: v})
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestOpenAIDimAndBatch(t *testing.T) {
	srv := mockServer(t, 4, "text-embedding-3-small")
	defer srv.Close()

	e := NewOpenAICompatibleEmbedder(srv.URL, "key", "text-embedding-3-small", 4, 2)
	if e.Dim() != 4 {
		t.Fatalf("dim %d", e.Dim())
	}
	if e.Key() != "remote/text-embedding-3-small" {
		t.Fatalf("key %q", e.Key())
	}

	vecs, err := e.Embed([]string{"a", "bb", "ccc", "dddd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 4 {
		t.Fatalf("got %d vecs", len(vecs))
	}
	for _, v := range vecs {
		if len(v) != 4 {
			t.Fatalf("vec dim %d", len(v))
		}
	}
}

func TestCacheHit(t *testing.T) {
	c := NewCache()
	if _, ok := c.Get("f", "p", "m"); ok {
		t.Fatal("unexpected hit")
	}
	vec := []float32{1, 2, 3}
	c.Put("f", "p", "m", vec)
	got, ok := c.Get("f", "p", "m")
	if !ok {
		t.Fatal("expected hit")
	}
	if strings.Join(floats(got), ",") != "1,2,3" {
		t.Fatalf("vec %v", got)
	}
	// different prompt => miss
	if _, ok := c.Get("f", "other", "m"); ok {
		t.Fatal("unexpected hit for different prompt")
	}
}

func TestBatcher(t *testing.T) {
	srv := mockServer(t, 2, "m")
	defer srv.Close()
	e := NewOpenAICompatibleEmbedder(srv.URL, "k", "m", 2, 4)
	b := NewBatcher(e)
	vecs, err := b.Submit([]string{"x", "yy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d", len(vecs))
	}
}

func floats(v []float32) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return out
}
