package db

import (
	"database/sql"
	"math"
	"os"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := t.TempDir() + "/test.db"
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
	})
	return d
}

func TestImageRepoRoundTrip(t *testing.T) {
	d := openTestDB(t)
	r := NewImageRepo(d)

	id, err := r.UpsertImage("/a/b.png", "a prompt")
	if err != nil {
		t.Fatal(err)
	}
	path, err := r.GetImagePath(id)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/a/b.png" {
		t.Fatalf("got %q", path)
	}
	if err := r.AddTag(id, "cyberpunk"); err != nil {
		t.Fatal(err)
	}
	ids, err := r.GetByTag("cyberpunk")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("get by tag: %v", ids)
	}
}

func TestBlobRoundTrip(t *testing.T) {
	vec := []float32{0.1, -2.5, 3.75, 100.25}
	blob := float32ToBlob(vec)
	dec := blobToFloat32(blob)
	if len(dec) != len(vec) {
		t.Fatalf("len %d != %d", len(dec), len(vec))
	}
	for i := range vec {
		if dec[i] != vec[i] {
			t.Fatalf("idx %d: %f != %f", i, dec[i], vec[i])
		}
	}
}

func TestBlobBytesMatch(t *testing.T) {
	vec := []float32{math.Float32frombits(0xDEADBEEF), math.Float32frombits(0x12345678)}
	blob := float32ToBlob(vec)
	for i, b := range blob {
		_ = b
		_ = i
	}
	dec := blobToFloat32(blob)
	if dec[0] != vec[0] || dec[1] != vec[1] {
		t.Fatal("byte-level decode mismatch")
	}
}

func TestEmbeddingBulkInsert10k(t *testing.T) {
	d := openTestDB(t)
	r := NewEmbeddingRepo(d)
	const n = 10000
	rows := make([]EmbeddingRow, n)
	for i := 0; i < n; i++ {
		rows[i] = EmbeddingRow{
			ImageID:  int32(i),
			Provider: "openai",
			ModelID:  "3-small",
			Dim:      2,
			Vector:   []float32{float32(i), float32(i + 1)},
		}
	}
	if err := r.BatchInsertEmbeddings(rows); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.GetEmbeddingsByModel("openai", "3-small")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != n {
		t.Fatalf("loaded %d != %d", len(loaded), n)
	}
	if loaded[5].Vector[0] != 5 {
		t.Fatalf("vector mismatch: %f", loaded[5].Vector[0])
	}
}

func TestLoadAllEmbeddings(t *testing.T) {
	d := openTestDB(t)
	r := NewEmbeddingRepo(d)
	rows := []EmbeddingRow{
		{ImageID: 1, Provider: "a", ModelID: "m1", Dim: 1, Vector: []float32{1}},
		{ImageID: 2, Provider: "b", ModelID: "m2", Dim: 1, Vector: []float32{2}},
	}
	if err := r.BatchInsertEmbeddings(rows); err != nil {
		t.Fatal(err)
	}
	all, err := r.LoadAllEmbeddings()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all %d", len(all))
	}
}
