package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRecentlyAddedQueryParams(t *testing.T) {
	// Latest must request flattened, video-only leaf items, else it returns
	// MusicAlbum/Series containers with no warmable media.
	qCh := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qCh <- r.URL.Query()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecentlyAdded(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	q := <-qCh
	if q.Get("GroupItems") != "false" {
		t.Errorf("GroupItems = %q, want false", q.Get("GroupItems"))
	}
	if q.Get("IncludeItemTypes") != "Movie,Episode" {
		t.Errorf("IncludeItemTypes = %q, want Movie,Episode", q.Get("IncludeItemTypes"))
	}
	if f := q.Get("Fields"); !strings.Contains(f, "Path") || !strings.Contains(f, "MediaSources") {
		t.Errorf("Fields = %q, want to contain Path,MediaSources", f)
	}
}

func TestRecentlyAddedMapsLeafItems(t *testing.T) {
	c := serveFixture(t, "latest.json")
	items, err := c.RecentlyAdded(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	for _, it := range items {
		if it.ServerPath == "" || it.BitrateBps == 0 || it.SizeBytes == 0 {
			t.Errorf("leaf item missing media info: %+v", it)
		}
	}
}

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
