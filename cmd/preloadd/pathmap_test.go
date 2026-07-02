package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBuildMapperUsesUNCFallback(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, nil // no docker
	}
	m := buildMapper(context.Background(), nil, run, quietLog())
	got, ok := m.ToHost(`\\host\Movies\a.mkv`)
	if !ok || got != "/mnt/user/Movies/a.mkv" {
		t.Errorf("got (%q,%v), want (/mnt/user/Movies/a.mkv,true)", got, ok)
	}
}

func TestBuildMapperManualWins(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) { return nil, nil }
	manual := []config.PathRule{{From: `\\host\Movies`, To: "/mnt/disk1/Movies"}}
	m := buildMapper(context.Background(), manual, run, quietLog())
	got, ok := m.ToHost(`\\host\Movies\a.mkv`)
	if !ok || got != "/mnt/disk1/Movies/a.mkv" {
		t.Errorf("manual rule should win: got (%q,%v)", got, ok)
	}
}

func TestBuildMapperDockerFailureIsSoft(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	m := buildMapper(context.Background(), nil, run, quietLog())
	// still functional via UNC fallback despite docker error
	if _, ok := m.ToHost(`\\host\TV\x.mkv`); !ok {
		t.Error("mapper should still work when docker detection errors")
	}
}
