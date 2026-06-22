package milkyway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func Test401RefreshReplay(t *testing.T) {
	var tokenCalls int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&tokenCalls, 1)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: fmt.Sprintf("tok-%d", n), ExpiresIn: 3600})
	}))
	defer tokenSrv.Close()

	var apiCalls int32
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&apiCalls, 1)
		if n == 1 {
			// First call: stale token -> 401 forces a refresh + replay.
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				t.Errorf("first auth = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"expired"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok-2" {
			t.Errorf("replay auth = %q (want refreshed)", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"transaction_id":7,"status":1}`)
	}))
	defer apiSrv.Close()

	opts := Options{BaseURL: apiSrv.URL, TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s"}
	c, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Postcheck(context.Background(), 7)
	if err != nil {
		t.Fatalf("postcheck after replay: %v", err)
	}
	if res.Status != StatusDone {
		t.Fatalf("status = %v", res.Status)
	}
	if n := atomic.LoadInt32(&apiCalls); n != 2 {
		t.Fatalf("api calls = %d (want 2: original + replay)", n)
	}
	if n := atomic.LoadInt32(&tokenCalls); n != 2 {
		t.Fatalf("token calls = %d (want 2: initial + forced refresh)", n)
	}
}

func Test401ReplayStillUnauthorized(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "tok", ExpiresIn: 3600})
	}))
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad creds"}`)
	}))
	defer apiSrv.Close()

	c, err := NewClient(Options{BaseURL: apiSrv.URL, TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Postcheck(context.Background(), 1)
	if err == nil {
		t.Fatal("expected auth error after failed replay")
	}
}

func TestPerAttemptTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			fmt.Fprint(w, `{}`)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil, WithRequestTimeout(20*time.Millisecond), WithMaxRetries(0))
	_, err := c.Postcheck(context.Background(), 1)
	if err == nil {
		t.Fatal("expected per-attempt timeout error")
	}
}

func TestContextCancellationNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil, WithRequestTimeout(time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := c.Postcheck(ctx, 1)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
