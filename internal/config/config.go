package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	Prefix             string `json:"prefix"`
	RouteInterface     string `json:"routeInterface"`
	TimeoutMS          int    `json:"timeoutMs"`
	Retries            int    `json:"retries"`
	MaxEntriesPerGroup int    `json:"maxEntriesPerGroup"`
	DryRun             bool   `json:"dryRun"`
	Lists              []List `json:"-"`
	IPv6               bool   `json:"ipv6"`
}

type List struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	URL    string `json:"url"`
}

type fileConfig struct {
	Config
	Lists []listSource `json:"lists"`
}

type listSource struct {
	Source string   `json:"source"`
	Lists  []string `json:"lists"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
}

const (
	SourceV2Fly = "v2fly"
	SourcePlain = "plain"

	maxKeeneticGroupEntries = 300

	v2flyURL = "https://raw.githubusercontent.com/v2fly/domain-list-community/master/data/"
)

var (
	identifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	v2flyList  = regexp.MustCompile(`^[a-z0-9!_-]+$`)
)

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return Config{}, fmt.Errorf("%s: expected a single JSON object", path)
	}

	file := fileConfig{Config: Config{
		Prefix:             "domain-list",
		TimeoutMS:          20000,
		Retries:            3,
		MaxEntriesPerGroup: maxKeeneticGroupEntries,
		IPv6:               true,
	}}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return Config{}, fmt.Errorf("%s: expected a single JSON object", path)
	}
	if err := file.Config.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	file.Config.Lists, err = expandLists(file.Lists)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return file.Config, nil
}

func (cfg Config) validate() error {
	if !identifier.MatchString(cfg.Prefix) {
		return fmt.Errorf("prefix must contain only letters, digits, underscores or hyphens")
	}
	if cfg.RouteInterface != "" && !identifier.MatchString(cfg.RouteInterface) {
		return fmt.Errorf("routeInterface must contain only letters, digits, underscores or hyphens")
	}
	if cfg.TimeoutMS < 1 || int64(cfg.TimeoutMS) > math.MaxInt64/int64(time.Millisecond) {
		return fmt.Errorf("timeoutMs must be a positive duration in milliseconds")
	}
	if cfg.Retries < 1 {
		return fmt.Errorf("retries must be positive")
	}
	if cfg.MaxEntriesPerGroup < 1 || cfg.MaxEntriesPerGroup > maxKeeneticGroupEntries {
		return fmt.Errorf("maxEntriesPerGroup must be between 1 and %d", maxKeeneticGroupEntries)
	}
	return nil
}

func expandLists(sources []listSource) ([]List, error) {
	var lists []List
	seen := make(map[string]bool)
	for i, source := range sources {
		expanded, err := source.expand()
		if err != nil {
			return nil, fmt.Errorf("lists[%d]: %w", i, err)
		}
		for _, list := range expanded {
			key := strings.ToLower(list.Name)
			if seen[key] {
				return nil, fmt.Errorf("lists[%d]: duplicate group name %q", i, list.Name)
			}
			seen[key] = true
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (source listSource) expand() ([]List, error) {
	switch source.Source {
	case SourceV2Fly:
		if len(source.Lists) == 0 || source.Name != "" || source.URL != "" {
			return nil, fmt.Errorf("v2fly requires a non-empty lists array")
		}
		lists := make([]List, 0, len(source.Lists))
		for _, raw := range source.Lists {
			slug := strings.ToLower(strings.TrimSpace(raw))
			if !v2flyList.MatchString(slug) {
				return nil, fmt.Errorf("invalid v2fly list name %q", raw)
			}
			title := strings.ToUpper(slug[:1]) + slug[1:]
			lists = append(lists, List{Name: title, Source: SourceV2Fly, URL: v2flyURL + url.PathEscape(slug)})
		}
		return lists, nil
	case SourcePlain:
		if source.Lists != nil {
			return nil, fmt.Errorf("plain uses name and url")
		}
		name := strings.TrimSpace(source.Name)
		if name == "" || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, fmt.Errorf("name must be a non-empty group description")
		}
		u, err := url.Parse(source.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("url must be an absolute HTTPS URL")
		}
		return []List{{Name: name, Source: SourcePlain, URL: source.URL}}, nil
	default:
		return nil, fmt.Errorf("unknown source %q", source.Source)
	}
}
