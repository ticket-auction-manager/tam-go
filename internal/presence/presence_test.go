package presence

import (
	"sync"
	"testing"
	"time"
)

// clock is a fake time source the tests move by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) advance(d time.Duration) time.Time {
	c.t = c.t.Add(d)
	return c.t
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)}
}

func TestSeenHeartbeatUpdatedForget(t *testing.T) {
	c := newClock()
	reg := New(c.now)
	if snap := reg.Snapshot(); len(snap) != 0 {
		t.Fatalf("a new registry must be empty, got %v", snap)
	}

	t0 := c.t
	reg.Seen("K", "tam-client/1.0.0")
	rec := reg.Snapshot()["K"]
	if rec.Seen != t0 || rec.Client != "tam-client/1.0.0" || rec.HasPending || rec.Pending != 0 || !rec.Updated.IsZero() {
		t.Fatalf("after Seen: %+v", rec)
	}

	t1 := c.advance(5 * time.Second)
	reg.Heartbeat("K", "tam-client/1.0.1", 3)
	rec = reg.Snapshot()["K"]
	if rec.Seen != t1 || rec.Client != "tam-client/1.0.1" || !rec.HasPending || rec.Pending != 3 || !rec.Updated.IsZero() {
		t.Fatalf("after Heartbeat: %+v", rec)
	}

	// A request that names no client keeps the last name and the last
	// heartbeat's count.
	t2 := c.advance(5 * time.Second)
	reg.Seen("K", "")
	rec = reg.Snapshot()["K"]
	if rec.Seen != t2 || rec.Client != "tam-client/1.0.1" || !rec.HasPending || rec.Pending != 3 {
		t.Fatalf("after Seen without a client: %+v", rec)
	}

	// An accepted write also counts as seeing the key.
	t3 := c.advance(5 * time.Second)
	reg.Updated("K")
	rec = reg.Snapshot()["K"]
	if rec.Updated != t3 || rec.Seen != t3 || rec.Client != "tam-client/1.0.1" || rec.Pending != 3 {
		t.Fatalf("after Updated: %+v", rec)
	}

	// A heartbeat with nothing queued is still a heartbeat.
	reg.Heartbeat("K", "tam-client/1.0.1", 0)
	if rec = reg.Snapshot()["K"]; !rec.HasPending || rec.Pending != 0 || rec.Updated != t3 {
		t.Fatalf("after an empty heartbeat: %+v", rec)
	}

	// Keys are independent, and an update on an unknown key creates it.
	reg.Updated("L")
	snap := reg.Snapshot()
	if len(snap) != 2 || snap["L"].Updated != t3 || snap["L"].Seen != t3 || snap["L"].Client != "" || snap["L"].HasPending {
		t.Fatalf("after Updated on a new key: %+v", snap)
	}

	reg.Forget("K")
	if snap = reg.Snapshot(); len(snap) != 1 || snap["L"].Updated != t3 {
		t.Fatalf("after Forget: %+v", snap)
	}
	reg.Forget("never there") // harmless
}

func TestSnapshotIsACopy(t *testing.T) {
	reg := New(newClock().now)
	reg.Seen("K", "c")
	snap := reg.Snapshot()
	snap["K"] = Record{Client: "changed"}
	snap["M"] = Record{}
	if again := reg.Snapshot(); len(again) != 1 || again["K"].Client != "c" {
		t.Fatalf("changing a snapshot must not change the registry: %+v", again)
	}
}

func TestNilClockIsTheWallClock(t *testing.T) {
	reg := New(nil)
	before := time.Now()
	reg.Seen("K", "c")
	if seen := reg.Snapshot()["K"].Seen; seen.Before(before) || seen.After(time.Now()) {
		t.Fatalf("Seen with the wall clock = %v", seen)
	}
}

func TestConcurrentUse(t *testing.T) {
	reg := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('A' + i%3))
			for j := 0; j < 200; j++ {
				reg.Seen(key, "c")
				reg.Heartbeat(key, "c", j)
				reg.Updated(key)
				reg.Snapshot()
				if j%50 == 0 {
					reg.Forget(key)
				}
			}
		}(i)
	}
	wg.Wait()
}
