package shutdown_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
)

// Exercise the real entry points: testing http.Server.Shutdown by itself
// cannot catch main returning as soon as Serve closes its listener.
func TestGracefulShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal regression runs on Unix; Windows has console and tray stop events")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"tam-server", "tam-client"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", program, err, out)
			}
			for _, finish := range []bool{true, false} {
				name := map[bool]string{true: "finishes_active_save", false: "bounds_stalled_save"}[finish]
				t.Run(name, func(t *testing.T) { checkShutdown(t, binary, program, finish) })
			}
		})
	}
}

func checkShutdown(t *testing.T, binary, program string, finish bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	data := t.TempDir()
	args := []string{"-addr", addr, "-tray=false"}
	if program == "tam-client" {
		args = append(args, "-open=false")
	} else {
		args = append(args, "-announce=false")
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "TAM_DATA_DIR="+data, "TAM_PWD=shutdown-test-password")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	var exitErr error
	go func() { exitErr = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-exited
		if t.Failed() {
			t.Log(output.String())
		}
	})
	client := &http.Client{Timeout: time.Second}
	base := "http://" + addr
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		res, err := client.Get(base + "/api")
		if err == nil {
			res.Body.Close()
			ready = res.StatusCode == http.StatusOK
			if ready {
				break
			}
		}
		select {
		case <-exited:
			t.Fatalf("exited before listening: %v", exitErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !ready {
		t.Fatal("program did not start listening")
	}
	key := ""
	if program == "tam-server" {
		req, _ := http.NewRequest(http.MethodPost, base+"/api/auth", strings.NewReader(`{"description":"shutdown test"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("TAM-PW", "shutdown-test-password")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var auth struct {
			AuthKey string `json:"auth_key"`
		}
		err = json.NewDecoder(res.Body).Decode(&auth)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || auth.AuthKey == "" {
			t.Fatalf("create key: %d, %v", res.StatusCode, err)
		}
		key = auth.AuthKey
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	body := `[{"prefix":"A","t_id":44,"first_name":"Finished during shutdown"}]`
	fmt.Fprintf(conn, "POST /api/tickets HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\nTAM-KEY: %s\r\nX-TAM-Client-Name: shutdown-test\r\nX-TAM-Save: 1\r\nExpect: 100-continue\r\n\r\n", len(body), key)
	reader := bufio.NewReader(conn)
	res, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusContinue {
		t.Fatalf("save handler did not begin reading the body: %d", res.StatusCode)
	}
	if _, err := io.WriteString(conn, body[:len(body)-1]); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if program == "tam-client" {
		res, err := client.Post(base+"/api/shutdown", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("shutdown: %d", res.StatusCode)
		}
	} else if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if finish {
		time.Sleep(500 * time.Millisecond)
		if _, err := io.WriteString(conn, body[len(body)-1:]); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("active save was cut off instead of drained: %v", err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("active save: %d", res.StatusCode)
		}
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish within its bounded drain")
	}
	if exitErr != nil {
		t.Fatalf("shutdown exit: %v", exitErr)
	}
	if elapsed := time.Since(started); !finish && elapsed < 3*time.Second {
		t.Fatalf("stalled save was abandoned before the drain deadline: %v", elapsed)
	}
	name := "tam-remote.db"
	if program == "tam-client" {
		name = "tam-local.db"
	}
	sql, err := db.Open(filepath.Join(data, name))
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	var count int
	if err := sql.QueryRow(`SELECT COUNT(*) FROM tickets WHERE t_id=44 AND first_name='Finished during shutdown'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if want := map[bool]int{true: 1, false: 0}[finish]; count != want {
		t.Fatalf("persisted tickets = %d, want %d", count, want)
	}
}
