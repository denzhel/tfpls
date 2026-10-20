// Package result is the optional aggregated check of the plan jobs (actions/result).
package result

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Run reads the `needs` context (JSON) and prints one line per job. It returns 0 when every needed job
// succeeded or was skipped (nothing to plan), and 1 when one failed, was cancelled, has no result, or the input is not valid.
func Run(needs string, stdout io.Writer) int {
	dec := json.NewDecoder(strings.NewReader(needs))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		fmt.Fprintln(stdout, "::error::the needs context is not a JSON object")
		return 1
	}
	ok := true
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			fmt.Fprintln(stdout, "::error::the needs context is not valid JSON")
			return 1
		}
		var job struct {
			Result string `json:"result"`
		}
		if err := dec.Decode(&job); err != nil {
			fmt.Fprintln(stdout, "::error::the needs context is not valid JSON")
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s\n", key, job.Result)
		if job.Result != "success" && job.Result != "skipped" {
			ok = false
		}
	}
	if _, err := dec.Token(); err != nil || dec.More() {
		fmt.Fprintln(stdout, "::error::the needs context is not valid JSON")
		return 1
	}
	if !ok {
		fmt.Fprintln(stdout, "::error::at least one plan job failed or was cancelled")
		return 1
	}
	fmt.Fprintln(stdout, "all plans succeeded (or nothing to plan)")
	return 0
}
