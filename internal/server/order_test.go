package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/store"
)

// orderedSave sends a keyed save of ticket A 1 with the given phone number,
// numbered as tam-client numbers its saves when client is not empty.
func (a *api) orderedSave(client, save, phone string) (int, http.Header) {
	a.t.Helper()
	body, _ := json.Marshal([]store.Ticket{{Prefix: "A", TID: 1, PhoneNumber: phone}})
	req, _ := http.NewRequest("POST", a.url+"/api/tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TAM-KEY", a.key)
	if client != "" {
		req.Header.Set("X-TAM-Client-Name", client)
		req.Header.Set("X-TAM-Save", save)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header
}

func (a *api) phone() string {
	a.t.Helper()
	tk, err := a.st.Ticket("A", 1)
	if err != nil || tk == nil {
		a.t.Fatalf("ticket A 1: %+v, %v", tk, err)
	}
	return tk.PhoneNumber
}

// TestSavesFromAClientApplyInOrder: a save the network delivers late (the
// client gave up on it, queued it and sent it again) must not undo what
// came after it. The server applies a numbered save only when it is newer
// than the last one applied from that client. The last save arriving again
// is answered as done with X-TAM-Stale; any other save at or below the
// last number is answered 409 with that number (X-TAM-Last-Save), so a
// client whose numbers went back numbers it anew rather than lose it.
// Saves without numbers, as the original client sends them, apply as they
// come.
func TestSavesFromAClientApplyInOrder(t *testing.T) {
	a := newAPI(t)
	if code, h := a.orderedSave("L1", "7", "seventh"); code != 200 || h.Get("X-TAM-Stale") != "" {
		t.Fatalf("a new save = %d, stale %q", code, h.Get("X-TAM-Stale"))
	}
	if code, h := a.orderedSave("L1", "7", "seventh"); code != 200 || h.Get("X-TAM-Stale") != "1" || a.phone() != "seventh" {
		t.Fatalf("save 7 again = %d, stale %q; want 200 and marked stale (the ticket reads %q)", code, h.Get("X-TAM-Stale"), a.phone())
	}
	for _, c := range []struct{ n, phone string }{{"6", "late copy 6"}, {"7", "another save numbered 7"}} {
		code, h := a.orderedSave("L1", c.n, c.phone)
		if code != 409 || h.Get("X-TAM-Last-Save") != "7" {
			t.Fatalf("save %s (%s) after save 7 = %d, last save %q; want 409 and 7", c.n, c.phone, code, h.Get("X-TAM-Last-Save"))
		}
		if a.phone() != "seventh" {
			t.Fatalf("save %s after save 7 changed the ticket to %q", c.n, a.phone())
		}
	}
	if code, _ := a.orderedSave("L1", "8", "eighth"); code != 200 || a.phone() != "eighth" {
		t.Fatalf("a newer save = %d, the ticket reads %q", code, a.phone())
	}
	if code, _ := a.orderedSave("", "", "unnumbered"); code != 400 || a.phone() != "eighth" {
		t.Fatalf("an unnumbered save = %d, the ticket reads %q; want 400 and eighth", code, a.phone())
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if code, _ := a.orderedSave("L1", bad, "bad"); code != 400 {
			t.Fatalf("save number %q = %d, want 400", bad, code)
		}
	}
	if code, _ := a.orderedSave(strings.Repeat("L", 65), "9", "long name"); code != 400 {
		t.Fatalf("a client name of 65 characters = %d, want 400", code)
	}
}

// TestStaleDeleteAnswersAsDone: a numbered prefix delete that is stale is
// skipped and answered 200, not 404: the client replaying it must not file
// it as refused.
func TestStaleDeleteAnswersAsDone(t *testing.T) {
	a := newAPI(t)
	a.st.UpsertPrefixes([]store.Prefix{{Prefix: "Z", Color: "red", Weight: 1}})
	del := func(save string) (int, http.Header) {
		req, _ := http.NewRequest("DELETE", a.url+"/api/prefixes?p=Z", nil)
		req.Header.Set("TAM-KEY", a.key)
		req.Header.Set("X-TAM-Client-Name", "L1")
		req.Header.Set("X-TAM-Save", save)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header
	}
	if code, _ := del("3"); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if code, h := del("3"); code != 200 || h.Get("X-TAM-Stale") != "1" {
		t.Fatalf("the same delete again = %d, stale %q; want 200 and marked stale", code, h.Get("X-TAM-Stale"))
	}
}

// TestARepeatedSaveIsNoUpdate: the admin page's "last update" of a client
// moves with the saves the server applied; a save arriving again, which
// changes nothing (X-TAM-Stale), or one refused as older (409), does not
// move it.
func TestARepeatedSaveIsNoUpdate(t *testing.T) {
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	reg := presence.New(func() time.Time { return now })
	a := newAPI(t, WithPresence(reg))
	if code, _ := a.orderedSave("L1", "5", "fifth"); code != 200 {
		t.Fatalf("save = %d", code)
	}
	applied := now
	now = now.Add(time.Minute)
	if code, h := a.orderedSave("L1", "5", "fifth"); code != 200 || h.Get("X-TAM-Stale") != "1" {
		t.Fatalf("the same save again = %d, stale %q", code, h.Get("X-TAM-Stale"))
	}
	now = now.Add(time.Minute)
	if code, _ := a.orderedSave("L1", "4", "older"); code != 409 {
		t.Fatalf("an older save = %d, want 409", code)
	}
	if rec := reg.Snapshot()[a.key]; rec.Updated != applied || rec.Seen != now {
		t.Fatalf("after a repeat and an older save: %+v, want updated at the applied save and seen now", rec)
	}
}
