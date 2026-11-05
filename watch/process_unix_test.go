//go:build !windows

package watch

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopSuspendedDaemon(t *testing.T) {
	for _, registered := range []bool{false, true} {
		name := "Stop"
		if registered {
			name = "StopDaemon"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(daemonRegistryEnv, t.TempDir())
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			process := exec.Command(os.Args[0], "-test.run=TestSuspendedDaemonHelperProcess", "--", "watch", "daemon", root)
			process.Env = append(os.Environ(), "CODEMAP_SUSPENDED_HELPER="+root, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			waited := make(chan struct{})
			var waitErr error
			go func() { waitErr = process.Wait(); close(waited) }()
			t.Cleanup(func() { _ = process.Process.Kill(); <-waited })
			pid := process.Process.Pid
			readyDeadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
					break
				}
				if time.Now().After(readyDeadline) {
					t.Fatal("daemon did not install its signal handler")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := WriteProcessPID(root, pid); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				state, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(state), "T") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("daemon did not stop: %s", state)
				}
				time.Sleep(10 * time.Millisecond)
			}

			if registered {
				err = StopDaemon(DaemonInfo{PID: pid, Root: root})
			} else {
				err = Stop(root)
			}
			if err != nil {
				t.Fatalf("%s() = %v", name, err)
			}
			if processAlive(pid) {
				t.Fatal("stop returned with the suspended daemon alive")
			}
			<-waited
			if waitErr != nil {
				t.Fatalf("daemon did not exit through its shutdown handler: %v", waitErr)
			}
			if daemons := ListDaemons(); len(daemons) != 0 {
				t.Fatalf("stopped daemon still registered: %+v", daemons)
			}
		})
	}
}

func TestSuspendedDaemonHelperProcess(t *testing.T) {
	root := os.Getenv("CODEMAP_SUSPENDED_HELPER")
	if root == "" {
		return
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM)
	defer signal.Stop(stopped)
	if err := os.WriteFile(filepath.Join(root, "ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-stopped
}
