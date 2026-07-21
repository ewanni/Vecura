package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"strings"

	"vecura/internal/api"
	"vecura/internal/db"
	"vecura/internal/models"
	"vecura/internal/scan"
	"vecura/internal/vector"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// assets is provided by the frontend build (Stage 7).
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	appDir := filepath.Join(home, ".vecura")
	_ = os.MkdirAll(appDir, 0o755)
	dbPath := filepath.Join(appDir, "gallery.db")
	thumbDir := filepath.Join(appDir, "thumbnails")

	d, err := db.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()

	store := vector.NewVectorStore()
	registry := models.NewRegistry(0)
	pipeline := scan.NewPipeline(d, registry, store, thumbDir)
	if err := pipeline.BuildFromDB(); err != nil {
		log.Fatal(err)
	}

	app := api.NewApp(d, pipeline, store, registry, thumbDir)

	// Load a local .env file (if present) so OPENROUTER_API_KEY and
	// friends are visible via os.Getenv. System env vars always win.
	loadEnvFile(".env")

	err = wails.Run(&options.App{
		Title:            "Vecura",
		Width:            1100,
		Height:           760,
		MinWidth:         880,
		MinHeight:        600,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Windows: &windows.Options{
			// Acrylic backdrop gives the vibrant, blurred material that
			// shows through the transparent webview (Win11 22621+).
			BackdropType:                      windows.Acrylic,
			WebviewIsTransparent:              true,
			WindowIsTranslucent:               true,
			DisableFramelessWindowDecorations: false,
		},
		OnStartup: app.Startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// loadEnvFile reads KEY=VALUE lines from path (ignoring comments and
// blank lines) and exports them into the process environment, but only when
// the key is not already set. This keeps system env vars authoritative.
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // no .env is fine
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if k == "" || os.Getenv(k) != "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
}
