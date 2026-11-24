package lists

import (
	"bufio"
	"bytes"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"keenetic-geosite-sync/internal/geosite"
)

var ipv4Like = regexp.MustCompile(`^[0-9.]+$`)

func parsePlain(body []byte, allowIPv6 bool) (Result, error) {
	var result Result
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	for line := 1; scanner.Scan(); line++ {
		value, _, _ := strings.Cut(scanner.Text(), "#")
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if ip, ipv6, ok := NormalizeIP(value); ok {
			if ipv6 && !allowIPv6 {
				result.SkippedIPv6++
				continue
			}
			value = ip
		} else {
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			if !geosite.ValidDomain(value) || ipv4Like.MatchString(value) {
				return Result{}, fmt.Errorf("line %d: invalid domain, IP address or CIDR", line)
			}
		}

		if !seen[value] {
			seen[value] = true
			result.Entries = append(result.Entries, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}
	sort.Strings(result.Entries)
	return result, nil
}

// NormalizeIP gives an IP or CIDR the same comparison value in a list and in the router config.
func NormalizeIP(value string) (normalized string, ipv6, ok bool) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		prefix = prefix.Masked()
		addr := prefix.Addr()
		if prefix.Bits() == addr.BitLen() {
			return addr.String(), addr.Is6(), true
		}
		return prefix.String(), addr.Is6(), true
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return addr.String(), addr.Is6(), true
	}
	return "", false, false
}
