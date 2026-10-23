package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var buildTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// Build builds the release assets: args are <tag> <outdir>.
//
//	<outdir>/tfpls_<os>_<arch>.tar.gz   os linux|darwin, arch amd64|arm64; the tarball holds the single file "tfpls"
//	<outdir>/SHA256SUMS                 sha256 of the tarballs, "<hash>  <name>" (check with: sha256sum -c SHA256SUMS)
//	<outdir>/RELEASE_NOTES.md           the CHANGELOG.md section of the tag, else a one-line note (not a release asset)
//
// The names do not contain the version, so releases/latest/download/<name> works. TFPLS_CHANGELOG overrides the
// changelog path (tests). It returns the exit code.
func Build(args []string, env Env, getenv func(string) string) int {
	if len(args) != 2 {
		fmt.Fprintln(env.Stderr, "usage: release build <tag, e.g. v0.5.0 or v0.5.0-rc.1> <outdir>")
		return 2
	}
	tag, out := args[0], args[1]
	if !buildTag.MatchString(tag) {
		fmt.Fprintf(env.Stderr, "build-release: '%s' is not a version tag (vX.Y.Z or vX.Y.Z-rc.N)\n", tag)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "build-release: "+format+"\n", a...)
		return 1
	}
	root, err := runner{env}.git(env.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fail("not in a git repository: %v", err)
	}
	changelog := getenv("TFPLS_CHANGELOG")
	if changelog == "" {
		changelog = filepath.Join(root, "CHANGELOG.md")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fail("%v", err)
	}
	if out, err = filepath.Abs(out); err != nil {
		return fail("%v", err)
	}
	work, err := os.MkdirTemp("", "tfpls-build-")
	if err != nil {
		return fail("%v", err)
	}
	defer os.RemoveAll(work)

	// darwin before linux: the order of a sorted listing of the tarballs, as SHA256SUMS always had it
	var sums strings.Builder
	for _, goos := range []string{"darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := fmt.Sprintf("tfpls_%s_%s", goos, arch)
			bin := filepath.Join(work, name)
			cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+tag, "-o", bin, "./cmd/tfpls")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+arch)
			if b, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(env.Stderr, "%s", b)
				return fail("build of %s failed", name)
			}
			data, err := os.ReadFile(bin)
			if err != nil {
				return fail("%v", err)
			}
			tgz, err := tarball(data)
			if err != nil {
				return fail("%v", err)
			}
			if err := os.WriteFile(filepath.Join(out, name+".tar.gz"), tgz, 0o644); err != nil {
				return fail("%v", err)
			}
			fmt.Fprintf(&sums, "%x  %s.tar.gz\n", sha256.Sum256(tgz), name)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return fail("%v", err)
	}

	notes := ""
	if b, err := os.ReadFile(changelog); err == nil {
		notes = Section(string(b), tag)
	}
	if notes == "" {
		notes = fmt.Sprintf("tfpls %s (no CHANGELOG section for this version).", tag)
	}
	if err := os.WriteFile(filepath.Join(out, "RELEASE_NOTES.md"), []byte(notes+"\n"), 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(env.Stdout, "build-release: %s built into %s\n", tag, out)
	return 0
}

// tarball packs data as the single executable file "tfpls" into a reproducible tar.gz (no times, owners or gzip name).
func tarball(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "tfpls", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Section returns the lines of the CHANGELOG section "## <tag>" up to the next "## ", without blank lines at the ends.
func Section(changelog, tag string) string {
	var lines []string
	on := false
	for _, l := range strings.Split(changelog, "\n") {
		l = strings.TrimSuffix(l, "\r")
		switch {
		case l == "## "+tag:
			on = true
		case strings.HasPrefix(l, "## "):
			on = false
		case on:
			lines = append(lines, l)
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}
