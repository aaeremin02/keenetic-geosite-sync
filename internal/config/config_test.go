package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadExpandsV2FlyNamesAndKeepsPlainSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const input = `{
	  "ipv6": false,
	  "lists": [
	    {"source":"v2fly","lists":["youtube","google-play"]},
	    {"source":"plain","name":"Подсети Telegram","url":"https://example.net/cidr.txt"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	want := []List{
		{Name: "Youtube", Source: SourceV2Fly, URL: v2flyURL + "youtube"},
		{Name: "Google-play", Source: SourceV2Fly, URL: v2flyURL + "google-play"},
		{Name: "Подсети Telegram", Source: SourcePlain, URL: "https://example.net/cidr.txt"},
	}
	if err != nil || cfg.Prefix != "kgs-" || cfg.IPv6 || !slices.Equal(cfg.Lists, want) {
		t.Fatalf("configured lists = %+v, prefix = %q, IPv6 = %t, error = %v", cfg.Lists, cfg.Prefix, cfg.IPv6, err)
	}

	duplicate := strings.Replace(input, "Подсети Telegram", "YouTube", 1)
	if err := os.WriteFile(path, []byte(duplicate), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate group name") {
		t.Fatalf("duplicate derived group must fail validation: %v", err)
	}
}

func TestLoadEnforcesKeeneticGroupLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, value := range []string{"300", "301"} {
		if err := os.WriteFile(path, []byte(`{"maxEntriesPerGroup":`+value+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if value == "300" {
			if err != nil || cfg.MaxEntriesPerGroup != 300 {
				t.Fatalf("300 should be accepted: max=%d, error=%v", cfg.MaxEntriesPerGroup, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "maxEntriesPerGroup") {
			t.Fatalf("301 should be rejected by the config loader, got %v", err)
		}
	}
}
