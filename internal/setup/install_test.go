package setup

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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// `tfpls setup` against a local HTTP server that can fail on demand: fake release assets laid out like the GitHub
// release URLs, with real checksum files. No real network. (These were the checks of tests/install.test.sh, which
// built the fixtures and the server with Python.)

const (
	fakeTofu = "#!/bin/sh\necho 'OpenTofu v9.9.9 (fake)'\n"
	fakeConf = "#!/bin/sh\necho 'Conftest: 9.9.9 (fake)'\n"
)

func sum(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeRelease(t *testing.T) string {
	root := t.TempDir()
	// OpenTofu: zip archives and tofu_9.9.9_SHA256SUMS
	var sums []string
	for _, p := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		name := fmt.Sprintf("tofu_9.9.9_%s_%s.zip", p[0], p[1])
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: "tofu", Method: zip.Deflate}
		h.SetMode(0o755)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(fakeTofu))
		zw.Close()
		mustWrite(t, filepath.Join(root, "v9.9.9", name), buf.Bytes())
		sums = append(sums, sum(buf.Bytes())+"  "+name)
	}
	sort.Strings(sums)
	mustWrite(t, filepath.Join(root, "v9.9.9", "tofu_9.9.9_SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"))
	// conftest: tar.gz archives and checksums.txt
	var lines []string
	for _, p := range [][2]string{{"Linux", "x86_64"}, {"Linux", "arm64"}, {"Darwin", "x86_64"}, {"Darwin", "arm64"}} {
		name := fmt.Sprintf("conftest_9.9.9_%s_%s.tar.gz", p[0], p[1])
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "conftest", Size: int64(len(fakeConf)), Mode: 0o755})
		tw.Write([]byte(fakeConf))
		tw.Close()
		gz.Close()
		mustWrite(t, filepath.Join(root, "c", "v9.9.9", name), buf.Bytes())
		lines = append(lines, sum(buf.Bytes())+"  "+name)
	}
	mustWrite(t, filepath.Join(root, "c", "v9.9.9", "checksums.txt"), []byte(strings.Join(lines, "\n")+"\n"))
	return root
}

type server struct {
	srv  *httptest.Server
	mu   sync.Mutex
	left int
	reqs []string
}

// startServer serves root; the first fail requests get HTTP 500; every request is logged.
func startServer(t *testing.T, root string, fail int) *server {
	s := &server{left: fail}
	files := http.FileServer(http.Dir(root))
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.URL.Path)
		if s.left > 0 {
			s.left--
			s.mu.Unlock()
			w.WriteHeader(500)
			return
		}
		s.mu.Unlock()
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.reqs) }

func (s *server) conftestRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.reqs {
		if strings.HasPrefix(p, "/c/") {
			n++
		}
	}
	return n
}

type install struct {
	cache, ghpath string
	srv           *server
}

func (i *install) run(t *testing.T, kv ...string) (int, string) {
	t.Helper()
	env := map[string]string{
		"HOME": t.TempDir(), "GITHUB_PATH": i.ghpath, "TFPLS_TOOL_CACHE": i.cache, "TFPLS_RETRY_DELAY": "0",
		"TFPLS_OS": "linux", "TFPLS_ARCH": "amd64", "TOFU_VERSION": "9.9.9", "CONFTEST_VERSION": "9.9.9",
		"TFPLS_TOFU_BASE_URL": i.srv.srv.URL, "TFPLS_CONFTEST_BASE_URL": i.srv.srv.URL + "/c",
	}
	for _, a := range kv {
		k, v, _ := strings.Cut(a, "=")
		env[k] = v
	}
	var out bytes.Buffer
	rc := Run(nil, Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, Stderr: &out, HTTP: &http.Client{Timeout: 20 * time.Second}, Sleep: func(time.Duration) {}})
	return rc, out.String()
}

func newInstall(t *testing.T, srv *server) *install {
	dir := t.TempDir()
	i := &install{cache: filepath.Join(dir, "cache"), ghpath: filepath.Join(dir, "ghpath"), srv: srv}
	mustWrite(t, i.ghpath, nil)
	return i
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&0o111 != 0
}

func TestInstallCleanAndCached(t *testing.T) {
	root := makeRelease(t)
	srv := startServer(t, root, 0)
	i := newInstall(t, srv)
	rc, out := i.run(t)
	if rc != 0 {
		t.Fatalf("clean install (rc=%d): %s", rc, out)
	}
	tofu := filepath.Join(i.cache, "opentofu/9.9.9/linux-amd64/tofu")
	conf := filepath.Join(i.cache, "conftest/9.9.9/linux-amd64/conftest")
	if !executable(tofu) || !executable(conf) {
		t.Error("both binaries must be installed and executable")
	}
	got, _ := exec.Command(tofu).Output()
	if strings.TrimSpace(string(got)) != "OpenTofu v9.9.9 (fake)" {
		t.Errorf("installed tofu is not the one from the archive: %q", got)
	}
	gp, _ := os.ReadFile(i.ghpath)
	if !strings.Contains(string(gp), "opentofu/9.9.9/linux-amd64") || !strings.Contains(string(gp), "conftest/9.9.9/linux-amd64") {
		t.Errorf("directories are not added to GITHUB_PATH: %q", gp)
	}
	if !strings.Contains(out, "checksum verified") {
		t.Errorf("checksum verification is not reported: %s", out)
	}
	// a second run: cache hit, no network
	before := srv.count()
	if rc, out = i.run(t); rc != 0 || srv.count() != before {
		t.Errorf("cache hit (rc=%d, requests %d -> %d): %s", rc, before, srv.count(), out)
	}
	// no policy configured: conftest is not downloaded
	i = newInstall(t, startServer(t, root, 0))
	rc, _ = i.run(t, "INSTALL_CONFTEST=false")
	if rc != 0 || !executable(filepath.Join(i.cache, "opentofu/9.9.9/linux-amd64/tofu")) || exists(filepath.Join(i.cache, "conftest")) || i.srv.conftestRequests() != 0 {
		t.Error("INSTALL_CONFTEST=false must install only tofu and make no conftest requests")
	}
}

func TestInstallTransientAndPermanentFailures(t *testing.T) {
	root := makeRelease(t)
	i := newInstall(t, startServer(t, root, 3))
	rc, out := i.run(t)
	if rc != 0 || !executable(filepath.Join(i.cache, "opentofu/9.9.9/linux-amd64/tofu")) {
		t.Errorf("must survive 3 transient server errors (rc=%d): %s", rc, out)
	}
	i = newInstall(t, startServer(t, root, 100000))
	rc, out = i.run(t)
	if rc == 0 {
		t.Error("a permanent failure must end in an error")
	}
	if !strings.Contains(out, "download failed after") {
		t.Errorf("the error does not name the failed download: %s", out)
	}
	if exists(filepath.Join(i.cache, "opentofu")) {
		t.Error("nothing may be installed after a failed download")
	}
}

func TestInstallRefusesTamperedArchive(t *testing.T) {
	root := makeRelease(t)
	zipPath := filepath.Join(root, "v9.9.9", "tofu_9.9.9_linux_amd64.zip")
	b, _ := os.ReadFile(zipPath)
	mustWrite(t, zipPath, append(b, []byte("tampered\n")...))
	i := newInstall(t, startServer(t, root, 0))
	rc, out := i.run(t)
	if rc == 0 {
		t.Error("a tampered archive must be refused")
	}
	if !strings.Contains(out, "checksum mismatch") {
		t.Errorf("a checksum mismatch must be reported: %s", out)
	}
	if exists(filepath.Join(i.cache, "opentofu")) {
		t.Error("a tampered archive must not be installed")
	}
}

func TestInstallRefusesMissingChecksumEntry(t *testing.T) {
	root := makeRelease(t)
	p := filepath.Join(root, "v9.9.9", "tofu_9.9.9_SHA256SUMS")
	b, _ := os.ReadFile(p)
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.Contains(l, "linux_amd64") {
			keep = append(keep, l)
		}
	}
	mustWrite(t, p, []byte(strings.Join(keep, "\n")+"\n"))
	i := newInstall(t, startServer(t, root, 0))
	if rc, out := i.run(t); rc == 0 {
		t.Errorf("a missing checksum entry must be refused: %s", out)
	}
	if exists(filepath.Join(i.cache, "opentofu")) {
		t.Error("nothing may be installed without a checksum entry")
	}
}

func TestInstallOtherPlatforms(t *testing.T) {
	root := makeRelease(t)
	for _, p := range [][2]string{{"darwin", "arm64"}, {"linux", "arm64"}, {"darwin", "amd64"}} {
		i := newInstall(t, startServer(t, root, 0))
		rc, out := i.run(t, "TFPLS_OS="+p[0], "TFPLS_ARCH="+p[1])
		if rc != 0 || !executable(filepath.Join(i.cache, "opentofu/9.9.9", p[0]+"-"+p[1], "tofu")) {
			t.Errorf("install for %s/%s (rc=%d): %s", p[0], p[1], rc, out)
		}
	}
	i := newInstall(t, startServer(t, root, 0))
	if rc, _ := i.run(t, "TFPLS_OS=freebsd"); rc == 0 {
		t.Error("an unsupported OS must be rejected")
	}
}
