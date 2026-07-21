package vector

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

func naiveTopK(Q []float32, ids []int32, vecs []float32, dim, count, K int) []int32 {
	type sc struct {
		id int32
		sc float32
	}
	var qn float32
	for _, x := range Q {
		qn += x * x
	}
	qn = float32(math.Sqrt(float64(qn)))
	scores := make([]sc, count)
	for i := 0; i < count; i++ {
		base := i * dim
		var dot, vn float32
		for j := 0; j < dim; j++ {
			v := vecs[base+j]
			dot += Q[j] * v
			vn += v * v
		}
		scores[i] = sc{id: ids[i], sc: dot / (qn * float32(math.Sqrt(float64(vn))))}
	}
	sort.Slice(scores, func(a, b int) bool { return scores[a].sc > scores[b].sc })
	out := make([]int32, 0, K)
	for i := 0; i < K && i < len(scores); i++ {
		out = append(out, scores[i].id)
	}
	return out
}

func TestSearchCorrectness(t *testing.T) {
	s := NewVectorStore()
	rng := func(seed int) []float32 {
		return []float32{
			float32(seed%13) - 6,
			float32((seed*3)%11) - 5,
			float32((seed*7)%17) - 8,
		}
	}
	const n = 500
	rows := make([]EmbeddingRow, n)
	for i := 0; i < n; i++ {
		rows[i] = EmbeddingRow{ImageID: int32(i), Provider: "p", ModelID: "m", Dim: 3, Vector: rng(i)}
	}
	s.BuildFromRows(rows)

	Q := rng(499) // closest to id 499
	K := 10
	got := s.Search(Q, "p", "m", K)
	if len(got) != K {
		t.Fatalf("want %d got %d", K, len(got))
	}
	want := naiveTopK(Q, extractIDs(rows), flatten(rows), 3, n, K)
	gotSet := toSet(gotIDs(got))
	wantSet := toSet(want)
	if fmt.Sprint(gotSet) != fmt.Sprint(wantSet) {
		t.Fatalf("topk set mismatch\n got=%v\nwant=%v", gotIDs(got), want)
	}
}

func extractIDs(rows []EmbeddingRow) []int32 {
	ids := make([]int32, len(rows))
	for i, r := range rows {
		ids[i] = r.ImageID
	}
	return ids
}
func flatten(rows []EmbeddingRow) []float32 {
	dim := rows[0].Dim
	out := make([]float32, len(rows)*dim)
	for i, r := range rows {
		copy(out[i*dim:], r.Vector)
	}
	return out
}
func gotIDs(r []Result) []int32 {
	out := make([]int32, len(r))
	for i, x := range r {
		out[i] = x.ID
	}
	return out
}

func toSet(ids []int32) map[int32]bool {
	m := make(map[int32]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestSearchDuringAdd(t *testing.T) {
	s := NewVectorStore()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5000; i++ {
			v := []float32{float32(i), float32(i + 1)}
			s.Add(int32(i), "p", "m", v)
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		res := s.Search([]float32{1, 2}, "p", "m", 5)
		_ = res
	}
	<-done
	res := s.Search([]float32{1, 2}, "p", "m", 5)
	if len(res) != 5 {
		t.Fatalf("after add len=%d", len(res))
	}
}

func BenchmarkSearch(b *testing.B) {
	s := NewVectorStore()
	const (
		n   = 100000
		dim = 2048
	)
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := 0; j < dim; j++ {
			v[j] = float32((i + j) % 7)
		}
		s.Add(int32(i), "p", "m", v)
	}
	Q := make([]float32, dim)
	for j := 0; j < dim; j++ {
		Q[j] = float32(j % 5)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Search(Q, "p", "m", 50)
	}
}

// BenchmarkBuildFromRows measures boot-time load of 100k vectors grouped by
// (provider, model_id), as done from the DB (ARCHITECTURE.md section 3).
func BenchmarkBuildFromRows(b *testing.B) {
	const (
		n   = 100000
		dim = 2048
	)
	rows := make([]EmbeddingRow, n)
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := 0; j < dim; j++ {
			v[j] = float32((i + j) % 7)
		}
		rows[i] = EmbeddingRow{ImageID: int32(i), Provider: "p", ModelID: "m", Dim: dim, Vector: v}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewVectorStore()
		s.BuildFromRows(rows)
	}
}
