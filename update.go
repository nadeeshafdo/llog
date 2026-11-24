package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	updateOwner   = "nadeeshafdo"
	updateRepo    = "llog"
	updateTimeout = 30 * time.Second
	maxAssetSize  = 100 << 20
)

type releaseAsset struct {
	name string
	url  string
}

type releaseInfo struct {
	tag    string
	assets map[string]string // asset name -> download URL
}

func githubToken() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

func updateClient() *http.Client {
	return &http.Client{Timeout: updateTimeout}
}

func getURL(client *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetSize+1))
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if len(body) > maxAssetSize {
		return nil, fmt.Errorf("asset exceeds size limit (%d bytes)", maxAssetSize)
	}
	return body, nil
}

func fetchLatest(client *http.Client, apiBase, owner, repo, token string) (releaseInfo, error) {
	var rel releaseInfo
	data, err := getURL(client, strings.TrimSuffix(apiBase, "/")+"/repos/"+owner+"/"+repo+"/releases/latest", token)
	if err != nil {
		return rel, err
	}
	var parsed struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return rel, fmt.Errorf("cannot parse release info: %w", err)
	}
	if parsed.TagName == "" {
		return rel, errors.New("release has no tag")
	}
	rel.tag = parsed.TagName
	rel.assets = make(map[string]string, len(parsed.Assets))
	for _, a := range parsed.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
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

// extractSingle unpacks a tar.gz or zip and returns the binary, preferring
// the entry named binName and falling back to the first regular file.
func extractSingle(archive []byte, filename, binName string) ([]byte, error) {
	if strings.HasSuffix(filename, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("cannot read zip: %w", err)
		}
		var fallback []byte
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(io.LimitReader(rc, maxAssetSize+1))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if len(data) == 0 {
				continue
			}
			if filepath.Base(f.Name) == binName {
				return data, nil
			}
			if fallback == nil {
				fallback = data
			}
		}
		if fallback != nil {
			return fallback, nil
		}
		return nil, errors.New("no binary in archive")
	}
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("cannot read gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var fallback []byte
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
		data, err := io.ReadAll(io.LimitReader(tr, maxAssetSize+1))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		if filepath.Base(hdr.Name) == binName {
			return data, nil
		}
		if fallback == nil {
			fallback = data
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("no binary in archive")
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
	data, err := getURL(client, assetURL, token)
	if err != nil {
		return "", false, err
	}
	sums, err := getURL(client, sumsURL, token)
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
	return nil
}

// runUpdate performs a self-update of the running binary.
func runUpdate(out io.Writer) error {
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
	tag, updated, err := performUpdate(updateClient(), "https://api.github.com", updateOwner, updateRepo, githubToken(), exe, cur)
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
