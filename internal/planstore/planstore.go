// Package planstore is the reviewed-plan store: pack and unpack a saved plan, name its artifact, find it and validate it.
// It is the Go version of actions/tofu/plan-store.sh and keeps its file format (openssl enc -aes-256-cbc -pbkdf2 -md sha256
// -salt, over "<64 hex sha256 of the tgz>\n<tgz>") and its error texts word for word. The key is read from the environment
// only and never appears on a command line.
package planstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Err carries the exit code the Bash function returned (1: refused or not found, 2: an API or environment failure).
type Err struct {
	Code int
	Msg  string
}

func (e *Err) Error() string { return e.Msg }

func fail(code int, format string, a ...any) error { return &Err{code, fmt.Sprintf(format, a...)} }

// Code returns the exit code of err (0 for nil).
func Code(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Err); ok {
		return e.Code
	}
	return 1
}

// Env is everything the store takes from outside: environment variables and the gh command.
type Env struct {
	Getenv func(string) string
	Gh     func(args ...string) ([]byte, error) // stdout of `gh args...`
}

func (e Env) key() string { return e.Getenv("PLAN_ENCRYPTION_KEY") }

// Slug turns a workspace name into a file-name-safe string.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ReplaceAll(s, "/", "__") {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// TargetsHash is the first 8 hex of the sha256 over the sorted, space-joined targets; "all" when there are none.
func TargetsHash(targets string) string {
	f := strings.Fields(targets)
	if len(f) == 0 {
		return "all"
	}
	sort.Strings(f)
	return sum([]byte(strings.Join(f, " ")))[:8]
}

func prefix(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// PlanName is the artifact name of the plan of a workspace, head commit and target set.
func PlanName(workspace, headSHA, targets string) string {
	return "tfpls-plan-" + Slug(workspace) + "-" + prefix(headSHA, 12) + "-" + TargetsHash(targets)
}

func deriveKey(pass string, salt []byte) (key, iv []byte, err error) {
	d, err := pbkdf2.Key(sha256.New, pass, salt, 10000, 48)
	if err != nil {
		return nil, nil, err
	}
	return d[:32], d[32:], nil
}

// seal is `openssl enc -aes-256-cbc -pbkdf2 -md sha256 -salt`.
func seal(plain []byte, pass string) ([]byte, error) {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, iv, err := deriveKey(pass, salt)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	buf := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(buf, buf)
	return append(append([]byte("Salted__"), salt...), buf...), nil
}

// open is `openssl enc -d ...`; ok is false for a bad header, a bad length or bad padding (what a wrong key mostly yields).
func open(data []byte, pass string) (plain []byte, ok bool) {
	if len(data) < 32 || !bytes.HasPrefix(data, []byte("Salted__")) || (len(data)-16)%aes.BlockSize != 0 {
		return nil, false
	}
	key, iv, err := deriveKey(pass, data[8:16])
	if err != nil {
		return nil, false
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	buf := append([]byte{}, data[16:]...)
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(buf, buf)
	n := int(buf[len(buf)-1])
	if n < 1 || n > aes.BlockSize || n > len(buf) {
		return nil, false
	}
	for _, c := range buf[len(buf)-n:] {
		if int(c) != n {
			return nil, false
		}
	}
	return buf[:len(buf)-n], true
}

func makeTgz(files map[string][]byte, order []string) ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for _, name := range order {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name]))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Pack stores <dir>/tfplan and the meta file as <outdir>/plan.tgz.enc (key set) or plan.tgz, and returns that path.
func Pack(env Env, dir, outdir, meta string) (string, error) {
	plan, err := os.ReadFile(filepath.Join(dir, "tfplan"))
	if err != nil {
		return "", fail(1, "no tfplan in %s", dir)
	}
	_ = os.MkdirAll(outdir, 0o755)
	m, err := os.ReadFile(meta)
	if err != nil {
		return "", fail(1, "tar failed")
	}
	tgz, err := makeTgz(map[string][]byte{"tfplan": plan, "meta.json": m}, []string{"tfplan", "meta.json"})
	if err != nil {
		return "", fail(1, "tar failed")
	}
	if pass := env.key(); pass != "" {
		enc, err := seal([]byte(sum(tgz)+"\n"+string(tgz)), pass)
		p := filepath.Join(outdir, "plan.tgz.enc")
		if err != nil || os.WriteFile(p, enc, 0o600) != nil {
			return "", fail(1, "encryption failed")
		}
		return p, nil
	}
	p := filepath.Join(outdir, "plan.tgz")
	if err := os.WriteFile(p, tgz, 0o600); err != nil {
		return "", fail(1, "tar failed")
	}
	return p, nil
}

// Unpack extracts tfplan and meta.json of a stored plan into outdir.
func Unpack(env Env, file, outdir string) error {
	_ = os.MkdirAll(outdir, 0o755)
	var tgz []byte
	switch {
	case strings.HasSuffix(file, ".enc"):
		pass := env.key()
		if pass == "" {
			return fail(1, "the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return fail(1, "could not decrypt the saved plan (wrong key or corrupted file)")
		}
		dec, ok := open(data, pass)
		if !ok {
			return fail(1, "could not decrypt the saved plan (wrong key or corrupted file)")
		}
		var want, rest []byte
		if len(dec) > 65 {
			want, rest = dec[:64], dec[65:]
		} else {
			want = dec
		}
		if subtle.ConstantTimeCompare(want, []byte(sum(rest))) != 1 {
			return fail(1, "the saved plan failed its integrity check (wrong key or tampered)")
		}
		tgz = rest
	case strings.HasSuffix(file, ".tgz"):
		if env.key() != "" {
			return fail(1, "the saved plan is NOT encrypted but PLAN_ENCRYPTION_KEY is set; refusing a possible downgrade (run /tofu plan again)")
		}
		tgz, _ = os.ReadFile(file)
	default:
		return fail(1, "unknown plan file type: %s", file)
	}
	files, err := readTgz(tgz)
	if err != nil {
		return fail(1, "the saved plan archive is invalid")
	}
	for _, n := range []string{"tfplan", "meta.json"} {
		if os.WriteFile(filepath.Join(outdir, n), files[n], 0o600) != nil {
			return fail(1, "the saved plan archive is invalid")
		}
	}
	return nil
}

// maxMember bounds one unpacked member (a saved plan is a few MB; this is far above any real one).
const maxMember = 1 << 30

func readTgz(tgz []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && (h.Name == "tfplan" || h.Name == "meta.json") {
			b, err := io.ReadAll(io.LimitReader(tr, maxMember+1)) // a decompression bomb must not fill the runner's memory
			if err != nil || len(b) > maxMember {
				return nil, fmt.Errorf("member too large or unreadable")
			}
			files[h.Name] = b
		}
	}
	for _, n := range []string{"tfplan", "meta.json"} {
		if _, ok := files[n]; !ok {
			return nil, fmt.Errorf("missing member %s", n)
		}
	}
	return files, nil
}

// metaField is the raw `.k // ""` for a top-level key of the meta file.
func metaField(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if !v {
			return ""
		}
	}
	b, _ := json.Marshal(m[k])
	return string(b)
}

// CheckMeta refuses a plan made for other code, another workspace, directory or tofu version.
func CheckMeta(metaFile, tree, name, dir, version string) error {
	var m map[string]any
	if b, err := os.ReadFile(metaFile); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if got := metaField(m, "tree"); got != tree {
		return fail(1, "the reviewed plan was made for different code (plan tree %s, current tree %s); run /tofu plan again", prefix(got, 12), prefix(tree, 12))
	}
	if got := metaField(m, "name"); got != name {
		return fail(1, "the reviewed plan is for workspace '%s', not '%s'", got, name)
	}
	if got := metaField(m, "dir"); got != dir {
		return fail(1, "the reviewed plan is for directory '%s', not '%s'", got, dir)
	}
	if got := metaField(m, "tofu_version"); got != version {
		return fail(1, "the reviewed plan was made with tofu %s, this run uses %s; run /tofu plan again", got, version)
	}
	return nil
}

var digits = regexp.MustCompile(`^[0-9]+$`)

type artifact struct {
	ID        any    `json:"id"`
	Expired   any    `json:"expired"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Run       *struct {
		ID         any    `json:"id"`
		Repo       any    `json:"repository_id"`
		HeadRepo   any    `json:"head_repository_id"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
	} `json:"workflow_run"`
}

// expired follows the truthiness of the former shell version: null and false are not expired.
func (a artifact) expired() bool {
	b, isBool := a.Expired.(bool)
	return a.Expired != nil && (!isBool || b)
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprint(v)
}

// Lookup finds the newest non-expired artifact of this repository's own trusted runs and returns "<artifact id> <run id>".
// An artifact counts only when its run belongs to this repository (repository id and head repository id both equal its id,
// so a fork cannot plant one) and ran on the default branch or on exactly HEAD_SHA (so its workflow files are the reviewed
// ones). The listing is paginated. Code 1: none (the message says whether it expired); code 2: an API call failed.
func Lookup(env Env, repo, name string) (string, error) {
	db := env.Getenv("DEFAULT_BRANCH")
	if db == "" {
		if b, err := env.Gh("api", "repos/"+repo, "--jq", ".default_branch"); err == nil {
			db = strings.TrimRight(string(b), "\n")
		}
	}
	if db == "" {
		return "", fail(2, "could not tell the default branch (needed to trust only reviewed plans)")
	}
	rid, sha := env.Getenv("GITHUB_REPOSITORY_ID"), env.Getenv("HEAD_SHA")
	if !digits.MatchString(rid) {
		rid = ""
		if b, err := env.Gh("api", "repos/"+repo, "--jq", ".id"); err == nil {
			rid = strings.TrimRight(string(b), "\n")
		}
	}
	if !digits.MatchString(rid) {
		return "", fail(2, "could not tell the repository id (needed to trust only this repository's own plans)")
	}
	listErr := fail(2, "could not list artifacts (does the workflow grant 'actions: read'?)")
	out, err := env.Gh("api", "--paginate", "repos/"+repo+"/actions/artifacts?name="+name+"&per_page=100")
	if err != nil {
		return "", listErr
	}
	var live, dead []artifact
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var page struct {
			Artifacts []artifact `json:"artifacts"`
		}
		if err := dec.Decode(&page); err == io.EOF {
			break
		} else if err != nil {
			return "", listErr
		}
		for _, a := range page.Artifacts {
			r := a.Run
			if r == nil || num(r.Repo) != rid || num(r.HeadRepo) != rid || !(r.HeadBranch == db || (sha != "" && r.HeadSHA == sha)) {
				continue
			}
			if a.expired() {
				dead = append(dead, a)
			} else {
				live = append(live, a)
			}
		}
	}
	if best := newest(live); best != nil {
		return num(best.ID) + " " + num(best.Run.ID), nil
	}
	if best := newest(dead); best != nil && best.ExpiresAt != "" {
		return "", fail(1, "the reviewed plan expired on %s (%s)", strings.SplitN(best.ExpiresAt, "T", 2)[0], name)
	}
	return "", fail(1, "no reviewed plan found (%s)", name)
}

// newest is the last of the greatest created_at (sort_by | reverse | .[0]).
func newest(as []artifact) *artifact {
	var best *artifact
	for i := range as {
		if best == nil || as[i].CreatedAt >= best.CreatedAt {
			best = &as[i]
		}
	}
	return best
}

// Fetch downloads the reviewed plan artifact into outdir and returns the plan file's path.
func Fetch(env Env, repo, name, outdir string) (string, error) {
	hit, err := Lookup(env, repo, name)
	if err != nil {
		return "", err
	}
	_ = os.MkdirAll(outdir, 0o755)
	if _, err := env.Gh("run", "download", hit[strings.Index(hit, " ")+1:], "--repo", repo, "-n", name, "-D", outdir); err != nil {
		return "", fail(2, "could not download the reviewed plan artifact (%s)", name)
	}
	for _, n := range []string{"plan.tgz.enc", "plan.tgz"} {
		if _, err := os.Stat(filepath.Join(outdir, n)); err == nil {
			return filepath.Join(outdir, n), nil
		}
	}
	return "", fail(2, "the artifact (%s) contains no plan file", name)
}

// Hint is one sentence telling the user how to fix a particular refusal.
func Hint(env Env, reason, label, targets string) string {
	cmd := "/tofu plan " + label
	if targets != "" {
		cmd += " -target=" + strings.ReplaceAll(targets, " ", " -target=")
	}
	has := func(parts ...string) bool { // the parts in this order, like a shell pattern *"a"*"b"*
		rest := reason
		for _, p := range parts {
			i := strings.Index(rest, p)
			if i < 0 {
				return false
			}
			rest = rest[i+len(p):]
		}
		return true
	}
	switch {
	case has("is encrypted but PLAN_ENCRYPTION_KEY is not set"):
		repo := env.Getenv("GITHUB_ACTION_REPOSITORY")
		if repo == "" {
			repo = "denzhel/tfpls"
		}
		return "This workflow does not receive the key. Pass the secret to the workflow that applies (`secrets: inherit` in the caller, or `PLAN_ENCRYPTION_KEY: ${{ secrets.PLAN_ENCRYPTION_KEY }}`); it must be the same key that saved the plan. See https://github.com/" + repo + "/blob/main/docs/reviewed-plans.md."
	case has("could not decrypt"), has("integrity check"):
		return "The key here differs from the one that saved the plan, or the file was altered. Make sure the plan and apply workflows receive the same PLAN_ENCRYPTION_KEY, or run `" + cmd + "` again to save a new plan."
	case has("is NOT encrypted but PLAN_ENCRYPTION_KEY is set"):
		return "The plan was saved without the key. Run `" + cmd + "` again so it is saved encrypted."
	case has("actions: read"):
		return "Grant `actions: read` to the caller workflow's permissions so the saved plan can be found and downloaded."
	case has("expired"):
		return "Saved plans are kept for 14 days. Run `" + cmd + "` again and review the new plan."
	case has("different code"):
		return "The branch changed or is behind its base since the plan. Update the branch, then run `" + cmd + "` again."
	case has("tofu", "this run uses"):
		return "The OpenTofu version changed since the plan was made. Run `" + cmd + "` again."
	}
	return "Run `" + cmd + "` (or push a commit so the PR plan runs) and review it, then apply."
}
