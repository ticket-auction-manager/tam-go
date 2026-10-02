// Package httpx holds the small helpers every handler uses.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// MaxBody is the largest request body accepted (backup files can be big).
// It is a variable so tests can lower it.
var MaxBody int64 = 64 << 20

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

// WriteError writes the {"detail": msg} error document the original uses.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"detail": msg})
}

// WriteInternal logs the real error and answers a generic 500, so driver
// messages and file paths stay in the log.
func WriteInternal(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	WriteError(w, http.StatusInternalServerError, "Internal error; see the daemon log")
}

// WriteDecodeError answers 413 for a body over MaxBody and 400 otherwise.
func WriteDecodeError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body must be at most %d bytes", MaxBody))
		return
	}
	WriteError(w, http.StatusBadRequest, err.Error())
}

// DecodeJSON decodes exactly one JSON document from the request body into v.
// The request must declare application/json. Bodies over MaxBody fail with
// an *http.MaxBytesError, which WriteDecodeError turns into a 413.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON document")
		}
		return err
	}
	return nil
}

// SameSite reports whether a request may perform a write. Browsers send
// Sec-Fetch-Site on every request; a cross-site value means another web page
// is trying to use this API. Non-browser clients send no header.
func SameSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// RemoteIP is the address part of r.RemoteAddr: the address a request came
// from, which the logs name and the password limit counts by.
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IntParam reads an integer path parameter.
func IntParam(r *http.Request, name string) (int, error) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	// Ids are whole numbers from 0 to the largest a browser holds exactly.
	if n < 0 || int64(n) > 1<<53-1 {
		return 0, fmt.Errorf("%s must be between 0 and 9007199254740991", name)
	}
	return n, nil
}

// JSONErrors wraps a mux so that, under prefix, an unknown path or a wrong
// method answers {"detail": ...} like the original instead of plain text.
// Redirects the mux issues (path cleaning) pass through unchanged.
func JSONErrors(mux *http.ServeMux, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" || !strings.HasPrefix(r.URL.Path, prefix) {
			mux.ServeHTTP(w, r)
			return
		}
		probe := &statusOnly{ResponseWriter: w}
		mux.ServeHTTP(probe, r)
		status := probe.status
		if status == 0 {
			status = http.StatusNotFound
		}
		if status/100 == 3 {
			w.WriteHeader(status)
			return
		}
		WriteError(w, status, http.StatusText(status))
	})
}

// statusOnly records the status the mux would send and drops its text body.
type statusOnly struct {
	http.ResponseWriter
	status int
}

func (s *statusOnly) WriteHeader(code int) { s.status = code }

func (s *statusOnly) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return len(b), nil
}
