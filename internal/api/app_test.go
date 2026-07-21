package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vecura/internal/db"
	"vecura/internal/embedder"
	"vecura/internal/models"
	"vecura/internal/scan"
	"vecura/internal/vector"
)

type fakeEmbed struct {
	key string
	dim int
}

func (f *fakeEmbed) Key() string { return f.key }
func (f *fakeEmbed) Dim() int    { return f.dim }
func (f *fakeEmbed) Embed(t []string) ([][]float32, error) {
	out := make([][]float32, len(t))
	for i := range out {
		v := make([]float32, f.dim)
		v[0] = float32(len(t[i]))
		out[i] = v
	}
	return out, nil
}

func newTestApp(t *testing.T) (*App, *vector.VectorStore) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(dir + "/g.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := vector.NewVectorStore()
	reg := models.NewRegistry(0)
	reg.RegisterRemote(&fakeEmbed{key: "remote/m", dim: 2})
	p := scan.NewPipeline(d, reg, store, dir+"/thumbs")
	return NewApp(d, p, store, reg, dir+"/thumbs"), store
}

func TestAddModelAndList(t *testing.T) {
	app, _ := newTestApp(t)
	info, err := app.AddModel(AddModelConfig{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "k",
		Model:   "text-embedding-3-small",
		Dim:     2,
		Batch:   16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != "remote/text-embedding-3-small" {
		t.Fatalf("key %q", info.Key)
	}
	if info.Dim != 2 {
		t.Fatalf("dim %d", info.Dim)
	}
	if len(app.ListModels()) < 2 {
		t.Fatalf("expected >=2 models, got %d", len(app.ListModels()))
	}
}

func TestSearchReturnsStructs(t *testing.T) {
	app, store := newTestApp(t)
	// Manually seed the store + db via scan pipeline would require files.
	// Instead seed directly to validate API wiring.
	store.Add(1, "remote", "m", []float32{3, 1})
	// Inject image path into db.
	if _, err := app.db.Exec(`INSERT INTO images (id, file_path, prompt) VALUES (1, ?, ?)`, "/x/a.png", "a cat"); err != nil {
		t.Fatal(err)
	}

	hits, err := app.Search("hello", "remote", "m", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits %d", len(hits))
	}
	if hits[0].ID != 1 || hits[0].Path != "/x/a.png" {
		t.Fatalf("hit %+v", hits[0])
	}
	if hits[0].ThumbnailURI != "" {
		t.Fatalf("unexpected thumb %q", hits[0].ThumbnailURI)
	}
}

func TestSearchUnknownModel(t *testing.T) {
	app, _ := newTestApp(t)
	// With no registered model and no keyword match, Search must not error;
	// it degrades to a (empty) keyword search instead of failing hard.
	hits, err := app.Search("q", "remote", "nope", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(hits))
	}
}

// TestSearchKeywordNoModel verifies that keyword search works even when no
// embedding model is registered at all.
func TestSearchKeywordNoModel(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(dir + "/g.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := vector.NewVectorStore()
	reg := models.NewRegistry(0) // intentionally no models registered
	p := scan.NewPipeline(d, reg, store, dir+"/thumbs")
	app := NewApp(d, p, store, reg, dir+"/thumbs")
	if _, err := d.Exec(`INSERT INTO images (file_path, prompt) VALUES (?, ?)`, "/x/tsunade.png", "photorealistic anime girl"); err != nil {
		t.Fatal(err)
	}
	hits, err := app.Search("photorealistic", "remote", "m", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected keyword hit, got %d", len(hits))
	}
	if hits[0].Prompt != "photorealistic anime girl" {
		t.Fatalf("prompt %q", hits[0].Prompt)
	}
}

func TestThumbnailURIGeneration(t *testing.T) {
	dir := t.TempDir()
	thumb := filepath.Join(dir, "thumbs")
	os.MkdirAll(thumb, 0o755)
	os.WriteFile(filepath.Join(thumb, "a.png.png"), []byte("IMG"), 0o644)
	app := NewApp(nil, nil, nil, nil, thumb)
	uri := app.thumbnailURI("x/a.png")
	if uri == "" {
		t.Fatal("expected data uri")
	}
	if len(uri) < 22 || uri[:22] != "data:image/png;base64," {
		t.Fatalf("bad uri prefix: %q", uri)
	}
	_ = context.Background()
	_ = embedder.NewCache
}

func TestProviderPresets(t *testing.T) {
	app, _ := newTestApp(t)
	presets, err := app.ProviderPresets()
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) < 2 {
		t.Fatalf("expected >=2 presets, got %d", len(presets))
	}
	var openai bool
	for _, p := range presets {
		if p.ID == "openai" && p.BaseURL != "" {
			openai = true
		}
	}
	if !openai {
		t.Fatal("openai preset missing base url")
	}
}

func TestListRemoteModels(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"text-embedding-3-small","context_length":8191},{"id":"other-model"}]}`))
	}))
	defer srv.Close()

	models, err := app.ListRemoteModels(srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	// The client-side embedding heuristic keeps only embedding-capable models
	// ("other-model" is dropped), so a single model is expected.
	if len(models) != 1 {
		t.Fatalf("models %d", len(models))
	}
	if models[0].ID != "text-embedding-3-small" || models[0].ContextLength != 8191 {
		t.Fatalf("model %+v", models[0])
	}
}

func TestAddModelAutoDim(t *testing.T) {
	app, _ := newTestApp(t)
	// No dim supplied; should infer 1536 from "3-small".
	info, err := app.AddModel(AddModelConfig{
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
		APIKey:   "k",
		Model:    "text-embedding-3-small",
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Dim != 1536 {
		t.Fatalf("inferred dim %d", info.Dim)
	}
}

func TestImageDataURI(t *testing.T) {
	dir := t.TempDir()
	thumb := filepath.Join(dir, "thumbs")
	os.MkdirAll(thumb, 0o755)
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	os.WriteFile(filepath.Join(thumb, "a.png.png"), png, 0o644)
	app := NewApp(nil, nil, nil, nil, thumb)
	uri, err := app.ImageDataURI("x/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if uri == "" {
		t.Fatal("empty uri")
	}
}
