package onboard

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	remoteRe      = regexp.MustCompile(`^(https://([^@/]+@)?github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)$`)
	backendAnyRe  = regexp.MustCompile(`^[[:space:]]*backend[[:space:]]+"`)
	backendNameRe = regexp.MustCompile(`^[[:space:]]*backend[[:space:]]+"([A-Za-z0-9_-]+)"`)
	backendS3Re   = regexp.MustCompile(`^[[:space:]]*backend[[:space:]]+"s3"`)
	cloudRe       = regexp.MustCompile(`^[[:space:]]*cloud[[:space:]]*\{`)
	endpointsRe   = regexp.MustCompile(`^[[:space:]]*endpoints?[[:space:]]*=`)
	r2Re          = regexp.MustCompile(`(?i)(^|["'/ =])([a-z0-9-]+\.)*r2\.cloudflarestorage\.com([/:"']|$)`)
	regionRe      = regexp.MustCompile(`^[[:space:]]*region[[:space:]]*=`)
	lockfileRe    = regexp.MustCompile(`^[[:space:]]*use_lockfile[[:space:]]*=[[:space:]]*true`)
	dynamoRe      = regexp.MustCompile(`^[[:space:]]*dynamodb_table[[:space:]]*=`)
	profileRe     = regexp.MustCompile(`^[[:space:]]*profile[[:space:]]*=`)
	reqProvRe     = regexp.MustCompile(`^[[:space:]]*required_providers`)
	moduleRe      = regexp.MustCompile(`^[[:space:]]*module[[:space:]]+"`)
	sourceRe      = regexp.MustCompile(`.*source[[:space:]]*=[[:space:]]*"([^"]+)".*`)
	modSourceRe   = regexp.MustCompile(`^[[:space:]]*source[[:space:]]*=[[:space:]]*"([^"]+)".*`)
	localSourceRe = regexp.MustCompile(`^[[:space:]]*source[[:space:]]*=[[:space:]]*"(\.\.?/[^"]*)".*`)
	providerRe    = regexp.MustCompile(`^[[:space:]]*provider[[:space:]]+"([A-Za-z0-9_-]+)".*`)
	resourceRe    = regexp.MustCompile(`^[[:space:]]*(resource|data)[[:space:]]+"([a-z0-9]+)_[A-Za-z0-9_-]*".*`)
	reqExactRe    = regexp.MustCompile(`^[[:space:]]*required_version[[:space:]]*=[[:space:]]*"[[:space:]]*=?[[:space:]]*v?([0-9]+\.[0-9]+\.[0-9]+)[[:space:]]*".*$`)
	reqAnyRe      = regexp.MustCompile(`^[[:space:]]*required_version[[:space:]]*=[[:space:]]*"([^"]*)".*`)
	verFileRe     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	callerUsesRe  = regexp.MustCompile(`uses:[[:space:]]*["']?[A-Za-z0-9_.-]+/tfpls/\.github/workflows/`)
	tofuWordRe    = regexp.MustCompile(`(?i)tofu|terraform`)
	tofuApplyRe   = regexp.MustCompile(`(?i)(tofu|terraform)[^#]*apply`)
	utilityRe     = regexp.MustCompile(`^(terraform|random|null|local|tls|time|archive|external|http|template|cloudinit)$`)
	stacksRe      = regexp.MustCompile(`^stacks:`)
	pathCharsRe   = regexp.MustCompile(`[^A-Za-z0-9._/-]`)
)

func (s *state) abs(rel string) string { return filepath.Join(s.root, rel) }

func (s *state) git(args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.root
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err == nil
}

// globFiles lists the regular, non-hidden files of dir (relative to the root) that end in suffix, sorted.
func (s *state) globFiles(dir, suffix string) []string {
	ents, err := os.ReadDir(s.abs(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") || !strings.HasSuffix(n, suffix) {
			continue
		}
		if fi, err := os.Stat(s.abs(path.Join(dir, n))); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, path.Join(dir, n))
	}
	sort.Strings(out)
	return out
}

func (s *state) lines(rel string) []string {
	b, err := os.ReadFile(s.abs(rel))
	if err != nil {
		return nil
	}
	t := strings.TrimSuffix(string(b), "\n")
	if t == "" && len(b) == 0 {
		return nil
	}
	return strings.Split(t, "\n")
}

func anyLine(ls []string, re *regexp.Regexp) bool {
	for _, l := range ls {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// blocks: the lines of every block whose header matches pat (header to the closing brace), file by file.
func blocks(pat *regexp.Regexp, files [][]string) []string {
	var out []string
	for _, ls := range files {
		in, depth := false, 0
		for _, l := range ls {
			if !in && pat.MatchString(l) {
				in, depth = true, 0
			}
			if in {
				out = append(out, l)
				o, c := strings.Count(l, "{"), strings.Count(l, "}")
				depth += o - c
				if o+c > 0 && depth <= 0 {
					in = false
				}
			}
		}
	}
	return out
}

func (s *state) readAll(rels []string) [][]string {
	var out [][]string
	for _, r := range rels {
		out = append(out, s.lines(r))
	}
	return out
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func (s *state) scanGit() {
	s.branch, s.branchSrc = "", ""
	if _, ok := s.git("rev-parse", "--git-dir"); ok {
		s.inGit = true
		if b, ok := s.git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok {
			s.branch = strings.TrimPrefix(b, "origin/")
		}
		if s.branch != "" {
			s.branchSrc = "origin/HEAD"
		}
		// no origin/HEAD: the usual names first, because onboarding often runs on a feature branch and the push
		// trigger must name the DEFAULT branch
		if s.branch == "" {
			for _, c := range []string{"main", "master"} {
				_, a := s.git("show-ref", "--quiet", "refs/remotes/origin/"+c)
				_, b := s.git("show-ref", "--quiet", "refs/heads/"+c)
				if a || b {
					s.branch, s.branchSrc = c, "guessed"
					break
				}
			}
		}
		if s.branch == "" {
			s.branch, _ = s.git("symbolic-ref", "--quiet", "--short", "HEAD")
			s.branchSrc = "the current branch"
		}
		top, _ := s.git("rev-parse", "--show-toplevel")
		phys, err := filepath.EvalSymlinks(s.root)
		if err != nil {
			phys = s.root
		}
		if top != "" {
			if t, err := filepath.EvalSymlinks(top); err == nil {
				top = t
			}
			if top != phys {
				s.topOK = false
			}
		}
		// the repository on GitHub (for the trust policy), read from the remote with a strict pattern
		if rem, _ := s.git("remote", "get-url", "origin"); rem != "" {
			if m := remoteRe.FindStringSubmatch(rem); m != nil {
				s.owner, s.repo = m[3], strings.TrimSuffix(m[4], ".git")
				if !safe(s.repo) {
					s.owner, s.repo = "", ""
				}
			}
		}
	}
	if s.branch == "" || regexp.MustCompile(`[^A-Za-z0-9._/-]`).MatchString(s.branch) {
		s.branch, s.branchSrc = "main", "assumed"
	}
}

func (s *state) scanLayout() {
	s.layout = "none"
	rootTfs := s.globFiles(".", ".tf")
	s.rootTF = len(rootTfs) > 0
	if s.rootTF {
		for _, f := range rootTfs {
			if anyLine(s.lines(f), backendAnyRe) {
				s.rootBackend = true
				break
			}
		}
	}
	// 1: one folder per workspace under workspaces/
	if ents, err := os.ReadDir(s.abs("workspaces")); err == nil {
		for _, e := range ents {
			n := e.Name()
			if strings.HasPrefix(n, ".") {
				continue
			}
			if fi, err := os.Stat(s.abs("workspaces/" + n)); err != nil || !fi.IsDir() {
				continue
			}
			if safe(n) {
				s.found = append(s.found, n)
			} else {
				s.skipped = append(s.skipped, n)
			}
		}
		sort.Strings(s.found)
		sort.Strings(s.skipped)
	}
	// 2: one root module and one var file per workspace
	if len(s.found) == 0 && s.rootTF {
		for _, cand := range []string{"vars", "env", "envs", "environments", "tfvars", "."} {
			if fi, err := os.Stat(s.abs(cand)); err != nil || !fi.IsDir() {
				continue
			}
			for _, f := range s.globFiles(cand, ".tfvars") {
				n := strings.TrimSuffix(path.Base(f), ".tfvars")
				if n == "terraform" || strings.HasSuffix(n, ".auto") {
					continue
				}
				if safe(n) {
					s.varfiles = append(s.varfiles, n)
				}
			}
			if len(s.varfiles) > 0 {
				s.vardir = cand
				break
			}
		}
	}
	// 3: separate root modules (a backend block in each directory), the tofu workspace is `default`
	if len(s.found) == 0 && len(s.varfiles) == 0 {
		for _, d := range s.backendDirs() {
			if !safe(strings.ReplaceAll(d, "/", "-")) || contains(s.envdirs, d) {
				continue
			}
			s.envdirs = append(s.envdirs, d)
		}
	}
	// 4: one root stack
	switch {
	case len(s.found) > 0:
		s.layout = "folders"
	case len(s.varfiles) > 0:
		s.layout = "varfile"
	case len(s.envdirs) > 0:
		s.layout = "envs"
		s.rootStack = s.rootBackend
	case s.rootTF:
		s.layout, s.rootStack = "root", true
	}
	switch s.layout {
	case "folders", "varfile", "root":
		s.stackDirs = []string{"."}
	case "envs":
		if s.rootStack {
			s.stackDirs = []string{"."}
		}
		s.stackDirs = append(s.stackDirs, s.envdirs...)
	}
	name := s.repo
	if name == "" {
		phys, err := filepath.EvalSymlinks(s.root)
		if err != nil {
			phys = s.root
		}
		name = filepath.Base(phys)
	}
	name = regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(name, "-")
	name = strings.TrimLeft(name, ".")
	if !safe(name) {
		name = "infra"
	}
	s.stackName = name
}

// backendDirs: the directories (depth 1 to 3 below the root) holding a .tf file with a backend block, in the order of
// their file paths, without workspaces/, modules/ and .terraform/.
func (s *state) backendDirs() []string {
	var files []string
	filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(s.root, p)
		if rel == "." {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if rel == ".git" || depth >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth >= 2 && strings.HasSuffix(d.Name(), ".tf") && !strings.Contains("/"+filepath.ToSlash(rel), "/.terraform/") {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	var dirs []string
	for _, f := range files {
		if !anyLine(s.lines(f), backendAnyRe) {
			continue
		}
		d := path.Dir(f)
		if d == "." || strings.HasPrefix(d, "workspaces/") || strings.HasPrefix(d, "modules/") || strings.Contains(d, "/modules/") ||
			strings.HasPrefix(d, ".terraform/") || strings.Contains(d, "/.terraform/") {
			continue
		}
		dirs = append(dirs, d)
	}
	return dirs
}

func (s *state) listTF() []string {
	var out []string
	filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(s.root, p)
		if rel == "." {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == ".terraform" || n == "node_modules" || depth >= 6 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".tf") {
			out = append(out, "./"+filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}

func (s *state) scanTF() {
	s.tfAll = s.listTF()
	s.verHint = "not pinned: add a .opentofu-version file to choose"
	for _, d := range s.stackDirs {
		fs := s.globFiles(d, ".tf")
		if len(fs) == 0 {
			continue
		}
		all := s.readAll(fs)
		t := ""
	find:
		for _, ls := range all {
			for _, l := range ls {
				if m := backendNameRe.FindStringSubmatch(l); m != nil {
					t = m[1]
					break find
				}
			}
		}
		if t == "" {
			cloud := false
			for _, ls := range all {
				if anyLine(ls, cloudRe) {
					cloud = true
				}
			}
			if cloud {
				s.cloudBlock = true
			} else {
				s.beNone++
			}
			continue
		}
		if t != "s3" {
			s.beOther = t
			continue
		}
		blk := blocks(backendS3Re, all)
		if anyLine(blk, endpointsRe) {
			// an S3-compatible service; the endpoint is never printed (it can carry an account id)
			if anyLine(blk, r2Re) {
				s.s3r2++
			} else {
				s.s3other++
			}
		} else {
			s.s3aws++
			if !anyLine(blk, regionRe) {
				s.regionMiss++
			}
		}
		if !anyLine(blk, lockfileRe) {
			s.lockMissing++
		}
		if anyLine(blk, dynamoRe) {
			s.dynamo = true
		}
	}
	// the OpenTofu version, read as the pipeline reads it (internal/setup/detect.go): the repository root only
	for _, f := range []string{".opentofu-version", ".tofu-version", ".terraform-version"} {
		fi, err := os.Stat(s.abs(f))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		v := ""
		for _, l := range s.lines(f) {
			if i := strings.Index(l, "#"); i >= 0 {
				l = l[:i]
			}
			l = strings.Trim(l, " \t\r\n\v\f")
			if l != "" {
				v = l
				break
			}
		}
		v = strings.TrimPrefix(v, "v")
		if verFileRe.MatchString(v) {
			s.ver, s.verSrc = v, f
			break
		}
	}
	rootTfs := s.globFiles(".", ".tf")
	if s.ver == "" {
		for _, f := range rootTfs {
			for _, l := range s.lines(f) {
				if m := reqExactRe.FindStringSubmatch(l); m != nil {
					s.ver, s.verSrc = m[1], "required_version in "+path.Base(f)
					break
				}
			}
			if s.ver != "" {
				break
			}
		}
	}
	if s.ver == "" {
		rng := ""
	rg:
		for _, f := range rootTfs {
			for _, l := range s.lines(f) {
				if m := reqAnyRe.FindStringSubmatch(l); m != nil {
					rng = m[1]
					break rg
				}
			}
		}
		if rng != "" {
			s.verHint = "required_version is a range (" + rng + "), not pinned: add a .opentofu-version file to choose"
		}
		s.verDeep = s.pinnedBelow()
	}
	if len(s.tfAll) == 0 {
		return
	}
	rels := make([]string, len(s.tfAll))
	for i, f := range s.tfAll {
		rels[i] = strings.TrimPrefix(f, "./")
	}
	all := s.readAll(rels)
	for _, ls := range all {
		if anyLine(ls, profileRe) {
			s.profileFound = true
		}
	}
	// providers: required_providers sources, provider blocks and the prefixes of resource and data types
	set := map[string]bool{}
	for _, l := range blocks(reqProvRe, all) {
		if m := sourceRe.FindStringSubmatch(l); m != nil {
			set[m[1][strings.LastIndex(m[1], "/")+1:]] = true
		}
	}
	for _, ls := range all {
		for _, l := range ls {
			if m := providerRe.FindStringSubmatch(l); m != nil {
				set[m[1]] = true
			} else if m := resourceRe.FindStringSubmatch(l); m != nil {
				set[m[2]] = true
			}
		}
	}
	for p := range set {
		if p != "" && !utilityRe.MatchString(p) {
			s.providers = append(s.providers, p)
		}
	}
	sort.Strings(s.providers)
	// module relations: local modules of the stacks, and how many come from a registry or git
	for _, d := range s.stackDirs {
		for _, p := range s.moduleWatch(d) {
			if !contains(s.modsLocal, p) {
				s.modsLocal = append(s.modsLocal, p)
			}
		}
	}
	remote := map[string]bool{}
	for _, l := range blocks(moduleRe, all) {
		if m := modSourceRe.FindStringSubmatch(l); m != nil && !strings.HasPrefix(m[1], "./") && !strings.HasPrefix(m[1], "../") {
			remote[m[1]] = true
		}
	}
	s.modsRemote = len(remote)
}

// pinnedBelow: an exact required_version below the repository root (the first one, in file order).
func (s *state) pinnedBelow() string {
	for _, f := range s.tfAll {
		if strings.Count(f, "/") < 2 {
			continue
		}
		for _, l := range s.lines(strings.TrimPrefix(f, "./")) {
			if m := reqExactRe.FindStringSubmatch(l); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// moduleWatch: the relative module sources of one directory as repo-relative paths (never outside the repository).
func (s *state) moduleWatch(d string) []string {
	var out []string
	for _, f := range s.globFiles(d, ".tf") {
		for _, l := range s.lines(f) {
			m := localSourceRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			var parts []string
			okp := true
			for _, seg := range strings.Split(d+"/"+m[1], "/") {
				switch seg {
				case "", ".":
				case "..":
					if len(parts) == 0 {
						okp = false
					} else {
						parts = parts[:len(parts)-1]
					}
				default:
					parts = append(parts, seg)
				}
				if !okp {
					break
				}
			}
			if !okp || len(parts) == 0 {
				continue
			}
			p := strings.Join(parts, "/")
			if p == d || strings.HasPrefix(p, d+"/") || pathCharsRe.MatchString(p) || contains(out, p) {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

func (s *state) scanGithubFiles() {
	files := append(s.globFiles(".github/workflows", ".yml"), s.globFiles(".github/workflows", ".yaml")...)
	for _, f := range files {
		ls := s.lines(f)
		hasSlug := false
		for _, l := range ls {
			if strings.Contains(l, s.slug+"/.github/workflows/") {
				hasSlug = true
			}
		}
		switch {
		case hasSlug || anyLine(ls, callerUsesRe):
			s.callers = append(s.callers, f)
		case anyLine(ls, tofuWordRe):
			s.pipelines = append(s.pipelines, f)
			if anyLine(ls, tofuApplyRe) {
				s.appliers = append(s.appliers, f)
			}
		default:
			s.wfOthers++
		}
	}
	s.wf = ".github/workflows/tofu.yml"
	if _, err := os.Lstat(s.abs(s.wf)); err != nil && len(s.callers) > 0 {
		s.wf = s.callers[0]
	}
	for _, f := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		if fi, err := os.Stat(s.abs(f)); err == nil && fi.Mode().IsRegular() {
			s.codeowners = f
			break
		}
	}
	if _, err := os.Lstat(s.abs(".tfpls.yml")); err == nil {
		s.cfgExists = true
		s.cfgHasStacks = anyLine(s.lines(".tfpls.yml"), stacksRe)
	}
}
