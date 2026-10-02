package store

import (
	"encoding/json"
	"testing"
)

func TestIntIsAJSONInteger(t *testing.T) {
	for in, want := range map[string]int{`4`: 4, `-3`: -3, ` 7 `: 7, `null`: 0, `9007199254740991`: 9007199254740991, `-9007199254740991`: -9007199254740991} {
		var n Int
		if err := json.Unmarshal([]byte(in), &n); err != nil || int(n) != want {
			t.Errorf("Int(%s) = %d, %v; want %d", in, n, err, want)
		}
	}
	for _, bad := range []string{`"4"`, `4.0`, `1.5`, `1e3`, `9007199254740992`, `-9007199254740992`, `"abc"`, `""`, `true`, `[1]`} {
		var n Int
		if err := json.Unmarshal([]byte(bad), &n); err == nil {
			t.Errorf("Int(%s) = %d, should fail", bad, n)
		}
	}
}

func TestModelsDecode(t *testing.T) {
	var ts []Ticket
	if err := json.Unmarshal([]byte(`[{"prefix":"A","t_id":4,"first_name":"S","pref":"CALL","changed":true}]`), &ts); err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 || ts[0].TID != 4 || ts[0].FirstName != "S" {
		t.Fatalf("ticket = %+v", ts)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","t_id":"4"}]`), &ts); err == nil {
		t.Fatal("a ticket number sent as a string must be refused")
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","first_name":"no id"}]`), &ts); err == nil {
		t.Fatal("a ticket without t_id must be rejected instead of becoming ticket 0")
	}
	if err := json.Unmarshal([]byte(`[null]`), &ts); err == nil {
		t.Fatal("a null ticket must be rejected")
	}

	var bs []Basket
	if err := json.Unmarshal([]byte(`[{"prefix":"A","b_id":2,"winning_ticket":5}]`), &bs); err != nil || bs[0].BID != 2 || bs[0].WinningTicket != 5 {
		t.Fatalf("basket = %+v, %v", bs, err)
	}
	// An empty winning-ticket box is null: no winner.
	if err := json.Unmarshal([]byte(`[{"prefix":"A","b_id":2,"winning_ticket":null}]`), &bs); err != nil || bs[0].WinningTicket != 0 {
		t.Fatalf("basket with an empty winner = %+v, %v", bs, err)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","description":"no id"}]`), &bs); err == nil {
		t.Fatal("a basket without b_id must be rejected")
	}

	var ps []Prefix
	if err := json.Unmarshal([]byte(`[{"prefix":"A","color":"red","weight":3}]`), &ps); err != nil || ps[0].Weight != 3 {
		t.Fatalf("prefix = %+v, %v", ps, err)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","color":"red","weight":"heavy"}]`), &ps); err == nil {
		t.Fatal("a non-numeric weight must be rejected")
	}

	out, _ := json.Marshal(Ticket{Prefix: "A", TID: 4})
	if string(out) != `{"prefix":"A","t_id":4,"first_name":"","last_name":"","phone_number":"","pref":""}` {
		t.Fatalf("ticket marshals as numbers: %s", out)
	}
}
