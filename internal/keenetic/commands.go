package keenetic

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	nameToken    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	addressToken = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
)

func CreateGroupCommand(name string) (string, error) {
	if !nameToken.MatchString(name) {
		return "", fmt.Errorf("invalid FQDN group name %q", name)
	}
	return "object-group fqdn " + name, nil
}

func DescribeGroupCommand(name, description string) (string, error) {
	group, err := CreateGroupCommand(name)
	if err != nil {
		return "", err
	}
	if description == "" || strings.ContainsFunc(description, unicode.IsControl) {
		return "", fmt.Errorf("invalid description for group %s", name)
	}
	return group + " description " + strconv.Quote(description), nil
}

func DeleteGroupCommand(name string) (string, error) {
	if !nameToken.MatchString(name) {
		return "", fmt.Errorf("invalid FQDN group name %q", name)
	}
	return "no object-group fqdn " + name, nil
}

func IncludeCommand(name, address string, remove bool) (string, error) {
	if !nameToken.MatchString(name) || !addressToken.MatchString(address) {
		return "", fmt.Errorf("invalid included address %q for FQDN group %q", address, name)
	}
	command := "object-group fqdn " + name + " include " + address
	if remove {
		command = "no " + command
	}
	return command, nil
}

func RouteCommand(name string, route Route) (string, error) {
	if !nameToken.MatchString(name) ||
		(route.Interface != "" && !nameToken.MatchString(route.Interface)) ||
		(route.Gateway == "" && route.Interface == "") {
		return "", fmt.Errorf("invalid DNS route for group %q", name)
	}
	args := []string{"dns-proxy route object-group", name}
	if route.Gateway != "" {
		if _, err := netip.ParseAddr(route.Gateway); err != nil {
			return "", fmt.Errorf("invalid DNS route gateway %q for group %s", route.Gateway, name)
		}
		args = append(args, route.Gateway)
	}
	if route.Interface != "" {
		args = append(args, route.Interface)
	}
	for _, option := range route.Options {
		if option != "auto" && option != "reject" {
			return "", fmt.Errorf("unrecognized DNS route option %q for group %s", option, name)
		}
		args = append(args, option)
	}
	return strings.Join(args, " "), nil
}
