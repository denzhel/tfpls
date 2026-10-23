// Package setup installs OpenTofu and conftest from their GitHub releases (tfpls setup):
//   - every download is retried with backoff (a transient network error must not fail a run),
//   - every archive is verified against the SHA-256 listed in the release's checksum file,
//   - tools are cached under the runner's tool cache, so a self-hosted runner downloads each version once.
//
// Environment: TOFU_VERSION (empty = what the repository asks for, else the default), CONFTEST_VERSION,
// INSTALL_CONFTEST ("false" skips conftest). Test overrides: TFPLS_TOFU_BASE_URL, TFPLS_CONFTEST_BASE_URL,
// TFPLS_TOOL_CACHE, TFPLS_RETRY_DELAY (seconds), TFPLS_OS, TFPLS_ARCH.
package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/ghactions"
)

// The default versions (tools/tool-versions.sh opens an issue when they fall behind the releases).
const (
	DefaultTofu     = "1.13.1"
	DefaultConftest = "0.71.1"
)

const (
	maxAttempts = 4
	maxDownload = 1 << 30 // bytes; a release archive is tens of megabytes
	maxBinary   = 1 << 30
)

// the version ends up in a URL and in a cache path: only plain versions (1.2.3, 1.2.3-rc.1) are accepted
var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.]*)?$`)

// Env is everything Run touches outside the process, injected for tests.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	HTTP   *http.Client
	Sleep  func(time.Duration)
}

// DefaultEnv is the real process environment.
func DefaultEnv() Env {
	d := &net.Dialer{Timeout: 20 * time.Second}
	return Env{
		Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, Sleep: time.Sleep,
		HTTP: &http.Client{Timeout: 300 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: d.DialContext, TLSHandshakeTimeout: 20 * time.Second}},
	}
}

// Run implements `tfpls setup` (install) and `tfpls setup detect [dir]` (print the version a repository asks for).
func Run(args []string, e Env) int {
	if len(args) > 0 && args[0] == "detect" {
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		if v := DetectVersion(dir); v != "" {
			fmt.Fprintln(e.Stdout, v)
		}
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintln(e.Stderr, "usage: tfpls setup [detect [dir]]")
		return 2
	}
	gh := &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}
	s := &installer{e: e, gh: gh}
	if err := s.run(); err != nil {
		gh.Error(err.Error())
		return 1
	}
	return 0
}

type installer struct {
	e     Env
	gh    *ghactions.Runner
	os    string // linux, darwin
	arch  string // amd64, arm64
	cache string
	delay time.Duration
}

func (s *installer) env(name, def string) string {
	if v := s.e.Getenv(name); v != "" {
		return v
	}
	return def
}

func (s *installer) run() error {
	tofu := s.e.Getenv("TOFU_VERSION")
	if tofu == "" {
		ws := s.e.Getenv("GITHUB_WORKSPACE")
		if ws == "" {
			ws = "."
		}
		if tofu = DetectVersion(ws); tofu != "" {
			s.gh.Notice("OpenTofu " + tofu + " (asked for by the repository)")
		} else {
			tofu = DefaultTofu
			s.gh.Notice("OpenTofu " + tofu + " (default; add .opentofu-version to choose)")
		}
	}
	conftest := s.env("CONFTEST_VERSION", DefaultConftest)
	for _, v := range []string{tofu, conftest} {
		if !versionRe.MatchString(v) {
			return fmt.Errorf("not a valid version: %q", v)
		}
	}

	switch strings.ToLower(s.env("TFPLS_OS", runtime.GOOS)) {
	case "linux":
		s.os = "linux"
	case "darwin":
		s.os = "darwin"
	default:
		return errors.New("unsupported OS")
	}
	switch s.env("TFPLS_ARCH", runtime.GOARCH) {
	case "x86_64", "amd64":
		s.arch = "amd64"
	case "aarch64", "arm64":
		s.arch = "arm64"
	default:
		return errors.New("unsupported architecture")
	}
	cArch := "x86_64"
	if s.arch == "arm64" {
		cArch = "arm64"
	}
	cOS := "Linux"
	if s.os == "darwin" {
		cOS = "Darwin"
	}
	s.cache = s.e.Getenv("TFPLS_TOOL_CACHE")
	if s.cache == "" {
		base := s.e.Getenv("RUNNER_TOOL_CACHE")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".cache")
		}
		s.cache = filepath.Join(base, "tfpls")
	}
	s.delay = 3 * time.Second
	if d := s.e.Getenv("TFPLS_RETRY_DELAY"); d != "" {
		n, err := strconv.ParseFloat(d, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("bad TFPLS_RETRY_DELAY %q", d)
		}
		s.delay = time.Duration(n * float64(time.Second))
	}

	tb := s.env("TFPLS_TOFU_BASE_URL", "https://github.com/opentofu/opentofu/releases/download")
	cb := s.env("TFPLS_CONFTEST_BASE_URL", "https://github.com/open-policy-agent/conftest/releases/download")

	tofuFile := fmt.Sprintf("tofu_%s_%s_%s.zip", tofu, s.os, s.arch)
	if err := s.install("opentofu", tofu, tb+"/v"+tofu+"/"+tofuFile, tb+"/v"+tofu+"/tofu_"+tofu+"_SHA256SUMS", tofuFile, "tofu"); err != nil {
		return err
	}
	if s.e.Getenv("INSTALL_CONFTEST") == "false" {
		fmt.Fprintln(s.e.Stdout, "conftest: skipped (no policy configured)")
		return nil
	}
	cFile := fmt.Sprintf("conftest_%s_%s_%s.tar.gz", conftest, cOS, cArch)
	return s.install("conftest", conftest, cb+"/v"+conftest+"/"+cFile, cb+"/v"+conftest+"/checksums.txt", cFile, "conftest")
}

func (s *installer) install(name, ver, url, sumsURL, file, bin string) error {
	dest := filepath.Join(s.cache, name, ver, s.os+"-"+s.arch)
	if fi, err := os.Stat(filepath.Join(dest, bin)); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o100 != 0 {
		fmt.Fprintf(s.e.Stdout, "%s %s: cached at %s\n", name, ver, dest)
		return s.gh.AddPath(dest)
	}
	tmp, err := os.MkdirTemp("", "tfpls-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "archive")
	have, err := s.fetch(url, archive)
	if err != nil {
		return err
	}
	sumsFile := filepath.Join(tmp, "sums")
	if _, err := s.fetch(sumsURL, sumsFile); err != nil {
		return err
	}
	sums, err := os.ReadFile(sumsFile)
	if err != nil {
		return err
	}
	want := checksumFor(string(sums), file)
	if want == "" {
		return fmt.Errorf("no checksum for %s in %s", file, sumsURL)
	}
	if want != have {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", file, want, have)
	}
	var data []byte
	switch {
	case strings.HasSuffix(file, ".zip"):
		data, err = fromZip(archive, bin)
	case strings.HasSuffix(file, ".tar.gz"):
		data, err = fromTarGz(archive, bin)
	default:
		return fmt.Errorf("unknown archive type: %s", file)
	}
	if err != nil {
		return fmt.Errorf("%s in %s: %w", bin, file, err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	// into place atomically enough for concurrent runners sharing the cache
	part := filepath.Join(dest, fmt.Sprintf("%s.%d", bin, os.Getpid()))
	if err := os.WriteFile(part, data, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(part, 0o755); err != nil {
		return err
	}
	if err := os.Rename(part, filepath.Join(dest, bin)); err != nil {
		return err
	}
	fmt.Fprintf(s.e.Stdout, "%s %s: installed to %s (checksum verified)\n", name, ver, dest)
	return s.gh.AddPath(dest)
}

// checksumFor finds the entry "<hex>  <file>" or "<hex> *<file>" (exact name) in a checksum file.
func checksumFor(sums, file string) string {
	for _, l := range strings.Split(sums, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && (f[1] == file || f[1] == "*"+file) && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0])
			}
		}
	}
	return ""
}

// fetch downloads url to out and returns the SHA-256 of what it wrote. A server error, a rate limit and a network
// error are retried (4 attempts, growing backoff); another client error (404, 403) is final.
func (s *installer) fetch(url, out string) (string, error) {
	var last error
	for n := 1; ; n++ {
		sum, retry, err := s.try(url, out)
		if err == nil {
			return sum, nil
		}
		last = err
		if !retry || n >= maxAttempts {
			if retry {
				return "", fmt.Errorf("download failed after %d attempts: %s (%v)", maxAttempts, url, last)
			}
			return "", fmt.Errorf("download failed: %s (%v)", url, last)
		}
		s.gh.Warning(fmt.Sprintf("download failed (attempt %d of %d), retrying: %s", n, maxAttempts, url))
		s.e.Sleep(s.delay * time.Duration(n))
	}
}

func (s *installer) try(url, out string) (sum string, retry bool, err error) {
	resp, err := s.e.HTTP.Get(url)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry = resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout
		return "", retry, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(out)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return "", true, err
	}
	if n > maxDownload {
		return "", false, errors.New("download too large")
	}
	return hex.EncodeToString(h.Sum(nil)), false, nil
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBinary {
		return nil, errors.New("binary too large")
	}
	return b, nil
}

// fromZip returns the entry named exactly bin (the archive's top level) and ignores every other entry.
func fromZip(path, bin string) ([]byte, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == bin && f.Mode().IsRegular() {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return readLimited(r)
		}
	}
	return nil, errors.New("not found")
}

func fromTarGz(path, bin string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	t := tar.NewReader(gz)
	for {
		h, err := t.Next()
		if err == io.EOF {
			return nil, errors.New("not found")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && strings.TrimPrefix(h.Name, "./") == bin {
			return readLimited(t)
		}
	}
}
