package tofu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

type prComment struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	User    *struct {
		Type string `json:"type"`
	} `json:"user"`
}

func (c prComment) isBot() bool { return c.User != nil && c.User.Type == "Bot" }

// listComments reads every comment of the pull request (paginated, 100 per page).
func listComments(c gh.Client, repo, pr string) ([]prComment, error) {
	var all []prComment
	err := c.Pages(func(dec *json.Decoder) error {
		var page []prComment
		if err := dec.Decode(&page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	}, fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repo, pr))
	return all, err
}

var hintRe = regexp.MustCompile("\n+`/tofu apply [^`\n]+` to apply\\s*$")

const supersededPrefix = "> 🔁 **Superseded**"

// PostComment posts a NEW comment for the workspace (the newest one is the live one). The bot's older comments with the same
// marker keep their text and get a one-line banner that links the new comment (the marker stays on the first line). The new
// comment is posted first, so a failure never leaves the pull request without a plan. The trailing apply hint of an older
// comment is removed with the banner, so only the newest comment offers `/tofu apply`. Only the bot's own comments are touched:
// a stranger's comment with the marker is never edited. It never fails the run: problems are annotations.
func PostComment(c gh.Client, gha *ghactions.Runner, repo, pr, marker, body string) {
	old, _ := listComments(c, repo, pr) // best effort: when the listing fails, only the new comment is posted
	var todo []prComment
	for _, cm := range old {
		if !cm.isBot() || !strings.HasPrefix(cm.Body, marker) {
			continue
		}
		parts := strings.Split(cm.Body, "\n")
		if len(parts) > 1 && strings.HasPrefix(parts[1], supersededPrefix) {
			continue
		}
		todo = append(todo, cm)
	}
	var created prComment
	out, err := c.Run("api", fmt.Sprintf("repos/%s/issues/%s/comments", repo, pr), "-f", "body="+body)
	if err != nil {
		gha.Error("could not post the plan comment (does the workflow grant pull-requests: write?)")
		return
	}
	_ = json.Unmarshal([]byte(out), &created)
	if len(todo) == 0 || created.HTMLURL == "" {
		return
	}
	banner := "> 🔁 **Superseded** by " + created.HTMLURL + ". The plan below is kept for reference only."
	for _, cm := range todo {
		text := hintRe.ReplaceAllString(cm.Body, "")
		parts := strings.Split(text, "\n")
		text = parts[0] + "\n" + banner + "\n" + strings.Join(parts[1:], "\n")
		if _, err := c.Run("api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repo, cm.ID), "-f", "body="+text); err != nil {
			gha.Warning(fmt.Sprintf("could not mark the older plan comment %d as superseded", cm.ID))
		}
	}
}
