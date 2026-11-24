package lists

import (
	"context"
	"fmt"
	"time"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/geosite"
)

type Result struct {
	Entries        []string
	SkippedKeyword int
	SkippedRegexp  int
	SkippedIPv6    int
}

type Resolver struct {
	geosite *geosite.Resolver
	load    func(context.Context, string) ([]byte, error)
	ipv6    bool
}

func NewResolver(cfg config.Config) (*Resolver, error) {
	client, err := newDownloader(time.Duration(cfg.TimeoutMS)*time.Millisecond, cfg.Retries)
	if err != nil {
		return nil, err
	}
	return &Resolver{geosite: geosite.NewResolver(client.load), load: client.load, ipv6: cfg.IPv6}, nil
}

func (r *Resolver) Resolve(ctx context.Context, source config.List) (Result, error) {
	switch source.Source {
	case config.SourceV2Fly:
		result, err := r.geosite.Resolve(ctx, source.URL)
		return Result{
			Entries:        result.Domains,
			SkippedKeyword: result.SkippedKeyword,
			SkippedRegexp:  result.SkippedRegexp,
		}, err
	case config.SourcePlain:
		body, err := r.load(ctx, source.URL)
		if err != nil {
			return Result{}, err
		}
		return parsePlain(body, r.ipv6)
	default:
		return Result{}, fmt.Errorf("unknown source %q", source.Source)
	}
}
