package keenetic

import (
	"crypto/sha256"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type Fingerprint [sha256.Size]byte

type State struct {
	Groups      map[string]Group
	Routes      map[string][]Route
	Fingerprint Fingerprint
}

type Group struct {
	Description string
	Domains     []string
	HasExcludes bool
}

type Route struct {
	Interface string
	Gateway   string
	Options   []string
	Disabled  bool
}

func (route Route) Equal(other Route) bool {
	return route.Interface == other.Interface && route.Gateway == other.Gateway && route.Disabled == other.Disabled &&
		slices.Equal(route.Options, other.Options)
}

func ParseRunningConfig(text string) State {
	state := State{Groups: make(map[string]Group), Routes: make(map[string][]Route)}
	hash := sha256.New()
	var groupName, lastRouteGroup string
	var group Group
	inDNSProxy := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(strings.TrimSpace(raw), ",")
		decoded, err := strconv.Unquote(line)
		if err == nil && strings.HasPrefix(line, `"`) {
			line = decoded
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		indented := strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") ||
			strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		line = strings.TrimSpace(line)
		// Ignore informational comments, but retain nesting and command order.
		if !strings.HasPrefix(line, "!") || line == "!" {
			hash.Write([]byte(strings.TrimRight(raw, " \t\r") + "\n"))
		}
		command := strings.ToLower(fields[0])

		// A top-level command or block separator closes the current group.
		if groupName != "" && (!indented || command == "!") {
			state.Groups[groupName] = group
			groupName = ""
		}
		if command == "!" {
			inDNSProxy = false
			lastRouteGroup = ""
			continue
		}
		if len(fields) >= 3 && command == "object-group" && strings.EqualFold(fields[1], "fqdn") {
			if groupName != "" {
				state.Groups[groupName] = group
			}
			groupName, group = fields[2], Group{}
			inDNSProxy = false
			lastRouteGroup = ""
			continue
		}
		if groupName != "" {
			switch command {
			case "description":
				value := strings.TrimSpace(line[len(fields[0]):])
				if decoded, err := strconv.Unquote(value); err == nil && strings.HasPrefix(value, `"`) {
					value = decoded
				}
				group.Description = value
			case "exclude":
				group.HasExcludes = true
			case "include":
				if len(fields) == 2 {
					group.Domains = append(group.Domains, strings.ToLower(fields[1]))
				}
			}
			continue
		}

		if command == "dns-proxy" {
			inDNSProxy = len(fields) == 1
			if inDNSProxy {
				lastRouteGroup = ""
				continue
			}
			fields = fields[1:]
		} else if !indented || !inDNSProxy {
			inDNSProxy = false
			lastRouteGroup = ""
			continue
		}

		if len(fields) < 2 || !strings.EqualFold(fields[0], "route") {
			continue
		}
		switch {
		case len(fields) >= 4 && strings.EqualFold(fields[1], "object-group"):
			name := fields[2]
			route := Route{Interface: fields[3]}
			options := fields[4:]
			if _, err := netip.ParseAddr(route.Interface); err == nil {
				route.Gateway, route.Interface = route.Interface, ""
				if len(options) > 0 && !isRouteOption(options[0]) {
					route.Interface, options = options[0], options[1:]
				}
			}
			for _, option := range options {
				switch strings.ToLower(option) {
				case "disable", "disabled":
					route.Disabled = true
				default:
					route.Options = append(route.Options, option)
				}
			}
			state.Routes[name] = append(state.Routes[name], route)
			lastRouteGroup = name
		case strings.EqualFold(fields[1], "disable") && lastRouteGroup != "":
			routes := state.Routes[lastRouteGroup]
			routes[len(routes)-1].Disabled = true
			lastRouteGroup = ""
		default:
			lastRouteGroup = ""
		}
	}

	if groupName != "" {
		state.Groups[groupName] = group
	}
	copy(state.Fingerprint[:], hash.Sum(nil))
	return state
}

func isRouteOption(value string) bool {
	switch strings.ToLower(value) {
	case "auto", "reject", "disable", "disabled":
		return true
	default:
		return false
	}
}
