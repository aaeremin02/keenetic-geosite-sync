package geosite

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestResolverFiltersNestedIncludesAndDetectsCycles(t *testing.T) {
	const first = "https://mirror.example/geosite/data/"
	const second = "https://mirror.example/another/data/"
	lists := map[string]string{
		first + "selected?raw=1": "include:nested @ads @video @-blocked\n",
		first + "nested":         "include:video\nblocked.example @ads @video @blocked\n",
		first + "video":          "plain.example\nads.example @ads\nvideo.example @video\nselected.example @ads @video\n",
		first + "loop":           "include:loop-child\n",
		first + "loop-child":     "include:loop\n",
		second + "selected":      "include:nested\n",
		second + "nested":        "another.example\n",
	}
	resolver := NewResolver(func(_ context.Context, name string) ([]byte, error) {
		if value, exists := lists[name]; exists {
			return []byte(value), nil
		}
		return nil, fmt.Errorf("missing fixture %s", name)
	})

	result, err := resolver.Resolve(t.Context(), first+"selected?raw=1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Domains, []string{"selected.example"}) {
		t.Fatalf("filtered domains = %v, want [selected.example]", result.Domains)
	}

	result, err = resolver.Resolve(t.Context(), second+"selected")
	if err != nil || !slices.Equal(result.Domains, []string{"another.example"}) {
		t.Fatalf("different path must resolve its own include: %+v, %v", result, err)
	}

	_, err = resolver.Resolve(t.Context(), first+"loop")
	if err == nil || !strings.Contains(err.Error(), "include cycle: "+first+"loop -> "+first+"loop-child -> "+first+"loop") {
		t.Fatalf("cycle error = %v", err)
	}
}
