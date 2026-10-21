package setup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectVersion(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"nothing", nil, ""},
		{"opentofu-version", map[string]string{".opentofu-version": "1.11.4\n"}, "1.11.4"},
		{"comments and v", map[string]string{".terraform-version": "# pinned\nv1.9.0  # tenv\n"}, "1.9.0"},
		{"CRLF", map[string]string{".opentofu-version": "1.9.0\r\n"}, "1.9.0"},
		{"opentofu wins", map[string]string{".terraform-version": "1.8.0", ".opentofu-version": "1.11.4"}, "1.11.4"},
		{"tofu beats terraform", map[string]string{".tofu-version": "1.7.1", ".terraform-version": "1.8.0"}, "1.7.1"},
		{"exact required_version", map[string]string{"terraform.tf": "terraform {\n  required_version = \"1.11.4\"\n}\n"}, "1.11.4"},
		{"equals required_version", map[string]string{"versions.tf": "terraform {\n  required_version = \"= 1.10.2\"\n}\n"}, "1.10.2"},
		{"range ignored", map[string]string{"terraform.tf": "required_version = \">= 1.6.0\"\n"}, ""},
		{"pessimistic ignored", map[string]string{"terraform.tf": "required_version = \"~> 1.6\"\n"}, ""},
		{"unusable file falls through", map[string]string{".opentofu-version": "latest", "terraform.tf": "required_version = \"1.5.0\"\n"}, "1.5.0"},
		{"version file beats required_version", map[string]string{".opentofu-version": "1.11.4", "terraform.tf": "required_version = \"1.5.0\"\n"}, "1.11.4"},
		{"unusable first file falls to the next file", map[string]string{".opentofu-version": "latest", ".terraform-version": "1.8.0"}, "1.8.0"},
		// bug fixed: the Bash picked the first *.tf in glob order per locale; Go uses byte order, independent of the locale
		{"two tf files: byte order", map[string]string{"b.tf": "required_version = \"1.2.0\"\n", "a.tf": "required_version = \"1.1.0\"\n"}, "1.1.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for f, b := range c.files {
				write(t, filepath.Join(dir, f), b)
			}
			if got := DetectVersion(dir); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestChecksumFor(t *testing.T) {
	h := strings.Repeat("ab", 32)
	sums := h + "  a.zip\n" + strings.Repeat("cd", 32) + " *b.zip\n" + h + "  prefix-a.zip\nshort  c.zip\n"
	if got := checksumFor(sums, "a.zip"); got != h {
		t.Fatalf("a.zip: %q", got)
	}
	if got := checksumFor(sums, "b.zip"); got != strings.Repeat("cd", 32) {
		t.Fatalf("binary mode entry: %q", got)
	}
	if got := checksumFor(sums, "zip"); got != "" {
		t.Fatalf("a suffix must not match: %q", got)
	}
	if got := checksumFor(sums, "c.zip"); got != "" {
		t.Fatalf("a short hash must not match: %q", got)
	}
}

type fixture struct {
	srv   *httptest.Server
	hits  atomic.Int32
	fails atomic.Int32 // the first N requests answer 500
	files map[string][]byte
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.fails.Add(-1) >= 0 {
			http.Error(w, "boom", 500)
			return
		}
		b, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func zipOf(t *testing.T, entries map[string]string) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for n, c := range entries {
		w, _ := z.Create(n)
		w.Write([]byte(c))
	}
	z.Close()
	return buf.Bytes()
}

func tgzOf(entries map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, c := range entries {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func (f *fixture) release(t *testing.T) {
	tofu := zipOf(t, map[string]string{"LICENSE": "x", "tofu": "#!/bin/sh\necho tofu\n"})
	f.files["/v9.9.9/tofu_9.9.9_linux_amd64.zip"] = tofu
	f.files["/v9.9.9/tofu_9.9.9_SHA256SUMS"] = []byte(sha(tofu) + "  tofu_9.9.9_linux_amd64.zip\n")
	conf := tgzOf(map[string]string{"conftest": "#!/bin/sh\necho conftest\n"})
	f.files["/c/v9.9.9/conftest_9.9.9_Linux_x86_64.tar.gz"] = conf
	f.files["/c/v9.9.9/checksums.txt"] = []byte(sha(conf) + "  conftest_9.9.9_Linux_x86_64.tar.gz\n")
}

func (f *fixture) run(t *testing.T, extra map[string]string) (int, string, string) {
	home := t.TempDir()
	ghPath := filepath.Join(home, "ghpath")
	vars := map[string]string{
		"GITHUB_PATH": ghPath, "TFPLS_TOOL_CACHE": filepath.Join(home, "cache"), "TFPLS_RETRY_DELAY": "0",
		"TFPLS_OS": "linux", "TFPLS_ARCH": "amd64", "TOFU_VERSION": "9.9.9", "CONFTEST_VERSION": "9.9.9",
		"TFPLS_TOFU_BASE_URL": f.srv.URL, "TFPLS_CONFTEST_BASE_URL": f.srv.URL + "/c",
	}
	for k, v := range extra {
		vars[k] = v
	}
	var out, errb bytes.Buffer
	rc := Run(nil, Env{Getenv: func(k string) string { return vars[k] }, Stdout: &out, Stderr: &errb, HTTP: f.srv.Client(), Sleep: func(time.Duration) {}})
	p, _ := os.ReadFile(ghPath)
	return rc, out.String(), string(p)
}

func TestInstallAndCache(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	rc, out, path := f.run(t, nil)
	if rc != 0 || !strings.Contains(out, "checksum verified") || strings.Count(path, "\n") != 2 {
		t.Fatalf("rc=%d out=%s path=%s", rc, out, path)
	}
}

func TestRetries(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	f.fails.Store(3)
	if rc, out, _ := f.run(t, nil); rc != 0 {
		t.Fatalf("3 transient errors must be survived: %s", out)
	}
	f.fails.Store(1000)
	rc, out, _ := f.run(t, nil)
	if rc == 0 || !strings.Contains(out, "download failed after 4 attempts") {
		t.Fatalf("rc=%d %s", rc, out)
	}
}

func TestNotFoundIsNotRetried(t *testing.T) {
	f := newFixture(t)
	f.hits.Store(0)
	rc, out, _ := f.run(t, nil)
	if rc == 0 || f.hits.Load() != 1 || !strings.Contains(out, "HTTP 404") {
		t.Fatalf("rc=%d hits=%d %s", rc, f.hits.Load(), out)
	}
}

func TestChecksumMismatch(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	f.files["/v9.9.9/tofu_9.9.9_linux_amd64.zip"] = append(f.files["/v9.9.9/tofu_9.9.9_linux_amd64.zip"], 'x')
	rc, out, path := f.run(t, nil)
	if rc == 0 || !strings.Contains(out, "checksum mismatch") || path != "" {
		t.Fatalf("rc=%d out=%s path=%q", rc, out, path)
	}
}

// the archive may hold other entries; only the named binary at the top level is taken, never a path from the archive
func TestArchiveEntries(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	evil := zipOf(t, map[string]string{"../../tofu": "evil", "sub/tofu": "evil"})
	f.files["/v9.9.9/tofu_9.9.9_linux_amd64.zip"] = evil
	f.files["/v9.9.9/tofu_9.9.9_SHA256SUMS"] = []byte(sha(evil) + "  tofu_9.9.9_linux_amd64.zip\n")
	rc, out, _ := f.run(t, nil)
	if rc == 0 || !strings.Contains(out, "tofu in tofu_9.9.9_linux_amd64.zip") {
		t.Fatalf("rc=%d %s", rc, out)
	}
}

// bug fixed: a version from the environment ended up in the URL and the cache path unchecked
func TestVersionIsValidated(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	for _, v := range []string{"../../etc", "1.2.3/../x", "1.2", "v1.2.3", "1.2.3 ", "1.2.3?x=1"} {
		rc, out, _ := f.run(t, map[string]string{"TOFU_VERSION": v})
		if rc == 0 || !strings.Contains(out, "not a valid version") {
			t.Fatalf("TOFU_VERSION=%q: rc=%d %s", v, rc, out)
		}
	}
	if rc, out, _ := f.run(t, map[string]string{"CONFTEST_VERSION": "../x"}); rc == 0 || !strings.Contains(out, "not a valid version") {
		t.Fatalf("conftest: rc=%d %s", rc, out)
	}
}

func TestDetectedVersionAndNotices(t *testing.T) {
	f := newFixture(t)
	f.release(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, ".opentofu-version"), "9.9.9\n")
	rc, out, _ := f.run(t, map[string]string{"TOFU_VERSION": "", "GITHUB_WORKSPACE": ws, "INSTALL_CONFTEST": "false"})
	if rc != 0 || !strings.Contains(out, "::notice::OpenTofu 9.9.9 (asked for by the repository)") || !strings.Contains(out, "conftest: skipped") {
		t.Fatalf("rc=%d %s", rc, out)
	}
	rc, out, _ = f.run(t, map[string]string{"TOFU_VERSION": "", "GITHUB_WORKSPACE": t.TempDir()})
	want := fmt.Sprintf("::notice::OpenTofu %s (default; add .opentofu-version to choose)", DefaultTofu)
	if !strings.Contains(out, want) {
		t.Fatalf("rc=%d %s", rc, out)
	}
}

func TestDetectSubcommand(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, ".tofu-version"), "v1.7.1\n")
	var out bytes.Buffer
	if rc := Run([]string{"detect", ws}, Env{Stdout: &out, Stderr: &out}); rc != 0 || out.String() != "1.7.1\n" {
		t.Fatalf("rc=%d %q", rc, out.String())
	}
	out.Reset()
	if rc := Run([]string{"detect", t.TempDir()}, Env{Stdout: &out, Stderr: &out}); rc != 0 || out.String() != "" {
		t.Fatalf("rc=%d %q", rc, out.String())
	}
	if rc := Run([]string{"bogus"}, Env{Stdout: &out, Stderr: &out}); rc != 2 {
		t.Fatalf("rc=%d", rc)
	}
}
