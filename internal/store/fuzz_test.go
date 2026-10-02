package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The fuzz targets below double as ordinary tests: go test runs every seed
// and every input saved under testdata/fuzz. go test -fuzz=FuzzName explores
// further.

// FuzzModelsDecode: whatever JSON arrives, decoding a ticket, basket, prefix
// or bare id never panics, and whatever is accepted encodes to JSON that
// decodes to the same value, so a row tam-client relays or a backup carries
// arrives as it left. An id the original client sends as a string decodes
// like the same id sent as a number.
func FuzzModelsDecode(f *testing.F) {
	for _, seed := range []string{
		`{"prefix":"A","t_id":4,"first_name":"Ann","last_name":"Lee","phone_number":"555-0001","pref":"CALL"}`,
		`{"prefix":"A","t_id":"4","changed":true}`, `{"prefix":"A","t_id":4.0}`, `{"prefix":"A","t_id":" 07 "}`,
		`{"prefix":"A","t_id":"+2"}`, `{"prefix":"A","t_id":-0}`, `{"prefix":"A","t_id":1e3}`, `{"prefix":"A","t_id":"1e3"}`,
		`{"prefix":"A","t_id":"0x1p4"}`, `{"prefix":"A","t_id":"1_000"}`, `{"prefix":"A","t_id":9007199254740993}`,
		`{"prefix":"A","t_id":9007199254740993.0}`, `{"prefix":"A","t_id":"9223372036854775807"}`,
		`{"prefix":"A","t_id":9223372036854775808}`, `{"prefix":"A","t_id":"Infinity"}`, `{"prefix":"A","t_id":"NaN"}`,
		`{"prefix":"A","t_id":1e400}`, `{"prefix":"A","t_id":1.5}`, `{"prefix":"A","t_id":null}`, `{"prefix":"A"}`,
		`{"T_ID":1,"PREFIX":"a"}`, `{"t_id":1,"t_id":null}`, `{"t_id":true}`, `{"t_id":[1]}`, `{"t_id":{}}`,
		`{"prefix":"A","b_id":"2","winning_ticket":5.0,"description":"Wine","donors":"Smiths"}`,
		`{"prefix":"A","b_id":2,"winning_ticket":null}`, `{"prefix":"A","b_id":2,"winning_ticket":"x"}`,
		`{"prefix":"A","color":"red","weight":"3"}`, `{"prefix":"A","color":"red","weight":4.0}`, `{"prefix":"A","weight":"heavy"}`,
		"{\"prefix\":\"a\\u0000b\\ud800\\u2028<>&\",\"t_id\":1}", "{\"prefix\":\"\xff\xfe\",\"t_id\":1}", `{"prefix":1,"t_id":1}`,
		`[]`, `null`, `4`, `"4"`, `4.0`, `" 4 "`, `"-9223372036854775808"`, `-9223372036854775809`, ``, `{`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		decodeRoundTrip[Ticket](t, data)
		decodeRoundTrip[Basket](t, data)
		decodeRoundTrip[Prefix](t, data)
		decodeRoundTrip[Int](t, data)

		// Only a JSON integer, or null, is a whole number.
		var n Int
		if json.Unmarshal(data, &n) != nil {
			return
		}
		if text := strings.TrimSpace(string(data)); text != "null" {
			if _, err := strconv.ParseInt(text, 10, 64); err != nil {
				t.Fatalf("%q decoded as the whole number %d", data, n)
			}
		}
	})
}

// decodeRoundTrip decodes data as a T and, when that works, checks that the
// value encodes and decodes to itself.
func decodeRoundTrip[T comparable](t *testing.T, data []byte) {
	t.Helper()
	var v T
	if json.Unmarshal(data, &v) != nil {
		return
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s decoded to the %T %+v, which does not encode: %v", data, v, v, err)
	}
	var again T
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("%s decoded to the %T %+v, whose encoding %s does not decode: %v", data, v, v, out, err)
	}
	if again != v {
		t.Fatalf("%s decoded to the %T %+v, which comes back as %+v through %s", data, v, v, again, out)
	}
}

// FuzzSearchTickets: a search returns exactly the tickets whose three
// fields contain the three fragments, where % _ and \ are ordinary
// characters and only the ASCII letters ignore case, as SQLite's LIKE
// compares them. The text is valid UTF-8, as everything the pages save is:
// the JSON decoder replaces broken bytes before a ticket reaches the store.
func FuzzSearchTickets(f *testing.F) {
	f.Add("Ann", "Lee", "555-0001", "an", "", "", uint8(0))
	f.Add("Joan", "Smith", "555-0002", "JO", "SMI", "0002", uint8(0))
	f.Add("50%", "a_b", `x\y`, "%", "_", `\`, uint8(0))
	f.Add("Zoë", "ÉCOLE", "☎ 555", "zo", "école", "☎", uint8(0))
	f.Add("İstanbul", "Straße", "K", "i\U00000307", "STRASSE", "k", uint8(0))
	f.Add("Ann\x00e", "Lee", "5", "e", "", "", uint8(0))
	f.Add("Bob", "x", "5", "\x00", "", "", uint8(0))
	f.Add("x", "y", "z", "x", "", "", uint8(255))
	f.Add("", "", "", "", "", "", uint8(0))
	f.Fuzz(func(t *testing.T, a, b, c, first, last, phone string, grow uint8) {
		text := func(s string) string { return strings.ToValidUTF8(s, "\U0000FFFD") }
		a, b, c = text(a), text(b), text(c)
		first, last, phone = text(first), text(last), text(phone)
		if grow > 0 {
			n := int(grow) * 235
			a, first = lengthen(a, n), lengthen(first, n)
		}
		// Rows in the order the search returns them: by prefix, then id.
		rows := []Ticket{
			{"A", 1, a, b, c, "CALL", 0},
			{"A", 2, b, c, a, "TEXT", 0},
			{"B", 1, c, a, b, "CALL", 0},
			{"B", 2, first, last, phone, "CALL", 0},
			{"C", 1, "Ann" + first, strings.ToUpper(last), "(" + phone + ")", "CALL", 0},
			{"C", 2, "50%", "a_b", `x\y`, "CALL", 0},
			{"C", 3, "", "", "", "", 0},
		}
		s := newTestStore(t)
		must(t, s.UpsertTickets(rows))
		got, err := s.SearchTickets(first, last, phone)
		if err != nil {
			t.Fatalf("search %q %q %q: %v", clip(first), clip(last), clip(phone), err)
		}
		want := []Ticket{}
		for _, r := range rows {
			if containsFold(r.FirstName, first) && containsFold(r.LastName, last) && containsFold(r.PhoneNumber, phone) {
				want = append(want, r)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("search %q %q %q found %s, want %s", clip(first), clip(last), clip(phone), ticketIDs(got), ticketIDs(want))
		}
	})
}

// containsFold reports whether s contains sub when only the ASCII letters
// ignore case.
func containsFold(s, sub string) bool {
	return strings.Contains(asciiLower(s), asciiLower(sub))
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func ticketIDs(ts []Ticket) string {
	ids := make([]string, len(ts))
	for i, tk := range ts {
		ids[i] = fmt.Sprintf("%s%d", tk.Prefix, tk.TID)
	}
	return "[" + strings.Join(ids, " ") + "]"
}

// clip shortens a long string for a failure message.
func clip(s string) string {
	if len(s) > 40 {
		return fmt.Sprintf("%s...(%d bytes)", s[:40], len(s))
	}
	return s
}

// lengthen repeats s until it is at least n bytes long, so the fuzzer
// reaches the sizes where limits live without typing that much. An empty s
// stays empty.
func lengthen(s string, n int) string {
	if s == "" || len(s) >= n {
		return s
	}
	return strings.Repeat(s, (n+len(s)-1)/len(s))
}

// FuzzPrefixNames: every name the README allows (trimmed, at most 100
// characters, no / or \, not . or .., and no control characters, which
// ValidatePrefixName adds) is accepted as it is, and every accepted name
// comes back out of the
// URLs the web app builds with it: a path segment escaped with
// encodeURIComponent (tam-client escapes with url.PathEscape on its way to
// the server) and the p query parameter of a prefix delete. Go's ServeMux,
// which both daemons route with, stands in for them.
func FuzzPrefixNames(f *testing.F) {
	for _, seed := range []string{
		"A", " CALL ", "A&B", "50%", "C+", "a b", "a#b?c=d;e", `"q'`, "日本", "🎟\U0000FE0F", "Zoë", "%2F", "%2e", "%",
		".", "..", "...", ".A", "A.", "~", "A/B", `A\B`, "A\tB", "A\x00B", "\x7f", "\U00000085A", "\U000000A0A\U000000A0", "\U0000200B",
		"", "   ", strings.Repeat("L", 100), strings.Repeat("L", 101), strings.Repeat("€", 34), strings.Repeat("🎟", 100),
		"\xff\xfe",
	} {
		f.Add(seed)
	}
	mux := http.NewServeMux()
	var seen string
	mux.HandleFunc("GET /api/tickets/{prefix}/{from}/{to}", func(w http.ResponseWriter, r *http.Request) {
		seen = r.PathValue("prefix")
	})
	mux.HandleFunc("GET /api/reports/byname/{prefix}", func(w http.ResponseWriter, r *http.Request) {
		seen = r.PathValue("prefix")
	})
	mux.HandleFunc("DELETE /api/prefixes", func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("p")
	})
	f.Fuzz(func(t *testing.T, name string) {
		if allowed := allowedName(name); allowed != "" {
			got, err := ValidatePrefixName(allowed)
			if err != nil || got != allowed {
				t.Fatalf("ValidatePrefixName(%q) = %q, %v; the rules allow it as it is", allowed, got, err)
			}
		}
		got, err := ValidatePrefixName(name)
		if err != nil {
			return
		}
		if got != strings.TrimSpace(name) {
			t.Fatalf("ValidatePrefixName(%q) = %q, want it trimmed", name, got)
		}
		for _, target := range []string{
			"/api/tickets/" + encodeURIComponent(got) + "/1/20",
			"/api/tickets/" + url.PathEscape(got) + "/1/20",
			"/api/reports/byname/" + encodeURIComponent(got),
			"/api/prefixes?p=" + encodeURIComponent(got),
		} {
			method := http.MethodGet
			if strings.HasPrefix(target, "/api/prefixes") {
				method = http.MethodDelete
			}
			seen = ""
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
			if rec.Code != 200 || seen != got {
				t.Fatalf("accepted name %q: %s %s answers %d with the prefix %q, want 200 with the name", got, method, target, rec.Code, seen)
			}
		}
	})
}

// allowedName reduces s to a name the README allows: it drops / \ and
// control characters, trims it and keeps at most 100 characters. It returns
// "" when nothing allowed is left.
func allowedName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxPrefixLen {
		s = strings.TrimSpace(string(r[:maxPrefixLen]))
	}
	if s == "." || s == ".." {
		return ""
	}
	return s
}

// encodeURIComponent escapes s as the web app's encodeURIComponent does
// before it puts a prefix in a URL: every byte of the UTF-8 except
// A-Z a-z 0-9 - _ . ! ~ * ' ( ) becomes a %XX escape.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// FuzzStoreRoundTrip: the store keeps text byte for byte, NUL and bytes
// that are not UTF-8 included, and every way of reading a row back returns
// what was written; a backup restored into another store is the same backup.
func FuzzStoreRoundTrip(f *testing.F) {
	f.Add("A", int64(1), "Ann", "Lee", "555-0001", "CALL", int64(0), int64(0))
	f.Add("A/B", int64(0), "", "", "", "", int64(3), int64(0))
	f.Add("A\x00B", int64(7), "a\x00b", "\xff\xfe", "\xed\xa0\x80", "\x00", int64(-1), int64(7))
	f.Add("  ", int64(-5), "50%", "a_b", `x\y`, "TEXT", int64(1<<62), int64(-5))
	f.Add("日本", int64(9223372036854775807), "Zoë", "🎟\U0000FE0F", "☎", " call ", int64(-9223372036854775808), int64(9223372036854775807))
	f.Add("123", int64(123), "1e3", "0x10", "NULL", "null", int64(0), int64(123))
	f.Fuzz(func(t *testing.T, prefix string, id int64, s1, s2, s3, s4 string, weight, winning int64) {
		s := newTestStore(t)
		p := Prefix{Prefix: prefix, Color: s4, Weight: int(weight)}
		tk := Ticket{Prefix: prefix, TID: int(id), FirstName: s1, LastName: s2, PhoneNumber: s3, Pref: s4}
		bk := Basket{Prefix: prefix, BID: int(id), Description: s1, Donors: s2, WinningTicket: int(winning)}
		must(t, s.UpsertPrefixes([]Prefix{p}))
		must(t, s.UpsertTickets([]Ticket{tk}))
		must(t, s.UpsertBaskets([]Basket{bk}))

		expect := func(what string, got, want any, err error) {
			t.Helper()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s = %#v, %v; want %#v", what, got, err, want)
			}
		}
		ps, err := s.ListPrefixes()
		expect("ListPrefixes", ps, []Prefix{p}, err)
		one, err := s.Ticket(prefix, int(id))
		expect("Ticket", one, &tk, err)
		ts, err := s.TicketRange(prefix, int(id), int(id))
		expect("TicketRange", ts, []Ticket{tk}, err)
		ts, err = s.TicketsByPrefix(prefix)
		expect("TicketsByPrefix", ts, []Ticket{tk}, err)
		ts, err = s.AllTickets()
		expect("AllTickets", ts, []Ticket{tk}, err)
		b, err := s.Basket(prefix, int(id))
		expect("Basket", b, &bk, err)
		bs, err := s.BasketRange(prefix, int(id), int(id))
		expect("BasketRange", bs, []Basket{bk}, err)
		bs, err = s.BasketsByPrefix(prefix)
		expect("BasketsByPrefix", bs, []Basket{bk}, err)
		line := DrawingLine{Prefix: prefix, BID: int(id), Description: s1, WinningTicket: int(winning)}
		if winning == id && winning > 0 { // a basket not drawn (0) has no winner
			line.LastName, line.FirstName, line.PhoneNumber = s2, s1, s3
		}
		d, err := s.DrawingLine(prefix, int(id))
		expect("DrawingLine", d, &line, err)

		// The drawing form changes only the winning ticket.
		must(t, s.UpsertWinning([]Basket{{Prefix: prefix, BID: int(id), WinningTicket: int(id)}}))
		bk.WinningTicket = int(id)
		b, err = s.Basket(prefix, int(id))
		expect("Basket after the drawing", b, &bk, err)

		bf, err := s.Export()
		expect("Export", bf, BackupFile{Prefixes: []Prefix{p}, Baskets: []Basket{bk}, Tickets: []Ticket{tk}}, err)
		other := newTestStore(t)
		must(t, other.Import(bf))
		again, err := other.Export()
		expect("Export after Import", again, bf, err)
	})
}
