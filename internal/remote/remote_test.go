package remote

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"ticket-auction-manager/tam-go/internal/version"
)

// echo answers with the request's method, path, headers and body so tests
// can check what the client sent.
func echo(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	status := http.StatusOK
	if r.Header.Get("TAM-KEY") == "BAD" {
		status = http.StatusUnauthorized
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"method": r.Method, "path": r.URL.RequestURI(), "key": r.Header.Get("TAM-KEY"),
		"pw": r.Header.Get("TAM-PW"), "ct": r.Header.Get("Content-Type"), "body": string(body),
		"client": r.Header.Get("X-TAM-Client"),
	})
}

func TestSelfSignedCertificatePolicy(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(echo))
	defer ts.Close()

	res, err := New(ts.URL, "K", true).Get("/api")
	if err != nil || !res.OK() {
		t.Fatalf("insecure client against a self-signed server: %v %+v", err, res)
	}
	if _, err := New(ts.URL, "K", false).Get("/api"); err == nil {
		t.Fatal("a verifying client must reject a self-signed certificate")
	}
}

func TestRequestShape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(echo))
	defer ts.Close()
	c := New(ts.URL+"/", "KEY123", false)

	var seen map[string]any
	res, err := c.Do(http.MethodPost, "/api/auth?key_to_del=x%26y", map[string]string{"TAM-PW": "pw"}, map[string]string{"description": "d"})
	if err != nil {
		t.Fatal(err)
	}
	if err := res.JSON(&seen); err != nil {
		t.Fatal(err)
	}
	if seen["method"] != "POST" || seen["path"] != "/api/auth?key_to_del=x%26y" || seen["key"] != "KEY123" ||
		seen["pw"] != "pw" || seen["ct"] != "application/json" || seen["body"] != `{"description":"d"}` {
		t.Fatalf("request seen by the server: %+v", seen)
	}

	res, _ = c.Get("/api/prefixes")
	res.JSON(&seen)
	if seen["method"] != "GET" || seen["ct"] != "" || seen["body"] != "" {
		t.Fatalf("GET must send no body or content type: %+v", seen)
	}

	res, _ = c.Delete("/api/prefixes?p=A")
	res.JSON(&seen)
	if seen["method"] != "DELETE" {
		t.Fatalf("DELETE: %+v", seen)
	}

	res, err = New(ts.URL, "BAD", false).Get("/api/prefixes")
	if err != nil || res.OK() || res.Status != 401 {
		t.Fatalf("a non-2xx answer is a Response, not an error: %v %+v", err, res)
	}
}

func TestUnreachableServerIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(echo))
	ts.Close()
	if _, err := New(ts.URL, "K", false).Get("/api"); err == nil {
		t.Fatal("a closed server must produce a transport error")
	}
}

// TestEveryRequestNamesTheClient: the server's admin page shows which
// program each client runs, so every request carries the program and its
// build version.
func TestEveryRequestNamesTheClient(t *testing.T) {
	old := version.Version
	version.Version = "9.9.9-test"
	t.Cleanup(func() { version.Version = old })
	ts := httptest.NewServer(http.HandlerFunc(echo))
	defer ts.Close()
	c := New(ts.URL, "KEY", false)

	var seen map[string]any
	for name, call := range map[string]func() (*Response, error){
		"Get":    func() (*Response, error) { return c.Get("/api") },
		"Post":   func() (*Response, error) { return c.Post("/api/tickets", []int{}) },
		"Delete": func() (*Response, error) { return c.Delete("/api/prefixes?p=A") },
		"Do": func() (*Response, error) {
			return c.Do(http.MethodGet, "/api", map[string]string{"X-TAM-Pending": "2"}, nil)
		},
	} {
		res, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res.JSON(&seen)
		if seen["client"] != "tam-client/9.9.9-test" {
			t.Fatalf("%s sent X-TAM-Client %q, want tam-client/9.9.9-test", name, seen["client"])
		}
	}
}
