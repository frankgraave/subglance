package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/watchdog"
)

// This is the shipped command, not an API double or alternate server assembly.
// SUBGLANCE_TEST_BINARY lets mutation runs exercise an independently built image.
func TestWatchdogActualBinary(t *testing.T) {
	binary := os.Getenv("SUBGLANCE_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "subglance")
		build := exec.Command("go", "build", "-p", "1", "-o", binary, ".")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	var status atomic.Int64
	status.Store(204)
	payloads := make(chan string, 100)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		payloads <- r.Header.Get("X-SubGlance-Event") + ":" + string(body)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte("PRIVATE_RESPONSE_BODY"))
	}))
	defer receiver.Close()
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			_ = listener.Close()
			args := []string{"--addr", addr, "--data-dir", t.TempDir()}
			if configured {
				args = append(args, "--watchdog-url", receiver.URL+"/PRIVATE_PATH?key=PRIVATE_TOKEN", "--watchdog-interval", "2s")
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			cmd := exec.CommandContext(ctx, binary, args...)
			var logs bytes.Buffer
			cmd.Stdout, cmd.Stderr = &logs, &logs
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Signal(os.Interrupt)
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("binary exit: %v\n%s", err, logs.String())
					}
				case <-time.After(12 * time.Second):
					cancel()
					<-done
					t.Error("binary shutdown timed out")
				}
			})
			base := "http://" + addr
			client := &http.Client{Timeout: time.Second}
			deadline := time.Now().Add(10 * time.Second)
			for {
				res, err := client.Get(base + "/health")
				if err == nil {
					_ = res.Body.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("binary did not become ready")
				}
				time.Sleep(20 * time.Millisecond)
			}
			res, err := client.Get(base + "/api/v1/watchdog")
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != 401 {
				t.Fatalf("anonymous diagnostic = %d", res.StatusCode)
			}
			jar, _ := cookiejar.New(nil)
			client.Jar = jar
			res, err = client.Post(base+"/api/v1/setup", "application/json", strings.NewReader(`{"email":"watchdog@example.test","password":"only-for-local-tests-123"}`))
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != 201 {
				t.Fatalf("setup = %d", res.StatusCode)
			}
			read := func() watchdog.Snapshot {
				t.Helper()
				res, err := client.Get(base + "/api/v1/watchdog")
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				if res.StatusCode != 200 {
					t.Fatalf("actual command watchdog = %d: %s", res.StatusCode, body)
				}
				if res.Header.Get("Cache-Control") != "private, no-store" {
					t.Fatal("diagnostic may be cached")
				}
				if strings.Contains(string(body), "PRIVATE") || strings.Contains(string(body), receiver.URL) {
					t.Fatalf("diagnostic leak: %s", body)
				}
				var s watchdog.Snapshot
				if err := json.Unmarshal(body, &s); err != nil {
					t.Fatal(err)
				}
				return s
			}
			initial := read()
			if initial.Configured != configured || initial.LastAttemptAt != nil || initial.LastSuccessAt != nil {
				t.Fatalf("startup is not honest: %+v", initial)
			}
			if !configured {
				return
			}
			poll := func(result string) watchdog.Snapshot {
				t.Helper()
				deadline := time.Now().Add(6 * time.Second)
				for {
					s := read()
					if s.LastResult != nil && *s.LastResult == result && !s.InFlight {
						return s
					}
					if time.Now().After(deadline) {
						t.Fatalf("never observed %s: %+v", result, s)
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			success := poll("succeeded")
			if *success.LastStatusCode != 204 || success.LastSuccessAt == nil {
				t.Fatalf("actual ping missing: %+v", success)
			}
			status.Store(403)
			rejected := poll("rejected")
			if *rejected.LastStatusCode != 403 || !rejected.LastSuccessAt.Equal(*success.LastSuccessAt) {
				t.Fatal("rejection rewrote success")
			}
			select {
			case payload := <-payloads:
				if payload != "alive:alive; 0 monitors scheduled, 0 checks completed" {
					t.Fatalf("payload changed: %q", payload)
				}
			default:
				t.Fatal("no actual outbound payload")
			}
		})
	}
}
