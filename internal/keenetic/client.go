package keenetic

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var commandError = regexp.MustCompile(`(?i)(?:^|\s)error(?:\[[0-9]+\]|:)|(?m)^\s*(?:unknown command|invalid command)\b`)

type Client struct{}

func (Client) ReadRunningConfig(ctx context.Context) (State, error) {
	output, err := run(ctx, "show running-config", false)
	if err != nil {
		return State{}, err
	}
	if strings.TrimSpace(output) == "" {
		return State{}, fmt.Errorf("ndmc returned an empty running configuration")
	}
	return ParseRunningConfig(output), nil
}

func (Client) RunCommand(ctx context.Context, command string) error {
	_, err := run(ctx, command, true)
	return err
}

func run(ctx context.Context, command string, showReply bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "ndmc", "-c", command).CombinedOutput()
	reply := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		return "", fmt.Errorf("ndmc %q: %w", command, ctx.Err())
	}
	check := reply
	if !showReply {
		check, _, _ = strings.Cut(check, "\n")
	}
	if err == nil && commandError.MatchString(check) {
		err = fmt.Errorf("KeeneticOS rejected the command")
	}
	if err != nil {
		if showReply && reply != "" {
			reply = strings.Join(strings.Fields(reply), " ")
			return "", fmt.Errorf("ndmc %q: %w (%s)", command, err, reply)
		}
		return "", fmt.Errorf("ndmc %q: %w", command, err)
	}
	return string(output), nil
}
