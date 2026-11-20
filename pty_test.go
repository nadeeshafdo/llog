package main

import (
	"bufio"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// These tests drive readKey through a real pty, which is the only faithful
// way to test lone-Esc detection: pipes and ptys behave differently.
// Regression test: lone Esc used to hang forever waiting for sequence bytes.

// errKeyTimeout aborts a test instead of hanging the suite if key reading
// ever blocks again.
type errKeyTimeout struct{}

func (errKeyTimeout) Error() string { return "timed out waiting for key" }

func openPty(t *testing.T) (master, slave *os.File) {
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
	state, err := term.MakeRaw(int(slave.Fd()))
	if err != nil {
		master.Close()
		slave.Close()
		t.Skipf("makeraw: %v", err)
	}
	t.Cleanup(func() { term.Restore(int(slave.Fd()), state) })
	return master, slave
}

func readKeyTimeout(t *testing.T, r *bufio.Reader, f *os.File) key {
	t.Helper()
	type res struct {
		k   key
		err error
	}
	ch := make(chan res, 1)
	go func() {
		k, err := readKey(r, f)
		ch <- res{k, err}
	}()
	select {
	case out := <-ch:
		if out.err != nil {
			t.Fatalf("readKey: %v", out.err)
		}
		return out.k
	case <-time.After(3 * time.Second):
		t.Fatal(errKeyTimeout{})
		return key{}
	}
}

func TestPtyLoneEsc(t *testing.T) {
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()
	if _, err := master.Write([]byte{keyEsc}); err != nil {
		t.Fatal(err)
	}
	if k := readKeyTimeout(t, bufio.NewReader(slave), slave); k.kind != kEsc {
		t.Fatalf("lone ESC on pty gave %v, want kEsc", k.kind)
	}
}

func TestPtyArrow(t *testing.T) {
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()
	if _, err := master.Write([]byte{keyEsc, '[', 'A'}); err != nil {
		t.Fatal(err)
	}
	if k := readKeyTimeout(t, bufio.NewReader(slave), slave); k.kind != kUp {
		t.Fatalf("arrow on pty gave %v, want kUp", k.kind)
	}
}
