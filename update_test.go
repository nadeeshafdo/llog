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
	f, err := zw.Create("llog")
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
		t.Fatalf("zip got %q err=%v", got, err)
	}
	if _, err := extractSingle(zbuf.Bytes(), "x.zip", "other"); err == nil {
		t.Fatal("missing binary name must fail, not fall back")
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
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"llog-linux-amd64.tar.gz","url":%q,"browser_download_url":%q},{"name":"SHA256SUMS","url":%q,"browser_download_url":%q}]}`,
			f.tag, host+"/llog-linux-amd64.tar.gz", host+"/browser/llog-linux-amd64.tar.gz", host+"/SHA256SUMS", host+"/browser/SHA256SUMS")
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

func TestPerformUpdateWithFileToken(t *testing.T) {
	var gotAuth []string
	var gotAccept []string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		host := "http://" + r.Host
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":"llog-linux-amd64.tar.gz","url":%q,"browser_download_url":%q},{"name":"SHA256SUMS","url":%q,"browser_download_url":%q}]}`,
			host+"/llog-linux-amd64.tar.gz", host+"/browser/llog-linux-amd64.tar.gz", host+"/SHA256SUMS", host+"/browser/SHA256SUMS")
	})
	asset := makeTarGz(t, "llog", []byte("updated-binary"))
	mux.HandleFunc("/llog-linux-amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		gotAccept = append(gotAccept, r.Header.Get("Accept"))
		w.Write(asset)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		gotAccept = append(gotAccept, r.Header.Get("Accept"))
		fmt.Fprintf(w, "%x  llog-linux-amd64.tar.gz\n", sha256.Sum256(asset))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"github_token": "filetok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	installPath := filepath.Join(dir, "llog")
	if err := os.WriteFile(installPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	tag, updated, err := performUpdate(updateClient(), srv.URL, "o", "r", fileToken(authPath), installPath, "v0.0.1")
	if err != nil || !updated || tag != "v9.9.9" {
		t.Fatalf("got tag=%q updated=%v err=%v", tag, updated, err)
	}
	for _, h := range gotAuth {
		if h != "Bearer filetok" {
			t.Fatalf("file token not sent correctly: %q", gotAuth)
		}
	}
	for _, a := range gotAccept {
		if a != "application/octet-stream" {
			t.Fatalf("asset Accept must be octet-stream, got %q", gotAccept)
		}
	}
	if len(gotAccept) != 2 {
		t.Fatalf("expected 2 asset fetches with octet-stream, got %d", len(gotAccept))
	}
	if got, _ := os.ReadFile(installPath); string(got) != "updated-binary" {
		t.Fatalf("installed = %q", got)
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

func TestRequireHTTPS(t *testing.T) {
	if err := requireHTTPS("https://api.github.com/x"); err != nil {
		t.Fatalf("https refused: %v", err)
	}
	if err := requireHTTPS("http://127.0.0.1:9/x"); err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	if err := requireHTTPS("http://localhost:9/x"); err != nil {
		t.Fatalf("localhost refused: %v", err)
	}
	if err := requireHTTPS("http://example.com/x"); err == nil {
		t.Fatal("cleartext accepted")
	}
	if err := requireHTTPS("ftp://example.com/x"); err == nil {
		t.Fatal("ftp accepted")
	}
}

func TestSecureRedirectStripsToken(t *testing.T) {
	var gotAuth []string
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
	}))
	defer final.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, final.URL+"/asset", http.StatusFound)
	}))
	defer first.Close()

	req, _ := http.NewRequest(http.MethodGet, first.URL, nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{CheckRedirect: secureRedirect}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(gotAuth) != 1 || gotAuth[0] != "" {
		t.Fatalf("token forwarded cross-host: %q", gotAuth)
	}
}

func TestSecureRedirectSameHostPasses(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://a.example/asset", nil)
	prev, _ := http.NewRequest(http.MethodGet, "https://a.example/release", nil)
	if err := secureRedirect(req, []*http.Request{prev}); err != nil {
		t.Fatalf("same-host https should pass: %v", err)
	}
}

func TestSecureRedirectRefusesDowngrade(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://cdn.example/asset", nil)
	prev, _ := http.NewRequest(http.MethodGet, "https://api.example/release", nil)
	if err := secureRedirect(req, []*http.Request{prev}); err == nil {
		t.Fatal("http downgrade should be refused")
	}
}

func TestCleanTag(t *testing.T) {
	if got := cleanTag("v1.2.3\nINJECTED\r\n"); got != "v1.2.3INJECTED" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractRejectsFallback(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "other", Mode: 0o755, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractSingle(buf.Bytes(), "x.tar.gz", "llog"); err == nil {
		t.Fatal("missing binary name must fail, not fall back")
	}
}

func TestExtractSkipsSymlink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: "llog"}
	fh.SetMode(os.ModeSymlink | 0o755)
	f, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("evil-target")); err != nil {
		t.Fatal(err)
	}
	f2, err := zw.Create("llog")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.Write([]byte("real-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractSingle(buf.Bytes(), "x.zip", "llog")
	if err != nil || string(got) != "real-bytes" {
		t.Fatalf("symlink must be skipped, got %q err=%v", got, err)
	}

	var buf2 bytes.Buffer
	zw2 := zip.NewWriter(&buf2)
	fh2 := &zip.FileHeader{Name: "llog"}
	fh2.SetMode(os.ModeSymlink | 0o755)
	f3, err := zw2.CreateHeader(fh2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f3.Write([]byte("evil-target")); err != nil {
		t.Fatal(err)
	}
	if err := zw2.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractSingle(buf2.Bytes(), "x.zip", "llog"); err == nil {
		t.Fatal("symlink alone must not count as the binary")
	}
}

func TestExtractSizeCap(t *testing.T) {
	old := maxAssetBytes
	maxAssetBytes = 16
	defer func() { maxAssetBytes = old }()
	data := makeTarGz(t, "llog", []byte("0123456789abcdef0123456789"))
	if _, err := extractSingle(data, "x.tar.gz", "llog"); err == nil {
		t.Fatal("over-limit extract must fail")
	}
}

func TestExtractFileCountCap(t *testing.T) {
	old := maxArchiveFiles
	maxArchiveFiles = 2
	defer func() { maxArchiveFiles = old }()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, n := range []string{"a", "b", "c"} {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractSingle(buf.Bytes(), "x.tar.gz", "llog"); err == nil {
		t.Fatal("too many files must fail")
	}
}

func TestOversizedJSONRefused(t *testing.T) {
	old := maxAPIBytes
	maxAPIBytes = 64
	defer func() { maxAPIBytes = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"` + strings.Repeat("v", 200) + `"}`))
	}))
	defer srv.Close()
	_, err := fetchLatest(updateClient(), srv.URL, "o", "r", "")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected size error, got %v", err)
	}
}
