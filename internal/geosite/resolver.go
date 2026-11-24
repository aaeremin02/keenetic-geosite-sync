package geosite

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type Result struct {
	Domains        []string
	SkippedKeyword int
	SkippedRegexp  int
}

type Resolver struct {
	load     func(context.Context, string) ([]byte, error)
	resolved map[string]map[string]entry
}

func NewResolver(load func(context.Context, string) ([]byte, error)) *Resolver {
	return &Resolver{load: load, resolved: make(map[string]map[string]entry)}
}

type entry struct {
	kind  string
	value string
	attrs []string
}

type include struct {
	name string
	must []string
	ban  []string
}

var (
	listName = regexp.MustCompile(`^[a-z0-9!_-]+$`)
	attrName = regexp.MustCompile(`^[a-z0-9!_-]+$`)
	domain   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)*$`)
)

func (r *Resolver) Resolve(ctx context.Context, name string) (Result, error) {
	entries, err := r.resolve(ctx, name, nil)
	if err != nil {
		return Result{}, err
	}
	var result Result
	domains := make(map[string]bool)
	for _, entry := range entries {
		switch entry.kind {
		case "keyword":
			result.SkippedKeyword++
		case "regexp":
			result.SkippedRegexp++
		default:
			domains[entry.value] = true
		}
	}
	for name := range domains {
		result.Domains = append(result.Domains, name)
	}
	sort.Strings(result.Domains)
	return result, nil
}

func (r *Resolver) resolve(ctx context.Context, name string, stack []string) (map[string]entry, error) {
	if cached, ok := r.resolved[name]; ok {
		return cached, nil
	}
	if slices.Contains(stack, name) {
		return nil, fmt.Errorf("include cycle: %s -> %s", strings.Join(stack, " -> "), name)
	}
	if len(stack) >= 64 {
		return nil, fmt.Errorf("include nesting exceeds 64 levels at %s", name)
	}

	body, err := r.load(ctx, name)
	if err != nil {
		return nil, err
	}
	entries, includes, err := parse(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	parent, err := url.Parse(name)
	if err != nil {
		return nil, fmt.Errorf("invalid list URL: %w", err)
	}
	for _, inc := range includes {
		endpoint := parent.ResolveReference(&url.URL{Path: inc.name}).String()
		child, err := r.resolve(ctx, endpoint, append(stack, name))
		if err != nil {
			return nil, err
		}
		for key, rule := range child {
			if matches(rule.attrs, inc) {
				entries[key] = rule
			}
		}
	}
	r.resolved[name] = entries
	return entries, nil
}

func matches(attrs []string, inc include) bool {
	for _, required := range inc.must {
		if !slices.Contains(attrs, required) {
			return false
		}
	}
	for _, excluded := range inc.ban {
		if slices.Contains(attrs, excluded) {
			return false
		}
	}
	return true
}

func parse(body []byte) (map[string]entry, []include, error) {
	entries := make(map[string]entry)
	var includes []include
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	for line := 1; scanner.Scan(); line++ {
		text, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}

		kind, value, typed := strings.Cut(fields[0], ":")
		tags := fields[1:]
		if !typed {
			kind, value = "domain", kind
		} else if value == "" && len(tags) > 0 {
			value, tags = tags[0], tags[1:]
		}
		kind = strings.ToLower(kind)
		if kind == "include" {
			inc, err := parseInclude(strings.ToLower(value), tags)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", line, err)
			}
			includes = append(includes, inc)
			continue
		}

		rule, err := parseEntry(kind, value, tags)
		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", line, err)
		}
		key := rule.kind + ":" + rule.value + " " + strings.Join(rule.attrs, " ")
		entries[key] = rule
	}
	return entries, includes, scanner.Err()
}

func parseInclude(name string, tags []string) (include, error) {
	if !listName.MatchString(name) {
		return include{}, fmt.Errorf("invalid include target %q", name)
	}
	inc := include{name: name}
	for _, tag := range tags {
		attr, ok := strings.CutPrefix(strings.ToLower(tag), "@")
		excluded := strings.HasPrefix(attr, "-") || strings.HasPrefix(attr, "!")
		if excluded {
			attr = attr[1:]
		}
		if !ok || !attrName.MatchString(attr) {
			return include{}, fmt.Errorf("invalid include filter %q", tag)
		}
		if excluded {
			inc.ban = append(inc.ban, attr)
		} else {
			inc.must = append(inc.must, attr)
		}
	}
	return inc, nil
}

func parseEntry(kind, value string, tags []string) (entry, error) {
	if kind != "regexp" {
		value = strings.ToLower(value)
	}
	switch kind {
	case "domain", "full":
		if !ValidDomain(value) {
			return entry{}, fmt.Errorf("invalid domain %q", value)
		}
	case "regexp", "keyword":
		if value == "" {
			return entry{}, fmt.Errorf("missing value for %s", kind)
		}
	default:
		return entry{}, fmt.Errorf("unsupported rule type %q", kind)
	}

	rule := entry{kind: kind, value: value}
	for _, tag := range tags {
		// Global affiliations require indexing the entire upstream data directory.
		if _, affiliation := strings.CutPrefix(tag, "&"); affiliation {
			continue
		}
		attr, ok := strings.CutPrefix(strings.ToLower(tag), "@")
		attr, _, _ = strings.Cut(attr, "=")
		if !ok || !attrName.MatchString(attr) {
			return entry{}, fmt.Errorf("invalid entry attribute %q", tag)
		}
		rule.attrs = append(rule.attrs, attr)
	}
	sort.Strings(rule.attrs)
	rule.attrs = slices.Compact(rule.attrs)
	return rule, nil
}

func ValidDomain(value string) bool {
	if len(value) > 253 || !domain.MatchString(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}
