package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	app := &App{configPath: cfgPath}

	err := app.SaveSettings(SaveSettingsReq{
		Provider:      "ollama",
		BaseURL:       "http://localhost:11434/v1",
		APIKey:        "sk-test-secret",
		SelectedModel: "nomic-embed-text",
		FolderPath:    "/some/images/folder",
	})
	if err != nil {
		t.Fatalf("SaveSettings returned error: %v", err)
	}

	// Verify the file was actually written to disk.
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		t.Fatalf("config.json not written to disk: %v", statErr)
	}
	data, _ := os.ReadFile(cfgPath)
	t.Logf("config.json on disk: %s", data)

	// Fresh app instance (no in-memory cfg) must read it back from disk.
	app2 := &App{configPath: cfgPath}
	cfg, err := app2.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if cfg.FolderPath != "/some/images/folder" {
		t.Fatalf("FolderPath mismatch: got %q", cfg.FolderPath)
	}
	if cfg.Provider != "ollama" {
		t.Fatalf("Provider mismatch: got %q", cfg.Provider)
	}
	if cfg.SelectedModel != "nomic-embed-text" {
		t.Fatalf("SelectedModel mismatch: got %q", cfg.SelectedModel)
	}
	prov := cfg.Providers["ollama"]
	if prov.BaseURL != "http://localhost:11434/v1" || prov.APIKey != "sk-test-secret" {
		t.Fatalf("Providers[ollama] mismatch: %+v (map=%+v)", prov, cfg.Providers)
	}
}
