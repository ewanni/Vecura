package scan

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"vecura/internal/db"
	"vecura/internal/embedder"
	"vecura/internal/models"
	"vecura/internal/vector"
)

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true, ".gif": true,
}

// Progress reports scan progress to subscribers.
type Progress struct {
	Total    int
	Done     int
	Current  string
	Finished bool
}

// Pipeline ties together db, embedder registry and the vector store.
type Pipeline struct {
	db       *sql.DB
	imgRepo  *db.ImageRepo
	embRepo  *db.EmbeddingRepo
	models   *models.Registry
	store    *vector.VectorStore
	cache    *embedder.Cache
	thumbDir string

	progMu   sync.Mutex
	progress Progress

	dbMu sync.Mutex // serializes all DB writes (SQLite is single-writer)

	subMu sync.Mutex
	sub   map[chan Progress]struct{}
}

// NewPipeline builds a scan pipeline.
func NewPipeline(d *sql.DB, m *models.Registry, s *vector.VectorStore, thumbDir string) *Pipeline {
	return &Pipeline{
		db:       d,
		imgRepo:  db.NewImageRepo(d),
		embRepo:  db.NewEmbeddingRepo(d),
		models:   m,
		store:    s,
		cache:    embedder.NewCache(),
		thumbDir: thumbDir,
		sub:      make(map[chan Progress]struct{}),
	}
}

// NewPipelineFromStore builds a pipeline sharing the same DB/models but a
// different vector store (used to rebuild an independent store from DB).
func NewPipelineFromStore(p *Pipeline, s *vector.VectorStore) *Pipeline {
	return &Pipeline{
		db:       p.db,
		imgRepo:  p.imgRepo,
		embRepo:  p.embRepo,
		models:   p.models,
		store:    s,
		cache:    p.cache,
		thumbDir: p.thumbDir,
		sub:      make(map[chan Progress]struct{}),
	}
}

// Subscribe registers a channel for progress updates.
func (p *Pipeline) Subscribe(ch chan Progress) {
	p.subMu.Lock()
	p.sub[ch] = struct{}{}
	p.subMu.Unlock()
}

// Unsubscribe removes a progress channel.
func (p *Pipeline) Unsubscribe(ch chan Progress) {
	p.subMu.Lock()
	delete(p.sub, ch)
	p.subMu.Unlock()
}

// ImageRepo exposes the underlying image repository for the API layer.
func (p *Pipeline) ImageRepo() *db.ImageRepo { return p.imgRepo }

func (p *Pipeline) emit() {
	p.progMu.Lock()
	cur := p.progress
	p.progMu.Unlock()
	p.subMu.Lock()
	for ch := range p.sub {
		select {
		case ch <- cur:
		default:
		}
	}
	p.subMu.Unlock()
}

// BuildFromDB loads persisted embeddings into the store at boot.
func (p *Pipeline) BuildFromDB() error {
	rows, err := p.embRepo.LoadAllEmbeddings()
	if err != nil {
		return err
	}
	vrows := make([]vector.EmbeddingRow, len(rows))
	for i, r := range rows {
		vrows[i] = vector.EmbeddingRow{
			ImageID:  r.ImageID,
			Provider: r.Provider,
			ModelID:  r.ModelID,
			Dim:      r.Dim,
			Vector:   r.Vector,
		}
	}
	p.store.BuildFromRows(vrows)
	return nil
}

// scanWorkers bounds how many files are decoded/thumbnailed in parallel.
const scanWorkers = 8

// scanBatch is how many embeddings are sent per embed request.
const scanBatch = 32

// ScanFolder walks a folder, generating thumbnails and embeddings for new
// images using the given model key. Work is parallelized across scanWorkers
// goroutines (image decode, thumbnail generation, DB writes), while embedding
// requests are batched (scanBatch per call) and serialized via embMu so the
// pipeline stays correct for local CGO models. Already-embedded images are
// skipped, making a re-scan of the same folder nearly instant.
func (p *Pipeline) ScanFolder(ctx context.Context, root, modelKey string) error {
	e, ok := p.models.Get(modelKey)
	if !ok {
		return fmt.Errorf("model not registered: %s", modelKey)
	}

	files, err := collectImages(root)
	if err != nil {
		return err
	}
	sort.Strings(files)

	p.progMu.Lock()
	p.progress = Progress{Total: len(files), Done: 0, Finished: false}
	p.progMu.Unlock()
	p.emit()

	var embMu sync.Mutex // serializes e.Embed (safe for local CGO models)
	fileCh := make(chan string)
	var wg sync.WaitGroup
	batch := effectiveBatch(e)
	for i := 0; i < scanWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.workerScan(ctx, fileCh, e, &embMu, batch)
		}()
	}

feed:
	for _, f := range files {
		select {
		case <-ctx.Done():
			break feed
		case fileCh <- f:
		}
	}
	close(fileCh)
	wg.Wait()

	p.progMu.Lock()
	p.progress.Finished = true
	p.progMu.Unlock()
	p.emit()
	return nil
}

// workerScan pulls file paths and processes them in batches.
func (p *Pipeline) workerScan(ctx context.Context, fileCh <-chan string, e embedder.Embedder, embMu *sync.Mutex, batch int) {
	prompts := make([]string, 0, batch)
	ids := make([]int32, 0, batch)
	paths := make([]string, 0, batch)
	flush := func() {
		if len(prompts) == 0 {
			return
		}
		p.flushBatch(e, embMu, prompts, ids, paths)
		prompts = prompts[:0]
		ids = ids[:0]
		paths = paths[:0]
	}

	for f := range fileCh {
		// Count every file exactly once so progress always reaches 100%,
		// even if the file is skipped, errors, or panics below.
		p.incDone()
		if ctx.Err() != nil {
			return
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[scan] recovered panic on %s: %v", f, r)
				}
			}()
			// Always (re)generate the thumbnail so the cached image stays
			// correct — including aspect ratio fixes applied after earlier
			// scans. Decoding is best-effort and ignored on failure.
			_ = generateThumbnail(f, p.thumbDir)
			// Prefer the embedded generation prompt; fall back to the file name.
			rawPrompt := ExtractPrompt(f)
			if rawPrompt == "" {
				rawPrompt = filepath.Base(f)
			}
			// For semantic search we embed only the positive prompt. The
			// negative prompt and the machine settings (Steps/Sampler/Seed/…)
			// are noise that would corrupt similarity. The full raw blob is
			// still stored for display.
			embedText := rawPrompt
			if parts, ok := ExtractPromptParts(f); ok {
				if pt := parts.EmbeddingText(); pt != "" {
					embedText = pt
				}
			}
			prompt := rawPrompt
			p.dbMu.Lock()
			// Read the previously stored prompt BEFORE upserting so we can
			// tell whether an existing embedding was built from a weak source
			// (e.g. the file name) and now needs to be re-embedded.
			oldPrompt, oldExists, gerr := p.imgRepo.GetPromptByPath(f)
			id, uerr := p.imgRepo.UpsertImage(f, prompt)
			has, herr := false, error(nil)
			if uerr == nil {
				has, herr = p.embRepo.HasEmbedding(id, "remote", e.Key())
			}
			// Re-embed when the prompt we now have is more useful than what
			// was embedded before: first scan stored the filename (or nothing)
			// but we now extracted a real prompt, or the prompt text changed.
			needReembed := false
			if has && gerr == nil {
				switch {
				case !oldExists || oldPrompt == "" || oldPrompt == filepath.Base(f):
					needReembed = prompt != filepath.Base(f)
				case oldPrompt != prompt:
					needReembed = true
				}
			}
			if needReembed {
				if derr := p.embRepo.DeleteEmbedding(id, "remote", e.Key()); derr != nil {
					log.Printf("[scan] delete stale embedding for %s: %v", f, derr)
				}
				has = false
			}
			p.dbMu.Unlock()
			if uerr != nil {
				log.Printf("[scan] upsert failed for %s: %v", f, uerr)
				return
			}
			if herr != nil {
				log.Printf("[scan] has-embedding check failed for %s: %v", f, herr)
				return
			}
			// Incremental: skip images already embedded with this model,
			// unless we just decided to re-embed above.
			if has {
				return
			}
			if v, ok := p.cache.Get(f, embedText, e.Key()); ok {
				p.store.Add(id, "remote", e.Key(), v)
				return
			}
			prompts = append(prompts, embedText)
			ids = append(ids, id)
			paths = append(paths, f)
			if len(prompts) >= batch {
				flush()
			}
		}()
	}
	flush()
}

// effectiveBatch returns the embedding batch size for the model. For the
// OpenAI-compatible remote embedder this honors the per-model Batch setting
// (e.g. 128); other embedders fall back to scanBatch.
func effectiveBatch(e embedder.Embedder) int {
	if re, ok := e.(*embedder.OpenAICompatibleEmbedder); ok && re.Batch > 0 {
		return re.Batch
	}
	return scanBatch
}

// flushBatch embeds a batch of prompts and persists the resulting vectors.
// Progress for these files was already counted when they were pulled from the
// channel, so this function does not touch the progress counter.
func (p *Pipeline) flushBatch(e embedder.Embedder, embMu *sync.Mutex, prompts []string, ids []int32, paths []string) {
	embMu.Lock()
	vecs, err := e.Embed(prompts)
	embMu.Unlock()
	if err != nil {
		log.Printf("[scan] embed failed for %d items: %v", len(prompts), err)
		return
	}
	rows := make([]db.EmbeddingRow, 0, len(prompts))
	for i, prompt := range prompts {
		if i >= len(vecs) {
			break
		}
		vec := vecs[i]
		if len(vec) == 0 {
			continue
		}
		id := ids[i]
		rows = append(rows, db.EmbeddingRow{
			ImageID: id, Provider: "remote", ModelID: e.Key(), Dim: e.Dim(), Vector: vec,
		})
		p.cache.Put(paths[i], prompt, e.Key(), vec)
		p.store.Add(id, "remote", e.Key(), vec)
		_ = generateThumbnail(paths[i], p.thumbDir)
	}
	if len(rows) > 0 {
		p.dbMu.Lock()
		err := p.embRepo.BatchInsertEmbeddings(rows)
		p.dbMu.Unlock()
		if err != nil {
			log.Printf("[scan] persist embeddings failed: %v", err)
		}
	}
}

// incDone advances progress by one processed file.
func (p *Pipeline) incDone() {
	p.progMu.Lock()
	p.progress.Done++
	p.progMu.Unlock()
	p.emit()
}

func collectImages(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if imageExts[ext] {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// generateThumbnail creates a 256x256 thumbnail in thumbDir (pure-Go, PNG).
// Best-effort: decode/encode failures are ignored (ARCHITECTURE.md section 6.Б).
func generateThumbnail(src, thumbDir string) error {
	if thumbDir == "" {
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	resized := resizeTo(img, 256)
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		return nil
	}
	name := filepath.Base(src) + ".png"
	dst, err := os.Create(filepath.Join(thumbDir, name))
	if err != nil {
		return nil
	}
	defer dst.Close()
	_ = png.Encode(dst, resized)
	return nil
}

// resizeTo scales an image so its longest side equals n while preserving the
// original aspect ratio (nearest-neighbor). This keeps thumbnails
// rectangular instead of forcing them into a square and distorting them.
func resizeTo(src image.Image, n int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return src
	}
	longest := w
	if h > longest {
		longest = h
	}
	scale := float64(n) / float64(longest)
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)*float64(h)/float64(nh))
		for x := 0; x < nw; x++ {
			sx := b.Min.X + int(float64(x)*float64(w)/float64(nw))
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
