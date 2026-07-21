package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"vecura/internal/scan"
)

func main() {
	dir := "data"
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "readdir:", err)
		os.Exit(1)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		switch ext {
		case ".jpeg", ".jpg", ".png", ".webp", ".bmp", ".gif":
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	if len(files) > 10 {
		files = files[:10]
	}

	type rec struct {
		File    string           `json:"file"`
		Raw     string           `json:"raw,omitempty"`
		HasMeta bool             `json:"hasMeta"`
		Parts   *scan.Prompt     `json:"parts,omitempty"`
	}
	out := make([]rec, 0, len(files))
	for _, f := range files {
		raw := scan.ExtractPrompt(f)
		parts, ok := scan.ExtractPromptParts(f)
		r := rec{File: f, Raw: raw, HasMeta: ok}
		if ok {
			p := parts
			r.Parts = &p
		}
		out = append(out, r)
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("mdata.json", b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote mdata.json with %d files\n", len(out))
}
