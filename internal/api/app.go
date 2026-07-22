package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"vecura/internal/db"
	"vecura/internal/models"
	"vecura/internal/scan"
	"vecura/internal/vector"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// defaultSearchLimit is used when the frontend does not specify K.
	defaultSearchLimit = 24
	// recentSearchLimit bounds how many past queries feed the suggestion
	// dropdown.
	recentSearchLimit = 20
	// MinWindowWidth/MinWindowHeight are the app's declared minimum window
	// size. main.go feeds the same constants into windows.Options so the
	// persisted-size restore logic below can never contradict the window's
	// actual declared minimum.
	MinWindowWidth  = 880
	MinWindowHeight = 600
)

// SearchHit is one result returned to the frontend.
type SearchHit struct {
	ID     int32   `json:"id"`
	Path   string  `json:"path"`
	Prompt string  `json:"prompt"`
	Score  float32 `json:"score"`
}

// ModelInfo describes a registered model for the frontend.
type ModelInfo struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
	Local    bool   `json:"local"`
	Dim      int    `json:"dim"`
}

// AddModelConfig registers a remote model via API.
type AddModelConfig struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
	Batch    int    `json:"batch"`
}

// App is the Wails-exposed application struct.
type App struct {
	ctx      context.Context
	db       *sql.DB
	pipeline *scan.Pipeline
	store    *vector.VectorStore
	registry *models.Registry
	history  *db.HistoryRepo

	// windowStatePath is where we persist the window size between runs.
	windowStatePath string

	// configPath is where we persist user settings between runs.
	configPath string

	// activeModel is the currently selected model key, restored on startup.
	activeModel string

	progMu sync.Mutex
	cfgMu  sync.Mutex
}

// NewApp constructs the Wails App.
func NewApp(d *sql.DB, p *scan.Pipeline, s *vector.VectorStore, reg *models.Registry, thumbDir string) *App {
	return &App{
		db:              d,
		pipeline:        p,
		store:           s,
		registry:        reg,
		history:         db.NewHistoryRepo(d),
		windowStatePath: filepath.Join(filepath.Dir(thumbDir), "window.json"),
		configPath:      filepath.Join(filepath.Dir(thumbDir), "config.json"),
	}
}

// providerCfg stores per-provider connection settings persisted to disk.
type providerCfg struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

// modelCfg stores a registered remote model so it can be re-registered
// after a restart.
type modelCfg struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
}

// appConfig is the persisted user settings blob. All fields are exported so
// Wails can serialize it to the frontend.
type appConfig struct {
	Provider      string                 `json:"provider"`
	Providers     map[string]providerCfg `json:"providers"`
	SelectedModel string                 `json:"selectedModel"`
	FetchedModels []RemoteModel          `json:"fetchedModels"`
	ActiveModel   string                 `json:"activeModel"`
	FolderPath    string                 `json:"folderPath"`
	Models        []modelCfg             `json:"models"`
}

// loadConfig reads the persisted settings, returning an empty config when no
// file exists yet.
func (a *App) loadConfig() appConfig {
	var cfg appConfig
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]providerCfg{}
	}
	return cfg
}

// saveConfig writes the settings to disk. It ensures the parent directory
// exists and logs any failure instead of silently dropping the write.
func (a *App) saveConfig(cfg appConfig) {
	if dir := filepath.Dir(a.configPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[config] mkdir %s failed: %v", dir, err)
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("[config] marshal failed: %v", err)
		return
	}
	if err := os.WriteFile(a.configPath, data, 0o644); err != nil {
		log.Printf("[config] write %s failed: %v", a.configPath, err)
	}
}

// registerModelFromCfg re-registers a saved remote model after a restart.
func (a *App) registerModelFromCfg(m modelCfg) {
	apiKey := m.APIKey
	// When the saved API key is empty (e.g. it was never persisted because
	// the key came from an env var), try the provider's saved config and
	// well-known env vars so the embedder can still authenticate.
	if apiKey == "" {
		cfg := a.loadConfig()
		if pc, ok := cfg.Providers[m.Provider]; ok && pc.APIKey != "" {
			apiKey = pc.APIKey
		}
	}
	if apiKey == "" {
		apiKey = apiKeyFromEnv(m.Provider)
	}
	e := newRemoteEmbedder(AddModelConfig{
		Provider: m.Provider,
		BaseURL:  m.BaseURL,
		APIKey:   apiKey,
		Model:    m.Model,
		Dim:      m.Dim,
		Batch:    128,
	})
	a.registry.RegisterRemote(e)
}

// SaveSettings persists the connection/selection UI state so that navigating
// away and back (or restarting) keeps the user's setup.
func (a *App) SaveSettings(req SaveSettingsReq) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg := a.loadConfig()
	cfg.Provider = req.Provider
	if cfg.Providers == nil {
		cfg.Providers = map[string]providerCfg{}
	}
	// Inherit API key from env when the frontend sends empty (the key
	// may have been auto-filled from an env var during the session).
	apiKey := req.APIKey
	if apiKey == "" {
		apiKey = apiKeyFromEnv(req.Provider)
	}
	cfg.Providers[req.Provider] = providerCfg{BaseURL: req.BaseURL, APIKey: apiKey}
	cfg.SelectedModel = req.SelectedModel
	cfg.FetchedModels = req.FetchedModels
	cfg.FolderPath = req.FolderPath
	a.saveConfig(cfg)
	return nil
}

// SaveSettingsReq carries the UI state from the frontend.
type SaveSettingsReq struct {
	Provider      string        `json:"provider"`
	BaseURL       string        `json:"baseUrl"`
	APIKey        string        `json:"apiKey"`
	SelectedModel string        `json:"selectedModel"`
	FetchedModels []RemoteModel `json:"fetchedModels"`
	FolderPath    string        `json:"folderPath"`
}

// GetConfig returns the persisted settings to the frontend on mount.
func (a *App) GetConfig() (*appConfig, error) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg := a.loadConfig()
	return &cfg, nil
}

// SetActiveModel records which registered model is currently selected.
func (a *App) SetActiveModel(key string) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg := a.loadConfig()
	cfg.ActiveModel = key
	a.activeModel = key
	a.saveConfig(cfg)
	return nil
}

// Startup stores the Wails runtime context, restores the previous window
// size, and bridges scan progress to events.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.restoreWindowSize()
	// Persist window size on every resize so the next launch matches.
	runtime.EventsOn(ctx, "resize", func(_ ...interface{}) {
		a.saveWindowSize()
	})
	ch := make(chan scan.Progress, 16)
	a.pipeline.Subscribe(ch)
	go func() {
		for p := range ch {
			runtime.EventsEmit(ctx, "scan:progress", p)
		}
	}()
	// Restore saved settings: re-register models and the active selection.
	cfg := a.loadConfig()
	for _, m := range cfg.Models {
		a.registerModelFromCfg(m)
	}
	a.activeModel = cfg.ActiveModel
}

// windowState is the persisted {width,height} blob.
type windowState struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// restoreWindowSize re-applies the size saved on the previous run.
func (a *App) restoreWindowSize() {
	data, err := os.ReadFile(a.windowStatePath)
	if err != nil {
		return // no saved size yet
	}
	var s windowState
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	if s.Width < MinWindowWidth || s.Height < MinWindowHeight {
		return // ignore implausibly small sizes
	}
	runtime.WindowSetSize(a.ctx, s.Width, s.Height)
}

// saveWindowSize writes the current window size to disk.
func (a *App) saveWindowSize() {
	w, h := runtime.WindowGetSize(a.ctx)
	if w <= 0 || h <= 0 {
		return
	}
	data, err := json.Marshal(windowState{Width: w, Height: h})
	if err != nil {
		return
	}
	_ = os.WriteFile(a.windowStatePath, data, 0o644)
}

// Search runs a hybrid search: a keyword/substring match against the stored
// prompt and file path (always available, no embedding model required) merged
// with a semantic vector search when a model is registered. Keyword matches
// rank at the top so queries like "photorealistic" reliably surface images
// whose prompt contains that word, even if the embedding model is weak or
// absent.
func (a *App) Search(query, provider, modelID string, K int, tag string) ([]SearchHit, error) {
	if K <= 0 {
		K = defaultSearchLimit
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []SearchHit{}, nil
	}

	allowed := map[int32]bool{}
	if tag != "" {
		ids, err := a.pipeline.ImageRepo().GetByTag(tag)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			allowed[id] = true
		}
	}

	// id -> best score so far.
	best := map[int32]float32{}

	// 1) Keyword / substring match (no model needed).
	kw, kerr := a.pipeline.ImageRepo().SearchByText(query, K)
	if kerr != nil {
		log.Printf("[search] keyword search failed: %v", kerr)
	}
	for _, id := range kw {
		if tag != "" && !allowed[id] {
			continue
		}
		if _, ok := best[id]; !ok {
			best[id] = 1.0 // keyword matches float to the top
		}
	}

	// 2) Semantic search when the model is registered.
	if e, ok := a.registry.Get(provider + "/" + modelID); ok {
		Q, qerr := e.Embed([]string{query})
		if qerr != nil {
			log.Printf("[search] embed query failed: %v", qerr)
		} else if len(Q) > 0 {
			_ = a.history.AddQuery(query)
			res := a.store.Search(Q[0], provider, modelID, K+len(kw))
			for _, r := range res {
				if tag != "" && !allowed[r.ID] {
					continue
				}
				if r.Score > best[r.ID] {
					best[r.ID] = r.Score
				}
			}
		}
	} else {
		// No model registered: still record the query for suggestions.
		_ = a.history.AddQuery(query)
	}

	if len(best) == 0 {
		return []SearchHit{}, nil
	}

	type scored struct {
		id    int32
		score float32
	}
	order := make([]scored, 0, len(best))
	for id, sc := range best {
		order = append(order, scored{id, sc})
	}
	sort.SliceStable(order, func(i, j int) bool {
		return order[i].score > order[j].score
	})
	if len(order) > K {
		order = order[:K]
	}

	ids := make([]int32, len(order))
	for i, s := range order {
		ids[i] = s.id
	}
	images, ierr := a.pipeline.ImageRepo().GetImagesByIDs(ids)
	if ierr != nil {
		return nil, ierr
	}

	hits := make([]SearchHit, 0, len(order))
	for _, s := range order {
		img, ok := images[s.id]
		if !ok {
			continue
		}
		hits = append(hits, SearchHit{
			ID:     s.id,
			Path:   img.Path,
			Prompt: img.Prompt,
			Score:  s.score,
		})
	}
	return hits, nil
}

// AddModel registers a remote model reachable through an OpenAI-compatible
// embeddings API.
func (a *App) AddModel(cfg AddModelConfig) (*ModelInfo, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("baseUrl and model are required")
	}
	// Inherit API key from the saved provider config when the frontend
	// sends an empty key (e.g. the key comes from an env var and was not
	// explicitly pasted by the user).
	if cfg.APIKey == "" {
		saved := a.loadConfig()
		if pc, ok := saved.Providers[cfg.Provider]; ok && pc.APIKey != "" {
			cfg.APIKey = pc.APIKey
		}
	}
	// Auto-resolve dim from known model id when not supplied.
	dim := cfg.Dim
	if dim <= 0 {
		dim = dimForModel(cfg.Model, 0)
	}
	if dim <= 0 {
		return nil, fmt.Errorf("cannot infer dimensionality for %q; please specify dim", cfg.Model)
	}
	cfg.Dim = dim
	e := newRemoteEmbedder(cfg)
	lm := a.registry.RegisterRemote(e)
	// Persist the registered model and make it the active one.
	a.cfgMu.Lock()
	saved := a.loadConfig()
	mc := modelCfg{Provider: cfg.Provider, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model, Dim: dim}
	found := false
	for i := range saved.Models {
		if saved.Models[i].Provider == cfg.Provider && saved.Models[i].Model == cfg.Model {
			saved.Models[i] = mc
			found = true
			break
		}
	}
	if !found {
		saved.Models = append(saved.Models, mc)
	}
	saved.ActiveModel = lm.Key
	a.activeModel = lm.Key
	a.saveConfig(saved)
	a.cfgMu.Unlock()
	return &ModelInfo{
		Key:      lm.Key,
		Provider: lm.Provider,
		ModelID:  lm.ModelID,
		Local:    lm.Local,
		Dim:      e.Dim(),
	}, nil
}

// ListModels returns registered models.
func (a *App) ListModels() []ModelInfo {
	loaded := a.registry.List()
	out := make([]ModelInfo, 0, len(loaded))
	for _, m := range loaded {
		dim := 0
		if m.Embedder != nil {
			dim = m.Embedder.Dim()
		}
		out = append(out, ModelInfo{
			Key:      m.Key,
			Provider: m.Provider,
			ModelID:  m.ModelID,
			Local:    m.Local,
			Dim:      dim,
		})
	}
	return out
}

// ScanFolder triggers a background scan of a folder for a given model key.
func (a *App) ScanFolder(path, modelKey string) error {
	go func() {
		_ = a.pipeline.ScanFolder(a.ctx, path, modelKey)
	}()
	return nil
}

// PickFolder opens a native directory dialog and returns the chosen path.
// Returns "" if cancelled.
func (a *App) PickFolder() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not started")
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Select image folder",
		CanCreateDirectories: true,
		ShowHiddenFiles:      false,
		ResolvesAliases:      true,
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// ImageDataURI returns a full-resolution data-URI for the given image path,
// used by the preview dialog.
func (a *App) ImageDataURI(imagePath string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	ct := mimeType(imagePath)
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func mimeType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

// RecentSearches returns recent search queries for the suggestion dropdown.
func (a *App) RecentSearches() ([]string, error) {
	return a.history.Recent(recentSearchLimit)
}

// CheckProvider validates an API key by calling the provider's /models
// endpoint. Returns the available models on success.
func (a *App) CheckProvider(baseURL, apiKey string) ([]RemoteModel, error) {
	return a.ListRemoteModels(baseURL, apiKey)
}

// RemoveModel unregisters a model by key.
func (a *App) RemoveModel(key string) error {
	a.registry.Unload(key)
	a.cfgMu.Lock()
	saved := a.loadConfig()
	kept := saved.Models[:0]
	for _, m := range saved.Models {
		if "remote/"+m.Model != key {
			kept = append(kept, m)
		}
	}
	saved.Models = kept
	if saved.ActiveModel == key {
		saved.ActiveModel = ""
		a.activeModel = ""
	}
	a.saveConfig(saved)
	a.cfgMu.Unlock()
	return nil
}

// ClearDB wipes all indexed data: images, embeddings, tags, and search
// history. The in-memory vector store is reset so search returns empty
// immediately after. Registered models and provider settings are preserved.
func (a *App) ClearDB() error {
	tables := []string{"image_tags", "tags", "embeddings", "search_history", "images"}
	for _, t := range tables {
		if _, err := a.db.Exec("DELETE FROM " + t); err != nil {
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	a.store.Reset()
	return nil
}

// GetModelInfo returns stored info for a registered model key.
func (a *App) GetModelInfo(key string) (*ModelInfo, error) {
	for _, m := range a.registry.List() {
		if m.Key == key {
			dim := 0
			if m.Embedder != nil {
				dim = m.Embedder.Dim()
			}
			return &ModelInfo{
				Key:      m.Key,
				Provider: m.Provider,
				ModelID:  m.ModelID,
				Local:    m.Local,
				Dim:      dim,
			}, nil
		}
	}
	return nil, fmt.Errorf("model not found: %s", key)
}
