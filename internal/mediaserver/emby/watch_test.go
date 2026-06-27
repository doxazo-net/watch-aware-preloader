package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func serveFixture(t *testing.T, file string) *Client {
	t.Helper()
	body, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResumeMapsFields(t *testing.T) {
	c := serveFixture(t, "resume.json")
	items, err := c.Resume(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.ID != "item1" || it.ServerPath != "/share/TV_Shows/Slow Horses/s05e01.mkv" {
		t.Errorf("bad id/path: %+v", it)
	}
	if it.BitrateBps != 25000000 || it.SizeBytes != 8471453856 {
		t.Errorf("bad bitrate/size: %+v", it)
	}
	// RunTimeTicks 27063290000 / 1e7 = 2706.329s
	if it.Runtime != time.Duration(27063290000*100) {
		t.Errorf("runtime = %v", it.Runtime)
	}
	// PlaybackPositionTicks 6000000000 / 1e7 = 600s
	if it.ResumeOffset != 600*time.Second {
		t.Errorf("resume offset = %v, want 10m", it.ResumeOffset)
	}
	if it.UserID != "u1" {
		t.Errorf("user id = %q, want u1", it.UserID)
	}
}
