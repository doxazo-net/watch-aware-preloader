package pathmap

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestBindRulesFromInspect(t *testing.T) {
	data, err := os.ReadFile("testdata/docker_inspect.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := BindRulesFromInspect(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{From: "/share/Movies", To: "/mnt/user/Movies"},
		{From: "/share/TV", To: "/mnt/user/TV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBindRulesFromInspectEmpty(t *testing.T) {
	got, err := BindRulesFromInspect([]byte(`[{"Mounts":[]}]`))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err %v, want empty", got, err)
	}
}

func TestDetectDockerRules(t *testing.T) {
	inspect, _ := os.ReadFile("testdata/docker_inspect.json")
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch args[0] {
		case "ps":
			return []byte("emby emby/embyserver:beta\nsonarr linuxserver/sonarr\n"), nil
		case "inspect":
			if args[len(args)-1] != "emby" {
				t.Fatalf("inspected wrong container: %v", args)
			}
			return inspect, nil
		}
		return nil, nil
	}
	got, err := DetectDockerRules(context.Background(), run, []string{"emby", "jellyfin"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{From: "/share/Movies", To: "/mnt/user/Movies"},
		{From: "/share/TV", To: "/mnt/user/TV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDetectDockerRulesNoContainer(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("sonarr linuxserver/sonarr\n"), nil
	}
	got, err := DetectDockerRules(context.Background(), run, []string{"emby", "jellyfin"})
	if err != nil || got != nil {
		t.Errorf("got %+v err %v, want nil,nil", got, err)
	}
}
