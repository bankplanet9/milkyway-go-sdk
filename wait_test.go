package milkyway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForCompletionReachesTerminal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		status := StatusPending
		if n >= 3 {
			status = StatusDone
		}
		fmt.Fprintf(w, `{"transaction_id":1,"status":%d}`, status)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	res, err := c.WaitForCompletion(context.Background(), 1, &PollOptions{
		InitialDelay:      time.Millisecond,
		MaxDelay:          2 * time.Millisecond,
		BackoffMultiplier: 2,
		Timeout:           time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone {
		t.Fatalf("status = %v", res.Status)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("polled %d times (want 3)", n)
	}
}

func TestWaitForCompletionTimeoutReturnsLast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"transaction_id":1,"status":%d}`, StatusPending)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	res, err := c.WaitForCompletion(context.Background(), 1, &PollOptions{
		InitialDelay:      5 * time.Millisecond,
		MaxDelay:          5 * time.Millisecond,
		BackoffMultiplier: 1,
		Timeout:           12 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Budget exceeded; last observed (non-terminal) status returned, no error.
	if res.Status != StatusPending {
		t.Fatalf("status = %v (want Pending)", res.Status)
	}
}

func TestWaitForCompletionImmediateTerminal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprintf(w, `{"transaction_id":1,"status":%d}`, StatusDone)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	res, err := c.WaitForCompletion(context.Background(), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone {
		t.Fatalf("status = %v", res.Status)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("polled %d times (want 1, already terminal)", n)
	}
}

func TestWaitForCompletionContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"transaction_id":1,"status":%d}`, StatusPending)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := c.WaitForCompletion(ctx, 1, &PollOptions{
		InitialDelay:      10 * time.Millisecond,
		MaxDelay:          10 * time.Millisecond,
		BackoffMultiplier: 1,
		Timeout:           time.Minute,
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
