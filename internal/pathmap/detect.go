package pathmap

import (
	"context"
	"encoding/json"
	"strings"
)

// Runner executes an external command and returns its stdout. Injected so
// docker calls are testable without a docker daemon.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type inspectContainer struct {
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

// BindRulesFromInspect turns `docker inspect` JSON into path rules: one
// {From: container Destination, To: host Source} per bind mount rooted at
// /mnt/ (the Unraid array/user shares). /config, /tmp, and non-bind mounts
// are ignored - they are not media libraries.
func BindRulesFromInspect(inspectJSON []byte) ([]Rule, error) {
	var containers []inspectContainer
	if err := json.Unmarshal(inspectJSON, &containers); err != nil {
		return nil, err
	}
	var rules []Rule
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type != "bind" || !strings.HasPrefix(m.Source, "/mnt/") {
				continue
			}
			if m.Destination == "/config" || m.Destination == "/tmp" {
				continue
			}
			rules = append(rules, Rule{From: m.Destination, To: m.Source})
		}
	}
	return rules, nil
}

// DetectDockerRules finds the first running container whose image name contains
// one of imageSubstrings, inspects it, and returns its bind-mount rules. A
// missing container yields (nil, nil): auto-detection is best-effort.
func DetectDockerRules(ctx context.Context, run Runner, imageSubstrings []string) ([]Rule, error) {
	out, err := run(ctx, "docker", "ps", "--format", "{{.Names}} {{.Image}}")
	if err != nil {
		return nil, err
	}
	name := matchContainer(string(out), imageSubstrings)
	if name == "" {
		return nil, nil
	}
	inspect, err := run(ctx, "docker", "inspect", name)
	if err != nil {
		return nil, err
	}
	return BindRulesFromInspect(inspect)
}

// matchContainer returns the container name of the first "name image" line whose
// image contains any of subs (case-insensitive), or "".
func matchContainer(psOutput string, subs []string) string {
	for _, line := range strings.Split(strings.TrimSpace(psOutput), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		image := strings.ToLower(fields[1])
		for _, s := range subs {
			if strings.Contains(image, strings.ToLower(s)) {
				return fields[0]
			}
		}
	}
	return ""
}
