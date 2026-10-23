// Package queue is a FIFO per workspace without cancelling anybody (GitHub's own concurrency groups keep one running and one
// pending run and silently cancel the rest). Every job takes a numbered ticket (a file on the data branch; the first push wins the
// number, so numbers are strictly ordered) and waits until no earlier ticket belongs to a run that is still active.
//
//	enter  take a ticket, then wait for the turn (prints the position, comments on the PR when PR is set)
//	leave  remove my ticket (run it with `if: always()`); a ticket whose run has finished is ignored by the others anyway, so a
//	       crashed or cancelled holder never blocks the queue.
package queue

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/datastore"
	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

// Env is everything Run touches outside the process.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     gh.Client
	Store  datastore.Store
	Now    func() time.Time
	Sleep  func(time.Duration)
	Pid    int
}

var (
	ticketRE = regexp.MustCompile(`^([0-9]+)-.*\.json$`)
	digitsRE = regexp.MustCompile(`^[0-9]+$`)
)

// Budget is the wait budget in minutes: never the whole job timeout (a job that waited its full budget would be killed mid-apply
// holding the lock).
func Budget(timeout string) int {
	t := 50
	if timeout != "" && digitsRE.MatchString(timeout) {
		if v, err := strconv.Atoi(timeout); err == nil {
			t = v
		}
	}
	switch {
	case t > 30:
		return t - 15
	case t >= 2:
		return t / 2
	}
	return t
}

// SafeName is the directory name of a queue: '/' and ' ' become '_', everything outside [A-Za-z0-9._-] is dropped. A result that
// is empty or only dots (a path component) gets a leading underscore.
func SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == ' ' {
			r = '_'
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if strings.Trim(s, ".") == "" {
		s = "_" + s
	}
	return s
}

type ticket struct {
	Run string `json:"run"`
	Job string `json:"job"`
	PR  string `json:"pr"`
	URL string `json:"url"`
}

type queue struct {
	e      Env
	r      *ghactions.Runner
	name   string
	label  string
	qdir   string
	state  string
	repo   string
	runID  string
	runURL string
	pr     string
}

// tickets lists the ticket files of the queue in order (the zero-padded number first: byte order = queue order).
func (q *queue) tickets(dir string) []string {
	ents, _ := os.ReadDir(filepath.Join(dir, q.qdir))
	var out []string
	for _, en := range ents {
		if en.Type().IsRegular() && ticketRE.MatchString(en.Name()) {
			out = append(out, en.Name())
		}
	}
	sort.Strings(out)
	return out
}

// runActive says whether workflow run id is still queued or running; when it cannot tell, it is assumed to be.
func (q *queue) runActive(id string) bool {
	if !digitsRE.MatchString(id) {
		return false // not a run id (a ticket someone wrote by hand): never put into a request path
	}
	st, err := q.e.GH.Run("api", "repos/"+q.repo+"/actions/runs/"+id, "--jq", ".status")
	if err != nil {
		return true
	}
	st = strings.TrimSpace(st)
	return st != "" && st != "completed"
}

// Run returns the exit code.
func Run(e Env) int {
	g := e.Getenv
	r := &ghactions.Runner{Getenv: g, Stdout: e.Stdout}
	mode, name := g("MODE"), g("NAME")
	if mode == "" || name == "" {
		fmt.Fprintln(e.Stderr, "queue: MODE and NAME are required")
		return 1
	}
	if ok, _ := datastore.ValidBranch(e.Store.Branch); !ok {
		if e.Store.Branch == "main" || e.Store.Branch == "master" {
			r.Warning("queue: refusing to use `" + e.Store.Branch + "`")
		} else {
			r.Warning("queue: invalid branch name")
		}
		return 0
	}
	safe := SafeName(name)
	label := g("LABEL")
	if label == "" {
		label = name
	}
	runID := g("RUN_ID")
	tmp := g("RUNNER_TEMP")
	if tmp == "" {
		tmp = "/tmp"
	}
	pr := g("PR")
	if pr != "" && !digitsRE.MatchString(pr) {
		pr = "" // used in a request path: only a number
	}
	q := &queue{e: e, r: r, name: name, label: label, qdir: "queue/" + safe, repo: g("REPO"), runID: runID, runURL: g("RUN_URL"), pr: pr,
		state: filepath.Join(tmp, "tfpls-queue-"+safe+"-"+orZero(runID))}
	switch mode {
	case "enter":
		return q.enter()
	case "leave":
		return q.leave()
	}
	fmt.Fprintf(e.Stderr, "unknown mode: %s\n", mode)
	return 2
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func (q *queue) enter() int {
	g := q.e.Getenv
	attempt := g("RUN_ATTEMPT")
	if attempt == "" {
		attempt = "1"
	}
	var my string
	werr := q.e.Store.Write("queue: "+q.name+" ticket", func(dir string) error {
		if err := os.MkdirAll(filepath.Join(dir, q.qdir), 0o755); err != nil {
			return err
		}
		next := 1
		for _, t := range q.tickets(dir) {
			if n, err := strconv.Atoi(ticketRE.FindStringSubmatch(t)[1]); err == nil && n >= next {
				next = n + 1
			}
		}
		my = fmt.Sprintf("%08d-%s-%s-%d.json", next, orZero(q.runID), attempt, q.e.Pid)
		b, _ := json.Marshal(ticket{Run: q.runID, Job: q.label, PR: g("PR"), URL: q.runURL})
		if err := os.WriteFile(filepath.Join(dir, q.qdir, my), append(b, '\n'), 0o644); err != nil {
			return err
		}
		readme := filepath.Join(dir, "README.md")
		if _, err := os.Stat(readme); err != nil {
			return os.WriteFile(readme, []byte("# tfpls data\n\nQueues (`queue/`), audit log and drift state written by tfpls.\n"), 0o644)
		}
		return nil
	})
	if werr != nil || my == "" {
		q.r.Warning("queue: could not take a ticket, continuing without waiting")
		return 0
	}
	if err := os.WriteFile(q.state, []byte(my+"\n"), 0o600); err != nil {
		q.r.Warning("queue: could not remember the ticket: " + err.Error())
	}
	fmt.Fprintf(q.e.Stdout, "queue: took ticket %s for %s\n", my, q.label)
	wait := Budget(g("TIMEOUT"))
	poll := 10 * time.Second
	if v, err := strconv.ParseFloat(g("TFPLS_POLL"), 64); err == nil && v >= 0 {
		poll = time.Duration(v * float64(time.Second))
	}
	deadline := q.e.Now().Add(time.Duration(wait) * time.Minute)
	announced := ""
	ahead, first := 0, ""
	for {
		var readErr error
		_, readErr = q.e.Store.Read(func(dir string) error {
			ahead, first = 0, ""
			for _, t := range q.tickets(dir) {
				if t == my {
					break
				}
				var tk ticket
				b, err := os.ReadFile(filepath.Join(dir, q.qdir, t))
				if err != nil || json.Unmarshal(b, &tk) != nil || tk.Run == "" || tk.Run == q.runID {
					continue
				}
				if q.runActive(tk.Run) {
					ahead++
					if first == "" {
						first = tk.URL
					}
				}
			}
			return nil
		})
		if readErr != nil {
			// The queue could not be read: do not take the turn on a guess (an empty answer would let this run jump the queue).
			fmt.Fprintf(q.e.Stdout, "queue: could not read the queue (%v), retrying\n", readErr)
			ahead = max(ahead, 1)
		} else if ahead == 0 {
			fmt.Fprintln(q.e.Stdout, "queue: it is our turn")
			q.deleteComment(announced)
			return 0
		} else {
			f := first
			if f == "" {
				f = "another run"
			}
			fmt.Fprintf(q.e.Stdout, "queue: position %d for %s (%d ahead, waiting for %s)\n", ahead+1, q.label, ahead, f)
			announced = q.announce(announced, ahead, first)
		}
		if !q.e.Now().Before(deadline) {
			q.r.Error(fmt.Sprintf("gave up waiting in the queue for `%s` after %d minutes (position %d)", q.label, wait, ahead+1))
			q.deleteComment(announced)
			return 1
		}
		q.e.Sleep(poll)
	}
}

// announce posts or updates the position comment on the pull request and returns its id.
func (q *queue) announce(id string, ahead int, first string) string {
	if q.pr == "" {
		return id
	}
	front := ""
	if first != "" {
		front = fmt.Sprintf("[the run in front](%s) is ", first)
	}
	body := fmt.Sprintf("⏳ `%s` is queued: position %d (%d ahead). It starts automatically when %sfinished. [This run](%s)", q.label, ahead+1, ahead, front, q.runURL)
	if id == "" {
		out, err := q.e.GH.Run("api", "repos/"+q.repo+"/issues/"+q.pr+"/comments", "-f", "body="+body, "--jq", ".id")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	q.e.GH.Run("api", "-X", "PATCH", "repos/"+q.repo+"/issues/comments/"+id, "-f", "body="+body)
	return id
}

func (q *queue) deleteComment(id string) {
	if id != "" && q.pr != "" && digitsRE.MatchString(id) {
		q.e.GH.Run("api", "-X", "DELETE", "repos/"+q.repo+"/issues/comments/"+id)
	}
}

func (q *queue) leave() int {
	b, _ := os.ReadFile(q.state)
	my := strings.TrimSpace(string(b))
	if my == "" || my != filepath.Base(my) || !ticketRE.MatchString(my) {
		fmt.Fprintln(q.e.Stdout, "queue: no ticket to give up")
		return 0
	}
	gone := true
	err := q.e.Store.Write("queue: "+q.name+" done", func(dir string) error {
		p := filepath.Join(dir, q.qdir, my)
		if _, err := os.Stat(p); err != nil {
			gone = true
			return nil
		}
		gone = false
		return os.Remove(p)
	})
	switch {
	case err != nil:
		q.r.Warning("queue: could not remove the ticket (later runs ignore it once this run has finished)")
	case gone:
		fmt.Fprintln(q.e.Stdout, "queue: ticket already gone")
		os.Remove(q.state)
	default:
		fmt.Fprintln(q.e.Stdout, "queue: ticket released")
		os.Remove(q.state)
	}
	return 0
}
