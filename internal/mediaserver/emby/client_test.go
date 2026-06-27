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
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Emby-Token")
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
	if gotToken != "secret" {
		t.Errorf("X-Emby-Token = %q, want secret", gotToken)
	}
	if out.Value != 42 {
		t.Errorf("decoded Value = %d, want 42", out.Value)
	}
}
