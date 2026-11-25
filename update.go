package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	updateOwner   = "nadeeshafdo"
	updateRepo    = "llog"
	updateTimeout = 30 * time.Second
)

var (
	// Separate caps keep attacker-controlled JSON small while archives
	// may be large. Tests shrink these.
	maxAPIBytes     = int64(1 << 20)
	maxSumsBytes    = int64(64 << 10)
	maxAssetBytes   = int64(100 << 20)
	maxArchiveFiles = 1024
)

type releaseAsset struct {
	name string
	url  string
}

type releaseInfo struct {
	tag    string
	assets map[string]string // asset name -> download URL
}

// githubToken prefers the env, then falls back to the token file that
// lives beside the log (auth.json). Env always wins for scripting.
func githubToken(authPath string) string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return fileToken(authPath)
}

// fileToken reads {"github_token": "..."} from path. The card holding the
// log is LUKS-encrypted, which is the real protection; file bits are set
// best-effort on write but cannot be relied on (exFAT ignores them).
func fileToken(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var parsed struct {
		GitHubToken string `json:"github_token"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.GitHubToken)
}

func updateClient() *http.Client {
	return &http.Client{Timeout: updateTimeout, CheckRedirect: secureRedirect}
}

// secureRedirect follows redirects without leaking credentials or
// downgrading transport: Authorization never leaves its host and http
// targets are refused outside loopback test hosts.
func secureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" && !isLoopbackHost(req.URL.Hostname()) {
		return fmt.Errorf("refusing non-https redirect to %s", redactHost(req.URL))
	}
	if n := len(via); n > 0 && req.URL.Host != via[n-1].URL.Host {
		req.Header.Del("Authorization")
	}
	return nil
}

func redactHost(u *url.URL) string {
	return u.Scheme + "://" + u.Host + "/..."
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	return net.ParseIP(h).IsLoopback()
}

// requireHTTPS refuses non-https URLs except loopback test hosts, so a
// compromised API response cannot push the updater onto cleartext.
func requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("refusing non-https URL %s", redactHost(u))
}

func getURL(client *http.Client, url, token string, maxBytes int64) ([]byte, error) {
	return getWithAccept(client, url, token, "application/vnd.github+json", maxBytes)
}

// getAsset fetches a release file through the Releases API. Private repos
// reject Bearer tokens on browser_download_url (github.com/.../download/...)
// with 404, so assets must use the API asset `url` with
// Accept: application/octet-stream, which 302-redirects to a signed URL.
func getAsset(client *http.Client, url, token string, maxBytes int64) ([]byte, error) {
	return getWithAccept(client, url, token, "application/octet-stream", maxBytes)
}

func getWithAccept(client *http.Client, url, token, accept string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("cannot access release (HTTP %d): private repos need GH_TOKEN with repo scope", resp.StatusCode)
		}
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds size limit (%d bytes)", maxBytes)
	}
	return body, nil
}

func fetchLatest(client *http.Client, apiBase, owner, repo, token string) (releaseInfo, error) {
	var rel releaseInfo
	apiURL := strings.TrimSuffix(apiBase, "/") + "/repos/" + owner + "/" + repo + "/releases/latest"
	if err := requireHTTPS(apiURL); err != nil {
		return rel, err
	}
	data, err := getURL(client, apiURL, token, maxAPIBytes)
	if err != nil {
		return rel, err
	}
	var parsed struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			// BrowserURL is the github.com download link. It works without
			// auth for public repos but 404s with Bearer tokens on private
			// repos, so it is only a fallback when the API url is absent
			// (old test fixtures).
			BrowserURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return rel, fmt.Errorf("cannot parse release info: %w", err)
	}
	if parsed.TagName == "" {
		return rel, errors.New("release has no tag")
	}
	rel.tag = cleanTag(parsed.TagName)
	if rel.tag == "" {
		return rel, errors.New("release has no tag")
	}
	rel.assets = make(map[string]string, len(parsed.Assets))
	for _, a := range parsed.Assets {
		u := a.URL
		if u == "" {
			u = a.BrowserURL
		}
		rel.assets[a.Name] = u
	}
	return rel, nil
}

// cleanTag strips newlines so a tag can never spoof terminal output.
func cleanTag(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
}

// selectAsset maps the runtime platform to a release asset name.
func selectAsset(goos, goarch string) (archive, bin string, err error) {
	switch goos {
	case "linux", "darwin":
		switch goarch {
		case "amd64", "arm64":
			return fmt.Sprintf("llog-%s-%s.tar.gz", goos, goarch), "llog", nil
		default:
			return "", "", fmt.Errorf("no build for %s/%s", goos, goarch)
		}
	default:
		return "", "", fmt.Errorf("self-update on %s is not supported yet", goos)
	}
}

// semBase reduces a version to its numeric base: tags, git describe output
// (v1.2.3-4-gHASH, optional -dirty) and bare versions. Anything else fails.
func semBase(v string) ([3]int, bool) {
	var zero [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimSuffix(v, "-dirty")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		rest := v[i+1:]
		dash := strings.IndexByte(rest, '-')
		if dash < 0 {
			return zero, false
		}
		if _, err := strconv.Atoi(rest[:dash]); err != nil {
			return zero, false
		}
		gh := rest[dash+1:]
		if !strings.HasPrefix(gh, "g") || len(gh) < 2 {
			return zero, false
		}
		for _, r := range gh[1:] {
			if !isHexDigit(r) {
				return zero, false
			}
		}
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return zero, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return zero, false
		}
		out[i] = n
	}
	return out, true
}

func isHexDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// needsUpdate reports whether latest is strictly newer than current.
// Unparseable current versions (dev builds, bare hashes) count as behind;
// an unparseable latest tag is an error rather than a guess.
func needsUpdate(current, latest string) (bool, error) {
	lb, ok := semBase(latest)
	if !ok {
		return false, fmt.Errorf("cannot parse latest version %q", latest)
	}
	cb, ok := semBase(current)
	if !ok {
		return true, nil
	}
	for i := 0; i < 3; i++ {
		if lb[i] != cb[i] {
			return lb[i] > cb[i], nil
		}
	}
	return false, nil
}

// verifyChecksum checks data against a sha256sum-format listing.
func verifyChecksum(data []byte, sums []byte, name string) error {
	want := fmt.Sprintf("%x", sha256.Sum256(data))
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if strings.TrimPrefix(f[1], "*") == name {
			if !strings.EqualFold(f[0], want) {
				return fmt.Errorf("checksum mismatch for %s", name)
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s", name)
}

// extractSingle unpacks a tar.gz or zip and returns the entry named
// binName. Anything else is refused: falling back to an arbitrary first
// file would let a malformed archive install the wrong bytes.
func extractSingle(archive []byte, filename, binName string) ([]byte, error) {
	if strings.HasSuffix(filename, ".zip") {
		return extractZip(archive, binName)
	}
	return extractTarGz(archive, binName)
}

func extractZip(archive []byte, binName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("cannot read zip: %w", err)
	}
	files := 0
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !f.FileInfo().Mode().IsRegular() {
			continue
		}
		files++
		if files > maxArchiveFiles {
			return nil, fmt.Errorf("archive holds too many files (limit %d)", maxArchiveFiles)
		}
		if filepath.Base(f.Name) != binName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxAssetBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		if total > maxAssetBytes {
			return nil, fmt.Errorf("extracted data exceeds size limit (%d bytes)", maxAssetBytes)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("archive entry %s is empty", binName)
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive has no %s", binName)
}

func extractTarGz(archive []byte, binName string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("cannot read gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	files := 0
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		files++
		if files > maxArchiveFiles {
			return nil, fmt.Errorf("archive holds too many files (limit %d)", maxArchiveFiles)
		}
		if filepath.Base(hdr.Name) != binName {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxAssetBytes+1))
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		if total > maxAssetBytes {
			return nil, fmt.Errorf("extracted data exceeds size limit (%d bytes)", maxAssetBytes)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("archive entry %s is empty", binName)
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive has no %s", binName)
}

// performUpdate checks the latest release and, when newer, replaces the
// binary at installPath atomically. It returns the new tag and whether an
// update happened. It never touches the log file.
func performUpdate(client *http.Client, apiBase, owner, repo, token, installPath, curVersion string) (string, bool, error) {
	archive, bin, err := selectAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", false, err
	}
	rel, err := fetchLatest(client, apiBase, owner, repo, token)
	if err != nil {
		return "", false, err
	}
	up, err := needsUpdate(curVersion, rel.tag)
	if err != nil {
		return "", false, err
	}
	if !up {
		return rel.tag, false, nil
	}
	assetURL, ok := rel.assets[archive]
	if !ok {
		return "", false, fmt.Errorf("release %s has no asset %s", rel.tag, archive)
	}
	sumsURL, ok := rel.assets["SHA256SUMS"]
	if !ok {
		return "", false, fmt.Errorf("release %s has no SHA256SUMS", rel.tag)
	}
	if err := requireHTTPS(assetURL); err != nil {
		return "", false, err
	}
	if err := requireHTTPS(sumsURL); err != nil {
		return "", false, err
	}
	data, err := getAsset(client, assetURL, token, maxAssetBytes)
	if err != nil {
		return "", false, err
	}
	sums, err := getAsset(client, sumsURL, token, maxSumsBytes)
	if err != nil {
		return "", false, err
	}
	if err := verifyChecksum(data, sums, archive); err != nil {
		return "", false, err
	}
	binData, err := extractSingle(data, archive, bin)
	if err != nil {
		return "", false, err
	}
	if err := replaceBinary(installPath, binData); err != nil {
		return "", false, err
	}
	return rel.tag, true, nil
}

// replaceBinary keeps the old binary as <path>.bak, then atomically swaps
// in the new one with executable permissions.
func replaceBinary(path string, data []byte) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read current binary: %w", err)
	}
	if err := os.WriteFile(path+".bak", old, 0o755); err != nil {
		return fmt.Errorf("cannot back up current binary: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".llog-update-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	// Best-effort durability: the data itself is already synced.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// runUpdate performs a self-update of the running binary.
func runUpdate(out io.Writer, authPath string) error {
	if runtime.GOOS == "windows" {
		return errors.New("self-update on Windows is not supported yet")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}
	bi := readBuildInfo()
	cur := bi.version
	if cur == "" {
		cur = bi.commit
	}
	if cur == "" {
		cur = "dev"
	}
	tag, updated, err := performUpdate(updateClient(), "https://api.github.com", updateOwner, updateRepo, githubToken(authPath), exe, cur)
	if err != nil {
		return err
	}
	if !updated {
		fmt.Fprintf(out, "llog %s is the latest release\n", tag)
		return nil
	}
	fmt.Fprintf(out, "updated llog %s -> %s\n", cur, tag)
	return nil
}

// runConfig stores the GitHub token beside the log file for --update to
// use. The token is read without echo when stdin is a terminal.
func runConfig(in io.Reader, out io.Writer, authPath string) error {
	fmt.Fprint(out, "GitHub token (input hidden, Enter to save): ")
	secret, err := readSecretLine(in)
	fmt.Fprintln(out)
	if err != nil {
		return err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return errors.New("empty token, nothing written")
	}
	if dir := filepath.Dir(authPath); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("cannot create config dir: %w", err)
		}
	}
	data := []byte("{\"github_token\": " + strconv.Quote(secret) + "}\n")
	if err := writeSynced(authPath, data, os.O_WRONLY|os.O_CREATE|os.O_TRUNC); err != nil {
		return fmt.Errorf("cannot write token: %w", err)
	}
	_ = os.Chmod(authPath, 0o600) // best effort; exFAT ignores bits anyway
	fmt.Fprintf(out, "saved token to %s\n", authPath)
	return nil
}

// readSecretLine reads one line, without echo when stdin is a terminal.
func readSecretLine(in io.Reader) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		line, err := term.ReadPassword(int(f.Fd()))
		return string(line), err
	}
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}
