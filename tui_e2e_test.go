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

// TestTuiLoopEndToEnd drives the real Tea program through a pty: it waits
// for the first rendered frame, types text, saves with Ctrl+S, and checks
// the result. Piped input cannot do this faithfully because startup input
// races terminal detection.
func TestTuiLoopEndToEnd(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no ptmx: %v", err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlockpt: %v", err)
	}
	ptn, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("getptn: %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(ptn), os.O_RDWR, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	defer slave.Close()

	t.Setenv("TERM", "xterm-256color")

	var mu sync.Mutex
	var transcript strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
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

	type outcome struct {
		res editResult
		err error
	}
	resCh := make(chan outcome, 1)
	go func() {
		entries := []Entry{
			{Header: "2002..", Text: "2002.. first memory"},
			{Header: "2026/11/17..", Text: "2026/11/17.."},
		}
		res, err := tuiLoop(slave, slave, entries, "2026/11/24 18:30..", nil)
		resCh <- outcome{res, err}
	}()

	waitFor := func(what string) {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			found := strings.Contains(transcript.String(), what)
			mu.Unlock()
			if found {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q in transcript", what)
	}

	waitFor("-- new entry --")
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
