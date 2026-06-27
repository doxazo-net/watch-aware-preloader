package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRejectsBadURL(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "http://user:pw@host", "http://h/?q=1"} {
		if _, err := New(bad, "k", nil); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestGetSendsTokenAndDecodes(t *testing.T) {
	tokenCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCh <- r.Header.Get("X-Emby-Token")
		_, _ = w.Write([]byte(`{"Value":42}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "secret", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Value int }
	if err := c.get(context.Background(), "/Test", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotToken := <-tokenCh; gotToken != "secret" {
		t.Errorf("X-Emby-Token = %q, want secret", gotToken)
	}
	if out.Value != 42 {
		t.Errorf("decoded Value = %d, want 42", out.Value)
	}
}

func TestGetDoesNotFollowRedirect(t *testing.T) {
	// X-Emby-Token would be re-sent on a cross-host redirect; the client must
	// refuse to follow, so the redirect target never sees the API key.
	leaked := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked <- r.Header.Get("X-Emby-Token")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	c, err := New(redirector.URL, "secret", redirector.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.get(context.Background(), "/x", nil, nil); err == nil {
		t.Error("expected error: redirect must not be followed")
	}
	select {
	case tok := <-leaked:
		t.Errorf("redirect was followed; token leaked to other host: %q", tok)
	default:
	}
}
