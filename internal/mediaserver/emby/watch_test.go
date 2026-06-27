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

func TestLatestFieldsValues(t *testing.T) {
	// latestFields is a package-level function; verify its returned values directly
	// so a future edit cannot silently regress the params without breaking this test.
	q := latestFields()
	if got := q.Get("GroupItems"); got != "false" {
		t.Errorf("GroupItems = %q, want \"false\"", got)
	}
	if got := q.Get("IncludeItemTypes"); got != "Movie,Episode" {
		t.Errorf("IncludeItemTypes = %q, want \"Movie,Episode\"", got)
	}
	if got := q.Get("Fields"); got != "Path,MediaSources" {
		t.Errorf("Fields = %q, want \"Path,MediaSources\"", got)
	}
}

func TestRecentlyAddedEmptyResponse(t *testing.T) {
	// An empty array from the server must return an empty slice with no error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.RecentlyAdded(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

func TestRecentlyAddedUserIDInPath(t *testing.T) {
	// The user ID must appear verbatim in the request URL path.
	const userID = "abc123"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecentlyAdded(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, userID) {
		t.Errorf("request path %q does not contain user ID %q", gotPath, userID)
	}
}

func TestRecentlyAddedFixtureServerPaths(t *testing.T) {
	// Verify the UNC ServerPath values from latest.json are preserved as-is
	// (the client should not mangle the paths before handing them to callers).
	c := serveFixture(t, "latest.json")
	items, err := c.RecentlyAdded(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	want := map[string]string{
		"ep1": `\\host\TV_Shows\Example Series\Season 1\example.s01e01.mkv`,
		"mv1": `\\host\Movies\Example Movie (2024)\example.movie.2024.mkv`,
	}
	for _, it := range items {
		if exp, ok := want[it.ID]; !ok {
			t.Errorf("unexpected item ID %q", it.ID)
		} else if it.ServerPath != exp {
			t.Errorf("item %q ServerPath = %q, want %q", it.ID, it.ServerPath, exp)
		}
	}
}

func TestRecentlyAddedFixtureRuntimes(t *testing.T) {
	// RunTimeTicks 12000000000 → 1200 s; 60000000000 → 6000 s.
	c := serveFixture(t, "latest.json")
	items, err := c.RecentlyAdded(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Duration{
		"ep1": 1200 * time.Second,
		"mv1": 6000 * time.Second,
	}
	for _, it := range items {
		if exp, ok := want[it.ID]; !ok {
			t.Errorf("unexpected item ID %q", it.ID)
		} else if it.Runtime != exp {
			t.Errorf("item %q Runtime = %v, want %v", it.ID, it.Runtime, exp)
		}
	}
}

func TestRecentlyAddedUserIDPropagated(t *testing.T) {
	// Every returned item must carry the user ID passed to RecentlyAdded.
	const userID = "testuser42"
	c := serveFixture(t, "latest.json")
	items, err := c.RecentlyAdded(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.UserID != userID {
			t.Errorf("item %q UserID = %q, want %q", it.ID, it.UserID, userID)
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
