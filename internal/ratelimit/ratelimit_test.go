package ratelimit

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func TestLimiter_AllowsUpToTheLimitPerWindow(t *testing.T) {
	l := New(3, time.Minute)

	for i := range 3 {
		if !l.Allow("chat", t0.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("hit %d should be allowed", i+1)
		}
	}
	if l.Allow("chat", t0.Add(10*time.Second)) {
		t.Fatal("the fourth hit within the window must be denied")
	}
}

func TestLimiter_TheWindowSlides(t *testing.T) {
	l := New(2, time.Minute)
	l.Allow("chat", t0)
	l.Allow("chat", t0.Add(30*time.Second))

	if l.Allow("chat", t0.Add(59*time.Second)) {
		t.Fatal("still 2 hits inside the last minute, want denied")
	}
	if !l.Allow("chat", t0.Add(61*time.Second)) {
		t.Fatal("the first hit is now older than the window, want allowed")
	}
	if l.Allow("chat", t0.Add(62*time.Second)) {
		t.Fatal("that made two hits again (at 30s and 61s), want denied")
	}
}

func TestLimiter_DeniedAttemptsDoNotExtendTheBlock(t *testing.T) {
	l := New(1, time.Minute)
	l.Allow("chat", t0)

	for i := 1; i <= 50; i++ {
		l.Allow("chat", t0.Add(time.Duration(i)*time.Second)) // a flood of denied attempts
	}

	if !l.Allow("chat", t0.Add(61*time.Second)) {
		t.Fatal("only accepted hits count, so the chat must be free 61s after its one accepted hit")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := New(1, time.Minute)

	if !l.Allow("a", t0) || !l.Allow("b", t0) {
		t.Fatal("each key has its own quota")
	}
	if l.Allow("a", t0.Add(time.Second)) {
		t.Fatal("key a is over its quota")
	}
}

func TestLimiter_ZeroLimitDisablesIt(t *testing.T) {
	l := New(0, time.Minute)

	for range 1000 {
		if !l.Allow("chat", t0) {
			t.Fatal("a limit of 0 means no limit")
		}
	}
	if len(l.hits) != 0 {
		t.Fatalf("a disabled limiter must not record anything, has %d keys", len(l.hits))
	}
}

func TestLimiter_IdleKeysAreForgotten(t *testing.T) {
	l := New(5, time.Minute)
	for _, key := range []string{"a", "b", "c"} {
		l.Allow(key, t0)
	}

	l.Allow("d", t0.Add(3*time.Minute))

	if len(l.hits) != 1 {
		t.Fatalf("got %d keys, want only the active one after the idle ones were swept", len(l.hits))
	}
}

func TestLimiter_CheckDoesNotRecord(t *testing.T) {
	l := New(2, time.Minute)

	for range 10 {
		if !l.Check("k", t0) {
			t.Fatal("checking never uses up the quota")
		}
	}
	l.Record("k", t0)
	l.Record("k", t0.Add(time.Second))

	if l.Check("k", t0.Add(2*time.Second)) {
		t.Fatal("two recorded hits reach the limit of 2")
	}
	if !l.Check("k", t0.Add(70*time.Second)) {
		t.Fatal("the hits age out of the window")
	}
}

func TestLimiter_RecordCountsEvenPastTheLimit(t *testing.T) {
	l := New(1, time.Minute)
	l.Record("k", t0)
	l.Record("k", t0.Add(30*time.Second))

	if l.Check("k", t0.Add(65*time.Second)) {
		t.Fatal("the second record (at 30s) is still inside the window at 65s")
	}
	if !l.Check("k", t0.Add(95*time.Second)) {
		t.Fatal("both records are older than the window at 95s")
	}
}

func TestLimiter_ResetForgetsAKey(t *testing.T) {
	l := New(1, time.Minute)
	l.Record("k", t0)
	l.Record("other", t0)

	l.Reset("k")

	if !l.Check("k", t0) {
		t.Fatal("a reset key starts over")
	}
	if l.Check("other", t0) {
		t.Fatal("other keys are untouched")
	}
}

func TestLimiter_DisabledCheckAndRecord(t *testing.T) {
	l := New(0, time.Minute)

	l.Record("k", t0)

	if !l.Check("k", t0) || len(l.hits) != 0 {
		t.Fatal("a disabled limiter allows everything and records nothing")
	}
}
