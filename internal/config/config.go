// Package config reads and writes the client's settings.json.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Settings is the settings.json document.
type Settings struct {
	RemoteServer  string `json:"remote_server"`
	RemoteKey     string `json:"remote_key"`
	RemotePort    string `json:"remote_port"`
	RemoteTLS     bool   `json:"remote_tls"`
	DefaultPref   string `json:"default_pref"`
	VenueName     string `json:"venue_name"`
	DisableAttrib bool   `json:"disable_attrib"`

	// Set by pairing: the server's display name and, over TLS, the
	// SHA-256 fingerprint of the certificate seen when pairing.
	RemoteName        string `json:"remote_name"`
	RemoteFingerprint string `json:"remote_fingerprint"`
}

// Defaults returns the settings of a fresh installation.
func Defaults() Settings {
	return Settings{
		RemotePort:  "8000",
		DefaultPref: "CALL",
		VenueName:   "Test Venue",
	}
}

// LoadError reports a settings file that could not be used. Load returns it
// together with the settings of the backup copy (FromBackup), or else with
// default settings, so the application keeps working.
type LoadError struct {
	Path       string
	Err        error
	FromBackup bool // the settings returned are the backup's (BackupPath)
}

func (e *LoadError) Error() string { return fmt.Sprintf("settings file %s: %v", e.Path, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// BackupPath is where Save keeps a copy of the last settings it wrote, for
// when the file itself cannot be read: a power cut can leave a file that was
// being written empty or full of zeros, and a hand edit can break it.
func BackupPath(path string) string { return path + ".bak" }

// Load reads the settings file. Defaults are created only when both the file
// and its backup are missing. A missing, unreadable or malformed file yields
// a *LoadError with the backup's settings when that copy is good, and defaults
// otherwise; existing recovery files stay untouched until an explicit save.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err == nil {
		s, perr := parse(data)
		if perr == nil {
			return s, nil
		}
		err = perr
	}
	bak, backupErr := os.ReadFile(BackupPath(path))
	if backupErr == nil {
		if s, perr := parse(bak); perr == nil {
			return s, &LoadError{Path: path, Err: err, FromBackup: true}
		} else {
			backupErr = perr
		}
	}
	if missing && errors.Is(backupErr, fs.ErrNotExist) {
		s := Defaults()
		if err := Save(path, s); err != nil {
			return s, &LoadError{Path: path, Err: err}
		}
		return s, nil
	}
	if missing {
		err = fmt.Errorf("%w (backup %s: %v)", err, BackupPath(path), backupErr)
	}
	return Defaults(), &LoadError{Path: path, Err: err}
}

// parse reads a settings document over the defaults. A file of nothing but
// zeros or spaces, as a power cut can leave, is an error like any other.
func parse(data []byte) (Settings, error) {
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		return Defaults(), err
	}
	return s, nil
}

// Save writes the settings file, then the same document as its backup copy
// (BackupPath). Each goes to a temporary file, flushed to the disk, that
// then replaces the real one, so a crash or a power cut mid-write leaves
// the old file intact. Windows refuses the replace while another process
// holds the file open; the document is then written in place.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := replace(path, data); err != nil {
		return err
	}
	if err := replace(BackupPath(path), data); err != nil {
		log.Printf("settings backup %s: %v", BackupPath(path), err)
	}
	return nil
}

// replace writes data to path through a synced temporary file.
func replace(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return os.WriteFile(path, data, 0o644)
	}
	return nil
}

// File is the settings document with an in-memory copy. Readers never see
// a half-written file: saves happen under the write lock, and the file is
// re-read only when its modification time or size changed, which is how a
// hand edit is picked up while the daemon runs.
type File struct {
	path    string
	mu      sync.RWMutex
	cur     Settings
	loaded  bool
	mtime   time.Time
	size    int64
	problem string // why the file could not be used, for the pages; "" when it could
}

// Open loads the settings file (creating it with defaults when missing)
// and returns the live document. A broken file is logged and reported by
// Problem; the backup copy's settings are used until it is fixed, or the
// defaults when there is no good copy.
func Open(path string) *File {
	f := &File{path: path}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.reload(); err != nil {
		log.Printf("%v (%s)", err, f.problem)
	} else if data, err := os.ReadFile(path); err == nil {
		// A file written before backups were kept gets its copy now.
		if bak, err := os.ReadFile(BackupPath(path)); err != nil || !bytes.Equal(bak, data) {
			if err := replace(BackupPath(path), data); err != nil {
				log.Printf("settings backup %s: %v", BackupPath(path), err)
			}
		}
	}
	return f
}

// Problem says why the settings file could not be used and what the client
// does meanwhile, or "" when the file is fine. The pages show it.
func (f *File) Problem() string {
	f.Get() // picks up a fix made by hand
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.problem
}

// reload reads the file and remembers its stat. The caller holds mu for
// writing. When the file cannot be used, the last good settings stay: the
// ones read before, or at start the backup copy's, or the defaults.
func (f *File) reload() error {
	s, err := Load(f.path)
	if info, statErr := os.Stat(f.path); statErr == nil {
		f.mtime, f.size = info.ModTime(), info.Size()
	}
	if err != nil {
		var le *LoadError
		fromBackup := errors.As(err, &le) && le.FromBackup
		switch {
		case fromBackup && (!f.loaded || errors.Is(le.Err, fs.ErrNotExist) && f.cur == s):
			f.cur, f.loaded = s, true
			f.problem = fmt.Sprintf("settings.json could not be read (%v); the client uses the copy it saved last (settings.json.bak). Save the settings again to repair the file.", unwrapLoad(err))
		case f.loaded:
			f.problem = fmt.Sprintf("settings.json was changed and cannot be read (%v); the client keeps the settings it had. Fix the file or save the settings again.", unwrapLoad(err))
		default:
			f.cur = s
			f.problem = fmt.Sprintf("settings.json could not be read (%v) and there is no good copy; the client runs with default settings, not paired with any server, until the file is fixed or the settings are saved again.", unwrapLoad(err))
		}
		return err
	}
	f.cur, f.loaded, f.problem = s, true, ""
	return nil
}

// unwrapLoad is the reason inside a *LoadError, without the path.
func unwrapLoad(err error) error {
	var le *LoadError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// changedOnDisk reports whether the file differs from what was last read.
func (f *File) changedOnDisk() bool {
	info, err := os.Stat(f.path)
	if err != nil {
		return true
	}
	return !info.ModTime().Equal(f.mtime) || info.Size() != f.size
}

// Get returns the current settings.
func (f *File) Get() Settings {
	f.mu.RLock()
	if !f.changedOnDisk() {
		s := f.cur
		f.mu.RUnlock()
		return s
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changedOnDisk() {
		if err := f.reload(); err != nil {
			log.Printf("%v (keeping the last good settings)", err)
		}
	}
	return f.cur
}

// Update applies fn to the current settings and saves the result. fn runs
// under the lock, so concurrent updates never lose each other's fields.
func (f *File) Update(fn func(Settings) (Settings, error)) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changedOnDisk() {
		if err := f.reload(); err != nil {
			log.Printf("%v (keeping the last good settings)", err)
		}
	}
	next, err := fn(f.cur)
	if err != nil {
		return f.cur, err
	}
	if err := Save(f.path, next); err != nil {
		return f.cur, err
	}
	f.cur, f.loaded, f.problem = next, true, ""
	if info, err := os.Stat(f.path); err == nil {
		f.mtime, f.size = info.ModTime(), info.Size()
	}
	return next, nil
}

var knownKeys = map[string]bool{
	"remote_server": true, "remote_key": true, "remote_port": true, "remote_tls": true,
	"default_pref": true, "venue_name": true, "disable_attrib": true,
	"remote_name": true, "remote_fingerprint": true,
}

// Merge applies the keys present in patch onto current. Keys that are not
// settings, or values of the wrong type, are rejected.
func Merge(current Settings, patch map[string]json.RawMessage) (Settings, error) {
	for k := range patch {
		if !knownKeys[k] {
			return current, fmt.Errorf("unknown setting %q", k)
		}
	}
	base, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return current, err
	}
	for k, v := range patch {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return current, err
	}
	var s Settings
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return current, fmt.Errorf("invalid settings: %w", err)
	}
	return s, nil
}

// Normalize trims the text fields so a stray space never ends up in a URL.
func Normalize(s Settings) Settings {
	s.RemoteServer = strings.TrimSpace(s.RemoteServer)
	s.RemoteKey = strings.TrimSpace(s.RemoteKey)
	s.RemotePort = strings.TrimSpace(s.RemotePort)
	s.DefaultPref = strings.TrimSpace(s.DefaultPref)
	s.VenueName = strings.TrimSpace(s.VenueName)
	s.RemoteName = strings.TrimSpace(s.RemoteName)
	s.RemoteFingerprint = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s.RemoteFingerprint), ":", ""))
	return s
}

// Validate checks the values that other code relies on.
func Validate(s Settings) error {
	if host := s.RemoteServer; host != "" {
		if strings.ContainsAny(host, `/\?#@ `) || strings.Contains(host, "://") {
			return errors.New("remote_server must be a host name or IP address, without scheme, port or path")
		}
		if s.RemotePort == "" {
			return errors.New("remote_port is required when remote_server is set")
		}
	}
	if p := s.RemotePort; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("remote_port must be a number between 1 and 65535")
		}
	}
	if s.DefaultPref != "CALL" && s.DefaultPref != "TEXT" {
		return errors.New("default_pref must be CALL or TEXT")
	}
	if fp := s.RemoteFingerprint; fp != "" {
		if len(fp) != 64 || strings.Trim(fp, "0123456789abcdef") != "" {
			return errors.New("remote_fingerprint must be a SHA-256 fingerprint in hex, or empty")
		}
	}
	return nil
}

// RemoteURL returns the base URL of the remote server, or "" in standalone
// mode. IPv6 addresses are bracketed.
func (s Settings) RemoteURL() string {
	host := strings.TrimSpace(s.RemoteServer)
	if host == "" {
		return ""
	}
	scheme := "http"
	if s.RemoteTLS {
		scheme = "https"
	}
	port := strings.TrimSpace(s.RemotePort)
	if port == "" {
		port = "8000"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
