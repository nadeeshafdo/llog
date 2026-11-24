package main

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type e2eOutcome struct {
	res editResult
	err error
}

func openTestPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		t.Skipf("unlockpt: %v", err)
	}
	ptn, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		t.Skipf("getptn: %v", err)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(ptn), os.O_RDWR, 0)
	if err != nil {
		master.Close()
		t.Skipf("open slave: %v", err)
	}
	return master, slave
}

// TestTuiLoopEndToEnd drives the real Tea program through a pty: it waits
// for the first rendered frame, types text, saves with Ctrl+S, and checks
// the result. Piped input cannot do this faithfully because startup input
// races terminal detection.
func TestTuiLoopEndToEnd(t *testing.T) {
	master, slave := openTestPty(t)
	defer master.Close()
	defer slave.Close()

	t.Setenv("TERM", "xterm-256color")

	var mu sync.Mutex
	var transcript strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				transcript.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	resCh := make(chan e2eOutcome, 1)
	go func() {
		entries := []Entry{
			{Header: "2002..", Text: "2002.. first memory"},
			{Header: "2026/11/17..", Text: "2026/11/17.."},
		}
		res, err := tuiLoop(slave, slave, entries, "2026/11/24 18:30..", nil)
		resCh <- e2eOutcome{res, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		found := strings.Contains(transcript.String(), "-- new entry --")
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first frame")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := master.Write([]byte("hello tui")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := master.Write([]byte{0x13}); err != nil {
		t.Fatal(err)
	}

	select {
	case out := <-resCh:
		if out.err != nil {
			t.Fatalf("tuiLoop: %v", out.err)
		}
		if !out.res.saved {
			t.Fatal("expected saved result")
		}
		if !strings.Contains(out.res.body, "hello tui") {
			t.Fatalf("body = %q", out.res.body)
		}
		if out.res.editIdx != -1 {
			t.Fatalf("editIdx = %d", out.res.editIdx)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for save")
	}
}

// Note: terminal death mid-session (pty closed, SSH drop) is owned by the
// process lifecycle (SIGHUP kills the process). Bubble Tea may block on a
// dead terminal instead of returning; no write can happen without an
// explicit save key, so the log stays untouched. Tested manually, not in CI
// because it hangs the suite by design.

// TestTuiLoopMidLineEdit types "ac", moves left, inserts "b", saves:
// the cursor path end to end on a real pty.
func TestTuiLoopMidLineEdit(t *testing.T) {
	master, slave := openTestPty(t)
	defer master.Close()
	defer slave.Close()

	t.Setenv("TERM", "xterm-256color")

	var mu sync.Mutex
	var transcript strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				transcript.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	resCh := make(chan e2eOutcome, 1)
	go func() {
		entries := []Entry{
			{Header: "2002..", Text: "2002.. first memory"},
		}
		res, err := tuiLoop(slave, slave, entries, "2026/11/24 18:30..", nil)
		resCh <- e2eOutcome{res, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		found := strings.Contains(transcript.String(), "-- new entry --")
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first frame")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, chunk := range [][]byte{[]byte("ac"), {0x1b, '[', 'D'}, []byte("b"), {0x13}} {
		if _, err := master.Write(chunk); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	select {
	case out := <-resCh:
		if out.err != nil {
			t.Fatalf("tuiLoop: %v", out.err)
		}
		if !out.res.saved {
			t.Fatal("expected saved result")
		}
		if out.res.body != "abc" {
			t.Fatalf("body = %q, want mid-line edit", out.res.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for save")
	}
}
