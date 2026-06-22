package milkyway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestProvider(srvURL string, expiresIn int) (*keycloakTokenProvider, *int32) {
	var calls int32
	opts := &Options{ClientID: "c", ClientSecret: "s", TokenURL: srvURL, Scope: "payments"}
	opts.applyDefaults()
	p := newKeycloakTokenProvider(&http.Client{}, opts)
	return p, &calls
}

func tokenServer(t *testing.T, calls *int32, expiresIn int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("client_id") != "c" || r.Form.Get("client_secret") != "s" {
			t.Errorf("creds = %q/%q", r.Form.Get("client_id"), r.Form.Get("client_secret"))
		}
		if r.Form.Get("scope") != "payments" {
			t.Errorf("scope = %q", r.Form.Get("scope"))
		}
		n := atomic.LoadInt32(calls)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: fmt.Sprintf("tok-%d", n), ExpiresIn: expiresIn})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTokenCaching(t *testing.T) {
	var calls int32
	srv := tokenServer(t, &calls, 3600)
	p, _ := newTestProvider(srv.URL, 3600)

	for i := 0; i < 5; i++ {
		tok, err := p.getAccessToken(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if tok != "tok-1" {
			t.Fatalf("token = %q", tok)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("token fetched %d times (want 1, cached)", n)
	}
}

func TestTokenRefreshNearExpiry(t *testing.T) {
	var calls int32
	srv := tokenServer(t, &calls, 10) // 10s lifetime, 30s skew -> always stale
	p, _ := newTestProvider(srv.URL, 10)

	if _, err := p.getAccessToken(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.getAccessToken(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// With skew (30s) > lifetime (10s) the cached token is always stale -> refetch.
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("expected 2 fetches with stale token, got %d", n)
	}
}

func TestTokenForceRefresh(t *testing.T) {
	var calls int32
	srv := tokenServer(t, &calls, 3600)
	p, _ := newTestProvider(srv.URL, 3600)

	t1, _ := p.getAccessToken(context.Background(), false)
	t2, _ := p.getAccessToken(context.Background(), true)
	if t1 == t2 {
		t.Fatalf("force refresh returned same token %q", t1)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d (want 2)", n)
	}
}

func TestTokenSingleFlight(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "tok", ExpiresIn: 3600})
	}))
	defer srv.Close()

	opts := &Options{ClientID: "c", ClientSecret: "s", TokenURL: srv.URL}
	opts.applyDefaults()
	p := newKeycloakTokenProvider(&http.Client{}, opts)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.getAccessToken(context.Background(), false); err != nil {
				t.Errorf("getAccessToken: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("single-flight failed: %d concurrent fetches (want 1)", n)
	}
}

func TestTokenEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_client"}`)
	}))
	defer srv.Close()
	opts := &Options{ClientID: "c", ClientSecret: "s", TokenURL: srv.URL}
	opts.applyDefaults()
	p := newKeycloakTokenProvider(&http.Client{}, opts)
	if _, err := p.getAccessToken(context.Background(), false); err == nil {
		t.Fatal("expected token acquisition error")
	}
}
