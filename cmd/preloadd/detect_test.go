package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
)

func TestRunDetectPathmapsJSON(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "ps" {
			return []byte("emby emby/embyserver\n"), nil
		}
		return []byte(`[{"Mounts":[{"Type":"bind","Source":"/mnt/user/Movies","Destination":"/share/Movies"}]}]`), nil
	}
	manual := []config.PathRule{{From: "/x", To: "/mnt/user/x"}}
	var buf bytes.Buffer
	if err := runDetectPathmaps(context.Background(), manual, run, &buf); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Rules []struct{ From, To, Source string } `json:"rules"`
		UNC   bool                                `json:"unraid_unc_fallback"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if !out.UNC {
		t.Error("expected unraid_unc_fallback=true")
	}
	if len(out.Rules) != 2 || out.Rules[0].Source != "manual" || out.Rules[1].Source != "docker" {
		t.Errorf("unexpected rules: %+v", out.Rules)
	}
}

func TestConfigPathFromArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "config.toml"},
		{"space form", []string{"-config", "/boot/config/plugins/wap/config.toml"}, "/boot/config/plugins/wap/config.toml"},
		{"equals form", []string{"-config=/etc/wap.toml"}, "/etc/wap.toml"},
		{"unknown trailing flag does not abort", []string{"-config", "/tmp/c.toml", "-bogus"}, "/tmp/c.toml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := configPathFromArgs(c.args); got != c.want {
				t.Errorf("configPathFromArgs(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}
