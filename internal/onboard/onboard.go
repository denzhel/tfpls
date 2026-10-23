// Package onboard is `tfpls onboard`: it looks at a repository, says what it understood and what it would change, asks
// (Enter takes the default), writes the caller workflow (and .tfpls.yml only when the layout needs one) and ends with a
// checklist. Safe to run again: an up-to-date file is left alone, a differing one is shown as a diff and replaced only on
// a yes or with --force, .tfpls.yml is never overwritten, no workflow is ever deleted. Offline unless the person says
// yes to the optional GitHub step.
package onboard

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TofuDefault is the default of tfpls setup (tests/onboard.test.sh keeps the two equal).
const TofuDefault = "1.13.2"

// Env is everything Run needs from the outside, so that it can be tested.
type Env struct {
	Getenv           func(string) string
	Stdout, Stderr   io.Writer
	StdoutIsTerminal bool
	Cwd              string
	// Input, when set, is the keyboard (tests); otherwise TFPLS_ONBOARD_TTY or /dev/tty is opened.
	Input           io.Reader
	InputIsTerminal bool
}

const usageText = `tfpls onboarding: looks at your repository, says what it understood, asks (Enter takes the default), then writes
.github/workflows/tofu.yml (and .tfpls.yml only when your layout needs one) and ends with a checklist of what is left.
  tfpls onboard [options] [dir]
  curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash -s -- [options]
Options:
  --yes              no questions: take every default (a differing workflow is kept, nothing goes online)
  --dry-run          show what would change (as diffs) and write nothing
  --force            replace a differing caller workflow without asking (.tfpls.yml is never overwritten)
  --no-jokes         no joke at the end
  --runs-on LABEL    runner label or JSON array of labels (default ubuntu-latest; ubuntu-slim is cheaper but cuts
                     every job after 15 minutes: only for workspaces that apply quickly)
  --ref REF          the tfpls version the caller uses (default v0)
  --slug OWNER/REPO  where tfpls lives (default denzhel/tfpls)
Safe to run again: a second run changes nothing. Offline, except an optional GitHub step (gh) that you say yes to.
NO_COLOR=1 switches the colours off.
`

var (
	refRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	slugRe   = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	labelRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	labelsRe = regexp.MustCompile(`^\[\s*"[A-Za-z0-9._-]+"(\s*,\s*"[A-Za-z0-9._-]+")*\s*\]$`)
	safeRe   = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)
	arnRe    = regexp.MustCompile(`^arn:aws[a-z-]*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`)
	digitsRe = regexp.MustCompile(`^[0-9]+$`)
)

func safe(s string) bool     { return safeRe.MatchString(s) }
func validARN(s string) bool { return arnRe.MatchString(s) }

type exitPanic struct{ code int }

type state struct {
	env  Env
	out  io.Writer
	errw io.Writer
	root string // the directory being onboarded (absolute)

	yes, dry, force, jokes bool
	ref, slug, runsOn      string
	dir                    string

	B, D, G, Y, RD, C, M, R string
	spin                    bool

	keys        *bufio.Reader
	keysIsTerm  bool
	interactive bool

	nwarn     int
	doneItems []string
	leftItems []string
	docs      string

	// git
	inGit             bool
	branch, branchSrc string
	owner, repo       string
	topOK             bool
	// layout
	found, skipped, varfiles, envdirs, stackDirs []string
	vardir                                       string
	layout                                       string
	rootStack, rootTF, rootBackend               bool
	stackName                                    string
	// terraform files
	tfAll                                                 []string
	s3aws, s3r2, s3other, beNone, lockMissing, regionMiss int
	beOther                                               string
	cloudBlock, dynamo, profileFound                      bool
	providers, modsLocal                                  []string
	modsRemote                                            int
	ver, verSrc, verHint, verDeep                         string
	// github files
	callers, pipelines, appliers []string
	wfOthers                     int
	wf, codeowners               string
	cfgExists, cfgHasStacks      bool
	// understanding
	awsProvider, needsAWS bool
	others, unknown       []string
	credsWord             string
	// online
	online                     bool
	ghRepoID, ghOwnerID, ghVis string
	stRole, stKey, stEnv       string
	cfgActive                  bool
	cfgWhat                    string
	wfAction, cfgAction        string
}

func (s *state) getenv(k string) string {
	if s.env.Getenv == nil {
		return os.Getenv(k)
	}
	return s.env.Getenv(k)
}

// Run is the whole of `tfpls onboard`; it returns the exit code.
func Run(args []string, env Env) (code int) {
	s := &state{env: env, out: env.Stdout, errw: env.Stderr}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitPanic); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	s.parseArgs(args)
	root := s.dir
	if !filepath.IsAbs(root) {
		root = filepath.Join(env.Cwd, root)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fmt.Fprintf(s.errw, "no such directory: %s\n", s.dir)
		return 1
	}
	s.root = root
	s.setupOutput()
	s.openKeyboard()
	s.topOK = true
	s.docs = "https://github.com/" + s.slug + "/blob/" + s.ref + "/docs"
	s.logo()
	s.scanGit()
	s.scanLayout()
	s.scanTF()
	s.scanGithubFiles()
	s.understand()
	s.report()
	s.onlineLookup()
	s.inShort()
	if !s.filesStep() {
		return 0
	}
	s.onlineSettings()
	s.checklist()
	s.joke()
	return 0
}

func (s *state) usageExit(code int, w io.Writer, msg string) {
	if msg != "" {
		fmt.Fprintln(w, msg)
	}
	fmt.Fprint(w, usageText)
	panic(exitPanic{code})
}

func (s *state) parseArgs(args []string) {
	s.jokes, s.ref, s.slug, s.runsOn = true, "v0", "denzhel/tfpls", "ubuntu-latest"
	i := 0
loop:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--yes":
			s.yes = true
			i++
		case a == "--dry-run":
			s.dry = true
			i++
		case a == "--force":
			s.force = true
			i++
		case a == "--no-jokes":
			s.jokes = false
			i++
		case a == "--ref" || a == "--slug" || a == "--runs-on":
			if i+1 >= len(args) || args[i+1] == "" {
				s.usageExit(2, s.errw, a+" needs a value")
			}
			switch a {
			case "--ref":
				s.ref = args[i+1]
			case "--slug":
				s.slug = args[i+1]
			default:
				s.runsOn = args[i+1]
			}
			i += 2
		case a == "-h" || a == "--help":
			fmt.Fprint(s.out, usageText)
			panic(exitPanic{0})
		case a == "--":
			i++
			break loop
		case strings.HasPrefix(a, "-"):
			s.usageExit(2, s.errw, "unknown option: "+a)
		default:
			break loop
		}
	}
	rest := args[i:]
	if len(rest) > 1 {
		fmt.Fprintln(s.errw, "one directory at most")
		panic(exitPanic{2})
	}
	s.dir = "."
	if len(rest) == 1 {
		s.dir = rest[0]
	}
	if !refRe.MatchString(s.ref) {
		fmt.Fprintln(s.errw, "--ref: letters, digits and . _ / - only")
		panic(exitPanic{2})
	}
	if !slugRe.MatchString(s.slug) {
		fmt.Fprintln(s.errw, "--slug: OWNER/REPO")
		panic(exitPanic{2})
	}
	if !labelRe.MatchString(s.runsOn) && !labelsRe.MatchString(s.runsOn) {
		fmt.Fprintln(s.errw, `--runs-on: a runner label, or a JSON array of labels such as '["self-hosted","linux"]'`)
		panic(exitPanic{2})
	}
}

func (s *state) setupOutput() {
	tty := s.env.StdoutIsTerminal && s.getenv("TERM") != "dumb"
	if s.getenv("NO_COLOR") == "" && (s.getenv("FORCE_COLOR") == "1" || tty) {
		s.B, s.D, s.G, s.Y, s.RD, s.C, s.M, s.R = "\033[1m", "\033[2m", "\033[32m", "\033[33m", "\033[31m", "\033[36m", "\033[35m", "\033[0m"
	}
	s.spin = tty // a spinner only on a real terminal, never in a log
}

// openKeyboard: questions only with a person at a terminal (stdout is a terminal and /dev/tty can be opened, also under
// curl | bash), or from the test hook TFPLS_ONBOARD_TTY.
func (s *state) openKeyboard() {
	if s.yes || s.dry {
		return
	}
	if s.env.Input != nil {
		s.keys, s.keysIsTerm, s.interactive = bufio.NewReader(s.env.Input), s.env.InputIsTerminal, true
		return
	}
	var f *os.File
	var err error
	if hook := s.getenv("TFPLS_ONBOARD_TTY"); hook != "" {
		f, err = os.Open(hook)
	} else if s.env.StdoutIsTerminal {
		f, err = os.Open("/dev/tty")
	} else {
		return
	}
	if err != nil {
		return
	}
	s.keys, s.keysIsTerm, s.interactive = bufio.NewReader(f), isTerminal(f), true
}

// ---- output ----------------------------------------------------------------------------------------------------------

func (s *state) p(format string, a ...any) { fmt.Fprintf(s.out, format, a...) }
func (s *state) ok(m string)               { s.p("  %s✔%s %s\n", s.G, s.R, m) }
func (s *state) warn(m string)             { s.p("  %s!%s %s\n", s.Y, s.R, m); s.nwarn++ }
func (s *state) note(m string)             { s.p("  %s•%s %s\n", s.C, s.R, m) }
func (s *state) section(m string)          { s.p("\n%s%s%s\n", s.B, m, s.R) }
func (s *state) row(label, v string)       { s.p("  %s%-11s%s %s\n", s.D, label, s.R, v) }
func (s *state) die(m string) {
	fmt.Fprintf(s.errw, "%s✘ %s%s\n", s.RD, m, s.R)
	panic(exitPanic{1})
}
func (s *state) done(m string) { s.doneItems = append(s.doneItems, m) }
func (s *state) left(m string) { s.leftItems = append(s.leftItems, m) }

func (s *state) logo() {
	s.p("\n%s     ________%s\n", s.Y, s.R)
	s.p("%s    /_______/|%s   %s%stfpls%s onboarding\n", s.Y, s.R, s.B, s.M, s.R)
	s.p("%s    | o   o ||%s   OpenTofu on GitHub Actions\n", s.Y, s.R)
	s.p("%s    |  \\_/  ||%s   %splan on pull requests, apply by comment, drift checks%s\n", s.Y, s.R, s.D, s.R)
	s.p("%s    |_______|/%s\n", s.Y, s.R)
}

func (s *state) joke() {
	if !s.jokes {
		return
	}
	s.p("\n  %s💬 %s%s\n\n", s.D, jokes[pickJoke(s.getenv("TFPLS_JOKE_SEED"))], s.R)
}

// ---- questions -------------------------------------------------------------------------------------------------------

// readReply reads one line from the keyboard; false at the end of the input.
func (s *state) readReply() (string, bool) {
	line, err := s.keys.ReadString('\n')
	if err != nil && line == "" {
		s.p("\n")
		return "", false
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	line = strings.Trim(line, " \t\r\n\v\f")
	if !s.keysIsTerm {
		s.p("%s\n", line) // answers that do not come from a terminal are echoed: the transcript reads well
	}
	return line, true
}

// ask asks a yes/no question: true = yes. Without a terminal, with --yes or --dry-run nothing is asked: the default counts.
func (s *state) ask(q string, def bool) bool {
	if !s.interactive {
		return def
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for tries := 0; tries < 3; tries++ {
		s.p("  %s?%s %s %s%s%s ", s.M, s.R, q, s.D, hint, s.R)
		r, ok := s.readReply()
		if !ok || r == "" {
			return def
		}
		switch strings.ToLower(r) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		s.note("please answer y or n (Enter takes the default)")
	}
	return def
}

// askText returns the answer (the default without a terminal or on Enter); an answer must pass check.
func (s *state) askText(q, def string, check func(string) bool) string {
	if !s.interactive {
		return def
	}
	shown := "[" + def + "]"
	if def == "" {
		shown = "(Enter skips)"
	}
	for tries := 0; tries < 3; tries++ {
		s.p("  %s?%s %s %s%s%s ", s.M, s.R, q, s.D, shown, s.R)
		r, ok := s.readReply()
		if !ok || r == "" {
			return def
		}
		if check(r) {
			return r
		}
		s.note("that does not look right, try again (Enter takes the default)")
	}
	return def
}

// quietly runs a command with no input; its output is returned. A spinner turns on a terminal.
func (s *state) quietly(label string, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = s.root
	cmd.Stdin = stdin
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if !s.spin {
		err = cmd.Run()
		return o.String(), e.String(), err
	}
	if err = cmd.Start(); err == nil {
		fin := make(chan error, 1)
		go func() { fin <- cmd.Wait() }()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		tk := time.NewTicker(100 * time.Millisecond)
		for i := 0; ; i++ {
			select {
			case err = <-fin:
				tk.Stop()
				s.p("\r\033[K")
				return o.String(), e.String(), err
			case <-tk.C:
				s.p("\r  %s%s%s %s", s.C, frames[i%10], s.R, label)
			}
		}
	}
	return o.String(), e.String(), err
}

func genKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b) + "\n", nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool { return isTerminal(f) }
