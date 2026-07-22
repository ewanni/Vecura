package vector

import (
	"math"
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// invNorm returns 1/|v| (precomputed denominator for cosine similarity).
func invNorm(v []float32) float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum <= 0 {
		return 0
	}
	return 1 / float32(math.Sqrt(float64(sum)))
}

func dotProd(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
