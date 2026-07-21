package scan

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vecura/internal/db"
	"vecura/internal/models"
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
		// deterministic embedding derived from text length
		v := make([]float32, f.dim)
		for j := range v {
			v[j] = float32(len(t[i])) + float32(j)*0.01
		}
		out[i] = v
	}
	return out, nil
}

func setup(t *testing.T) (*Pipeline, *vector.VectorStore, *models.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(dir + "/g.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	store := vector.NewVectorStore()
	reg := models.NewRegistry(0)
	reg.RegisterRemote(&fakeEmbed{key: "remote/m", dim: 4})
	p := NewPipeline(d, reg, store, dir+"/thumbs")
	return p, store, reg, dir
}

func TestScanIdempotent(t *testing.T) {
	p, store, _, dir := setup(t)
	root := filepath.Join(dir, "imgs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, "img"+string(rune('A'+i%26))+".png"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	if err := p.ScanFolder(ctx, root, "remote/m"); err != nil {
		t.Fatal(err)
	}
	first := store.Search([]float32{1, 2, 3, 4}, "remote", "remote/m", 5)

	// Re-scan: should not duplicate entries.
	if err := p.ScanFolder(ctx, root, "remote/m"); err != nil {
		t.Fatal(err)
	}
	second := store.Search([]float32{1, 2, 3, 4}, "remote", "remote/m", 5)
	if len(first) != len(second) {
		t.Fatalf("idempotency broken: %d != %d", len(first), len(second))
	}
	if store.Search([]float32{1, 2, 3, 4}, "remote", "remote/m", 1000) == nil {
		t.Fatal("expected results")
	}
}

func TestScanDuringSearch(t *testing.T) {
	p, _, _, dir := setup(t)
	root := filepath.Join(dir, "imgs")
	os.MkdirAll(root, 0o755)
	for i := 0; i < 30; i++ {
		os.WriteFile(filepath.Join(root, "img"+string(rune('A'+i%26))+".png"), []byte("x"), 0o644)
	}

	done := make(chan struct{})
	go func() {
		_ = p.ScanFolder(context.Background(), root, "remote/m")
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_ = p.store.Search([]float32{1, 1, 1, 1}, "remote", "remote/m", 3)
	}
	<-done
}

func TestBuildFromDB(t *testing.T) {
	p, _, _, dir := setup(t)
	root := filepath.Join(dir, "imgs")
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "a.png"), []byte("x"), 0o644)
	if err := p.ScanFolder(context.Background(), root, "remote/m"); err != nil {
		t.Fatal(err)
	}
	// New store should rebuild from DB.
	store2 := vector.NewVectorStore()
	p2 := NewPipelineFromStore(p, store2)
	if err := p2.BuildFromDB(); err != nil {
		t.Fatal(err)
	}
	if len(store2.Search([]float32{1, 1, 1, 1}, "remote", "remote/m", 10)) == 0 {
		t.Fatal("rebuilt store empty")
	}
}

func pngChunk(typ string, data []byte) []byte {
	buf := make([]byte, 8+len(data)+4)
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(data)))
	copy(buf[4:8], typ)
	copy(buf[8:8+len(data)], data)
	crc := crc32.ChecksumIEEE(buf[4 : 8+len(data)])
	binary.BigEndian.PutUint32(buf[8+len(data):], crc)
	return buf
}

func TestExtractPromptPNG(t *testing.T) {
	var b bytes.Buffer
	b.Write(pngSig)
	b.Write(pngChunk("IHDR", make([]byte, 13)))
	b.Write(pngChunk("tEXt", []byte("parameters\x00Tsunade, 1girl, naruto")))
	b.Write(pngChunk("iTXt", []byte("prompt\x00\x00\x00\x00\x00Tsunade in konoha")))
	b.Write(pngChunk("IEND", nil))

	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ExtractPrompt(p)
	if !strings.Contains(got, "Tsunade") {
		t.Fatalf("expected Tsunade in extracted prompt, got %q", got)
	}
	// The "parameters" key has top priority, so its value leads the string.
	if !strings.HasPrefix(got, "Tsunade, 1girl, naruto") {
		t.Fatalf("expected parameters text first, got %q", got)
	}
}

func TestExtractPromptFallback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.txt")
	os.WriteFile(p, []byte("not an image"), 0o644)
	if got := ExtractPrompt(p); got != "" {
		t.Fatalf("expected empty for non-image, got %q", got)
	}
}
