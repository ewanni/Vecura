package scan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseA1111(t *testing.T) {
	raw := "hilda_boreas_greyrat,((skindentation)),side view\n" +
		"Negative prompt: aBase:bad quality,worst quality\n" +
		"masterpiece,ultra-HD,cinematic lighting\n" +
		"Steps: 25, Sampler: Euler a, CFG scale: 5, Seed: 3221058437, Size: 832x1216, " +
		"Lora hashes: \"dra_v0.2_cwhj: 00b12d1d4ac9, Hilda_Boreas_Greyrat: 876fae916131\", Version: f2.0.1"

	p := ParsePrompt(raw)
	if p.Positive == "" {
		t.Fatal("positive prompt missing")
	}
	if !strings.Contains(p.Positive, "hilda_boreas_greyrat") {
		t.Fatalf("positive wrong: %q", p.Positive)
	}
	if !strings.Contains(p.Negative, "aBase:bad quality") {
		t.Fatalf("negative wrong: %q", p.Negative)
	}
	// Machine settings must NOT leak into the embedding text.
	embed := p.EmbeddingText()
	for _, leak := range []string{"Negative prompt:", "Steps: 25", "Sampler:", "Seed:", "Lora hashes:"} {
		if strings.Contains(embed, leak) {
			t.Fatalf("embedding text leaked %q: %q", leak, embed)
		}
	}
	if p.Params["Sampler"] != "Euler a" {
		t.Fatalf("Sampler param wrong: %q", p.Params["Sampler"])
	}
	if p.Params["CFG scale"] != "5" {
		t.Fatalf("CFG scale param wrong: %q", p.Params["CFG scale"])
	}
	// Quoted comma-containing value must survive quote-aware parsing.
	if got := p.Params["Lora hashes"]; !strings.Contains(got, "00b12d1d4ac9") || !strings.Contains(got, "876fae916131") {
		t.Fatalf("Lora hashes param wrong: %q", got)
	}
	if p.Params["Version"] != "f2.0.1" {
		t.Fatalf("Version param wrong: %q", p.Params["Version"])
	}
}

func TestParseA1111NoNegative(t *testing.T) {
	p := ParsePrompt("a simple positive prompt, with commas")
	if p.Positive != "a simple positive prompt, with commas" {
		t.Fatalf("positive wrong: %q", p.Positive)
	}
	if p.Negative != "" {
		t.Fatalf("negative should be empty: %q", p.Negative)
	}
}

func TestParseComfyUI(t *testing.T) {
	raw := `{
	  "nodes": [
	    {"class_type": "CLIPTextEncode", "inputs": {"text": "a cat sitting on a chair, masterpiece"}},
	    {"class_type": "CLIPTextEncode", "inputs": {"text": "ugly, blurry, worst quality"}},
	    {"class_type": "KSampler", "inputs": {}}
	  ]
	}`
	p := ParsePrompt(raw)
	if p.Positive == "" {
		t.Fatal("comfy positive missing")
	}
	if !strings.Contains(p.Positive, "cat sitting on a chair") {
		t.Fatalf("comfy positive wrong: %q", p.Positive)
	}
	if !strings.Contains(p.Negative, "ugly") {
		t.Fatalf("comfy negative wrong: %q", p.Negative)
	}
}

func TestExtractPromptPartsFromPNG(t *testing.T) {
	params := "hilda_boreas_greyrat,((skindentation))\n" +
		"Negative prompt: aBase:bad quality\n" +
		"Steps: 25, Sampler: Euler a, Seed: 3221058437"

	var b bytes.Buffer
	b.Write(pngSig)
	b.Write(pngChunk("IHDR", make([]byte, 13)))
	b.Write(pngChunk("tEXt", append([]byte("parameters\x00"), []byte(params)...)))
	b.Write(pngChunk("IEND", nil))

	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	parts, ok := ExtractPromptParts(p)
	if !ok {
		t.Fatal("ExtractPromptParts returned false")
	}
	if !strings.Contains(parts.Positive, "hilda_boreas_greyrat") {
		t.Fatalf("positive wrong: %q", parts.Positive)
	}
	if !strings.Contains(parts.Negative, "aBase:bad quality") {
		t.Fatalf("negative wrong: %q", parts.Negative)
	}
	// The full raw must still be available for display.
	if !strings.Contains(parts.Raw, "Steps: 25") {
		t.Fatalf("raw missing params: %q", parts.Raw)
	}
}
