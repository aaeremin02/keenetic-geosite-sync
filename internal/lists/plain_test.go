package lists

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestPlainListNormalizesEntriesAndFiltersIPv6(t *testing.T) {
	body := []byte("\xef\xbb\xbf# mixed list\r\n" +
		"192.0.2.129/24 # canonicalize the network\n192.0.2.0/24\n" +
		"198.51.100.25/32\n198.51.100.25\n" +
		"2001:DB8::beef/64\n::ffff:192.0.2.10\nExample.COM.\n")
	wantIPv4 := []string{"192.0.2.0/24", "198.51.100.25", "example.com"}

	filtered, err := parsePlain(body, false)
	if err != nil || filtered.SkippedIPv6 != 2 || !slices.Equal(filtered.Entries, wantIPv4) {
		t.Fatalf("IPv4 and domain result = %+v, error = %v", filtered, err)
	}

	all, err := parsePlain(body, true)
	wantAll := append(slices.Clone(wantIPv4), "2001:db8::/64", "::ffff:192.0.2.10")
	sort.Strings(wantAll)
	if err != nil || all.SkippedIPv6 != 0 || !slices.Equal(all.Entries, wantAll) {
		t.Fatalf("mixed result = %+v, error = %v", all, err)
	}

	_, err = parsePlain([]byte("192.0.2.0/24\n192.0.2.0/33\n"), false)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("malformed CIDR should reject the whole list: %v", err)
	}
}
