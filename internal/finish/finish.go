// Package finish is the end of a job that ran tofu: pick the notification webhook, write the audit event. Neither ever fails the job.
package finish

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/denzhel/tfpls/internal/datastore"
	"github.com/denzhel/tfpls/internal/record"
)

// Env is injected for tests.
type Env struct {
	Getenv    func(string) string
	Stdout    io.Writer
	SetOutput func(name, value string) error
	Now       func() time.Time
}

const wsChars = " \t\n\r\f\v"

func trim(s string) string { return strings.Trim(s, wsChars) }

func isURL(s string) bool {
	rest := ""
	switch {
	case strings.HasPrefix(s, "https://"):
		rest = s[len("https://"):]
	case strings.HasPrefix(s, "http://"):
		rest = s[len("http://"):]
	default:
		return false
	}
	return rest != "" && strings.IndexFunc(rest, unicode.IsSpace) < 0
}

func sanitizeKey(k string) string {
	var b strings.Builder
	for _, r := range k {
		if r < 128 && (r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Pick chooses the webhook: the URL ("" = nothing is sent) and the note for the log. KEY is the workspace's notify_secret, a key of HOOKS
// (the secret TFPLS_NOTIFY_WEBHOOKS, a JSON object); empty or SLACK_WEBHOOK is DEFAULT_HOOK, else HOOKS.SLACK_WEBHOOK.
func Pick(key, defaultHook, hooks string) (url, note string) {
	k := trim(key)
	if k == "" {
		k = "SLACK_WEBHOOK"
	}
	show := sanitizeKey(k)
	d, h := trim(defaultHook), trim(hooks)
	var obj map[string]any
	isObj := true
	if h != "" {
		var v any
		if err := json.Unmarshal([]byte(h), &v); err != nil {
			isObj = false
		} else if m, ok := v.(map[string]any); ok && m != nil {
			obj = m
		} else {
			isObj = false
		}
	}
	switch {
	case k == "SLACK_WEBHOOK" && d != "":
		if isURL(d) {
			return d, "notification webhook: the secret SLACK_WEBHOOK"
		}
		return "", "::warning::the secret SLACK_WEBHOOK is not an http(s) URL; no notification is sent"
	case !isObj:
		return "", "::warning::the secret TFPLS_NOTIFY_WEBHOOKS is not a JSON object of webhook URLs ({\"KEY\": \"https://...\"}); no notification is sent for notify_secret `" + show + "`"
	}
	if v, ok := obj[k]; ok {
		if s, isStr := v.(string); isStr && isURL(trim(s)) {
			return trim(s), "notification webhook: key `" + show + "` of the secret TFPLS_NOTIFY_WEBHOOKS"
		}
		return "", "::warning::the value of `" + show + "` in the secret TFPLS_NOTIFY_WEBHOOKS is not an http(s) URL; no notification is sent"
	}
	switch {
	case k == "SLACK_WEBHOOK":
		return "", "no notification webhook: the secret SLACK_WEBHOOK is not set"
	case h == "":
		return "", "::warning::notify_secret `" + show + "` names a key of the secret TFPLS_NOTIFY_WEBHOOKS, which is not set or not passed on (secrets: inherit); no notification is sent (docs/notifications.md)"
	}
	return "", "::warning::notify_secret `" + show + "` is not a key of the secret TFPLS_NOTIFY_WEBHOOKS; no notification is sent (docs/notifications.md)"
}

// Webhook implements `tfpls finish webhook`: env KEY, DEFAULT_HOOK, HOOKS; writes the step output url. Masked first: GitHub masks a secret
// as a whole and line by line, not a value inside a JSON secret, and the next step shows its inputs in the log.
func Webhook(e Env) int {
	url, note := Pick(e.Getenv("KEY"), e.Getenv("DEFAULT_HOOK"), e.Getenv("HOOKS"))
	if url != "" {
		fmt.Fprintf(e.Stdout, "::add-mask::%s\n", strings.ReplaceAll(url, "%", "%25"))
	}
	fmt.Fprintln(e.Stdout, note)
	if err := e.SetOutput("url", url); err != nil {
		fmt.Fprintf(e.Stdout, "::warning::could not write the step output: %v\n", err)
	}
	return 0
}

// Store implements `tfpls finish store`: write the audit event (AUDIT=true) to the data branch.
func Store(e Env) int {
	if e.Getenv("AUDIT") != "true" {
		return 0
	}
	st := datastore.FromEnv(e.Getenv)
	if ok, why := datastore.ValidBranch(st.Branch); !ok {
		fmt.Fprintf(e.Stdout, "::warning::audit log: %s\n", why)
		return 0
	}
	now := e.Getenv("TFPLS_NOW")
	if now == "" {
		now = e.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	line, err := record.Build(e.Getenv, now)
	if err != nil {
		fmt.Fprintf(e.Stdout, "::warning::audit log: %v\n", err)
		return 0
	}
	file := record.File(now)
	msg := "audit: " + e.Getenv("KIND") + " " + e.Getenv("WORKSPACE") + " " + e.Getenv("STATUS")
	if err := st.Write(msg, func(d string) error { return record.Write(d, file, line) }); err != nil {
		fmt.Fprintf(e.Stdout, "::warning::could not write the audit event to %s\n", st.Branch)
		return 0
	}
	fmt.Fprintf(e.Stdout, "data branch %s updated\n", st.Branch)
	return 0
}
