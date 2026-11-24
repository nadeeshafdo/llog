package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSemBase(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v0.1.0", [3]int{0, 1, 0}, true},
		{"0.2.3", [3]int{0, 2, 3}, true},
		{"v10.20.30", [3]int{10, 20, 30}, true},
		{"v0.1.0-2-gabcdef0", [3]int{0, 1, 0}, true},
		{"v0.1.0-dirty", [3]int{0, 1, 0}, true},
		{"v0.1.0-2-gabcdef0-dirty", [3]int{0, 1, 0}, true},
		{"abc1234", [3]int{}, false},
		{"dev", [3]int{}, false},
		{"v1.2", [3]int{}, false},
		{"v1.2.x", [3]int{}, false},
		{"v0.1.0-foo", [3]int{}, false},
		{"", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := semBase(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("semBase(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
		wantErr     bool
	}{
		{"v0.1.0", "v0.1.1", true, false},
		{"v0.1.1", "v0.1.0", false, false},
		{"v0.1.0", "v0.1.0", false, false},
		{"v0.1.0-2-gabcdef0", "v0.1.0", false, false},
		{"abc1234", "v0.1.0", true, false},
		{"dev", "v0.1.0", true, false},
		{"v0.1.0", "nonsense", false, true},
	}
	for _, c := range cases {
		got, err := needsUpdate(c.cur, c.latest)
		if (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
			t.Fatalf("needsUpdate(%q,%q) = %v,%v", c.cur, c.latest, got, err)
		}
	}
}

func TestSelectAsset(t *testing.T) {
	name, bin, err := selectAsset("linux", "amd64")
	if err != nil || name != "llog-linux-amd64.tar.gz" || bin != "llog" {
		t.Fatalf("got %q %q %v", name, bin, err)
	}
	if _, _, err := selectAsset("windows", "amd64"); err == nil {
		t.Fatal("windows should be unsupported")
	}
	if _, _, err := selectAsset("linux", "386"); err == nil {
		t.Fatal("386 should be unsupported")
	}
	if _, _, err := selectAsset("plan9", "amd64"); err == nil {
		t.Fatal("plan9 should be unsupported")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("binary-bytes")
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	lister := sum + "  llog-linux-amd64.tar.gz\n"
	if err := verifyChecksum(data, []byte(lister), "llog-linux-amd64.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum([]byte("tampered"), []byte(lister), "llog-linux-amd64.tar.gz"); err == nil {
		t.Fatal("tampered bytes must fail")
	}
	if err := verifyChecksum(data, []byte(lister), "other.tar.gz"); err == nil {
		t.Fatal("missing entry must fail")
	}
}

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractSingle(t *testing.T) {
	data := makeTarGz(t, "llog", []byte("new-binary"))
	got, err := extractSingle(data, "llog-linux-amd64.tar.gz", "llog")
	if err != nil || string(got) != "new-binary" {
		t.Fatalf("got %q %v", got, err)
	}
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, err := zw.Create("other-name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("zip-binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = extractSingle(zbuf.Bytes(), "x.zip", "llog")
	if err != nil || string(got) != "zip-binary" {
		t.Fatalf("zip fallback got %q %v", got, err)
	}
	if _, err := extractSingle([]byte("garbage"), "x.tar.gz", "llog"); err == nil {
		t.Fatal("garbage should fail")
	}
}

// fakeGitHub serves a latest-release JSON, one tarball and its checksums.
type fakeGitHub struct {
	t     *testing.T
	tag   string
	asset []byte
	hits  *int64
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(f.hits, 1)
		host := "http://" + r.Host
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"llog-linux-amd64.tar.gz","browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`,
			f.tag, host+"/llog-linux-amd64.tar.gz", host+"/SHA256SUMS")
	})
	mux.HandleFunc("/llog-linux-amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(f.hits, 1)
		w.Write(f.asset)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(f.hits, 1)
		fmt.Fprintf(w, "%x  llog-linux-amd64.tar.gz\n", sha256.Sum256(f.asset))
	})
	return mux
}

func TestPerformUpdateE2E(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform-specific asset names")
	}
	var hits int64
	fg := &fakeGitHub{t: t, tag: "v9.9.9", hits: &hits}
	fg.asset = makeTarGz(t, "llog", []byte("brand-new-binary"))
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "llog")
	if err := os.WriteFile(installPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	tag, updated, err := performUpdate(updateClient(), srv.URL, "o", "r", "", installPath, "v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !updated || tag != "v9.9.9" {
		t.Fatalf("got tag=%q updated=%v", tag, updated)
	}
	got, err := os.ReadFile(installPath)
	if err != nil || string(got) != "brand-new-binary" {
		t.Fatalf("installed = %q err=%v", got, err)
	}
	fi, err := os.Stat(installPath)
	if err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary not executable: %v", fi)
	}
	old, err := os.ReadFile(installPath + ".bak")
	if err != nil || string(old) != "old-binary" {
		t.Fatalf("backup = %q err=%v", old, err)
	}

	// Already there: no asset download at all.
	atomic.StoreInt64(&hits, 0)
	tag, updated, err = performUpdate(updateClient(), srv.URL, "o", "r", "", installPath, "v9.9.9")
	if err != nil || updated || tag != "v9.9.9" {
		t.Fatalf("up-to-date gave tag=%q updated=%v err=%v", tag, updated, err)
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("up-to-date check hit the server %d times, want 1 (release only)", n)
	}

	// Ahead of the release: no downgrade.
	_, updated, err = performUpdate(updateClient(), srv.URL, "o", "r", "", installPath, "v9.9.9-1-gabcdef0")
	if err != nil || updated {
		t.Fatalf("ahead-of-release gave updated=%v err=%v", updated, err)
	}
}

func TestPerformUpdateDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, _, err := performUpdate(updateClient(), srv.URL, "o", "r", "", filepath.Join(t.TempDir(), "llog"), "v0.0.1"); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("expected token hint, got %v", err)
	}
}
