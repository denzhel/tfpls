//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/denzhel/tfpls/internal/onboard"
	"github.com/denzhel/tfpls/internal/setup"
)

const (
	planTwoAdds = "Plan: 2 to add, 0 to change, 0 to destroy."
	key         = "e2e-plan-key"
)

func apply(r *repo, kv ...string) result {
	return r.tfpls([]string{"tofu"}, append([]string{"APPLY=force", "PLAN_MODE=fresh"}, kv...)...)
}

// The tofu that the tests use is the one `tfpls setup` installs, and it is the pinned default: an OpenTofu bump that is not
// reflected in the code (or the other way round) fails here.
func TestInstalledTofuIsTheDefault(t *testing.T) {
	if useHostTofu {
		t.Skip("TFPLS_E2E_TOFU: the tofu of the PATH is used, so the pinned default is not checked")
	}
	r := newRepo(t, "basic")
	out, err := r.tofu("version", "-json")
	if err != nil {
		t.Fatalf("tofu version: %v\n%s", err, out)
	}
	var v struct {
		V string `json:"terraform_version"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("tofu version -json: %v\n%s", err, out)
	}
	// the installed version is what setup asks for; the other places that name the default must agree with what really runs
	eq(t, "installed OpenTofu", v.V, setup.DefaultTofu)
	eq(t, "default of onboarded repositories", onboard.TofuDefault, v.V)
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`exact \x60required_version\x60, else (\d+\.\d+\.\d+)`).FindSubmatch(doc)
	if m == nil {
		t.Fatal("docs/getting-started.md does not name the default OpenTofu any more")
	}
	eq(t, "default named in docs/getting-started.md", string(m[1]), v.V)
}

func TestPlanWithChanges(t *testing.T) {
	r := newRepo(t, "basic")
	x := r.tfpls([]string{"tofu"})
	eq(t, "exit code", itoa(x.rc), "0")
	eq(t, "status", x.outputs["status"], "planned")
	eq(t, "summary", x.outputs["summary"], "+2 ~0 -0")
	eq(t, "counts", x.outputs["counts"], `{"add":2,"change":0,"destroy":0,"replace":0}`)
	eq(t, "changes", x.outputs["changes"], `[{"action":"added","address":"terraform_data.a"},{"action":"added","address":"terraform_data.b"}]`)
	eq(t, "reviewed", x.outputs["reviewed"], "false")
	contains(t, "console", x.out, planTwoAdds)
	for _, want := range []string{
		"<!-- tfpls:terraform/dev -->\n<!-- tfpls-status: planned -->\n**Plan:** 2 to add, 0 to change, 0 to destroy.\n",
		"### 📝 [dev](https://github.com/o/r/actions/runs/5) plan logs",
		"  # terraform_data.a will be created\n  + resource \"terraform_data\" \"a\" {\n      + id     = (known after apply)\n      + input  = \"alpha\"",
		"  # terraform_data.b will be created",
		"\n`/tofu apply dev` to apply",
	} {
		contains(t, "plan comment", x.comment, want)
	}
	eq(t, "nothing was applied", r.stateAddresses(), "")
	if _, err := os.Stat(filepath.Join(r.stack(), "tfplan")); err == nil {
		t.Error("the plan file is left in the working directory")
	}
}

func TestPlanNoChangesAfterApply(t *testing.T) {
	r := newRepo(t, "basic")
	x := apply(r)
	eq(t, "apply status", x.outputs["status"], "applied")
	contains(t, "apply comment", x.comment, "**Applied:** 2 added, 0 changed, 0 destroyed.\n")
	contains(t, "apply comment", x.comment, "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.")
	eq(t, "state", r.stateAddresses(), "terraform_data.a\nterraform_data.b")

	x = r.tfpls([]string{"tofu"})
	eq(t, "exit code", itoa(x.rc), "0")
	eq(t, "status", x.outputs["status"], "no-changes")
	eq(t, "summary", x.outputs["summary"], "No changes.")
	eq(t, "counts", x.outputs["counts"], "")
	eq(t, "changes", x.outputs["changes"], "[]")
	contains(t, "comment", x.comment, "<!-- tfpls-status: no-changes -->\n**No changes.**\n")
}

func TestApplyTargeted(t *testing.T) {
	r := newRepo(t, "basic")
	x := apply(r, "TARGETS=terraform_data.a")
	eq(t, "status", x.outputs["status"], "applied")
	eq(t, "summary", x.outputs["summary"], "+1 ~0 -0")
	eq(t, "changes", x.outputs["changes"], `[{"action":"added","address":"terraform_data.a"}]`)
	contains(t, "comment", x.comment, "**Applied:** 1 added, 0 changed, 0 destroyed.\n")
	eq(t, "only the target is in the state", r.stateAddresses(), "terraform_data.a")

	x = r.tfpls([]string{"tofu"})
	eq(t, "the rest is still planned", x.outputs["summary"], "+1 ~0 -0")
	eq(t, "rest", x.outputs["changes"], `[{"action":"added","address":"terraform_data.b"}]`)

	x = r.tfpls([]string{"tofu"}, "TARGETS=terraform_data.a;rm")
	eq(t, "a bad target is refused", x.outputs["status"], "error")
	contains(t, "reason", x.outputs["summary"], "Invalid -target value")
}

// savePlan makes the saved plan of the current tree the way a pull request run does and returns its path; the artifact the
// stub serves for the later, separate, apply invocation is that file.
func savePlan(t *testing.T, r *repo, planKey string) string {
	t.Helper()
	x := r.tfpls([]string{"tofu"}, "SAVE_PLAN=true", "HEAD_SHA=abc123", "PLAN_ENCRYPTION_KEY="+planKey)
	eq(t, "plan status", x.outputs["status"], "planned")
	p := x.outputs["plan_path"]
	if p == "" {
		t.Fatalf("no plan_path output:\n%s", x.out)
	}
	art := filepath.Join(r.stub, "artifact")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, filepath.Base(p)), b, 0o600); err != nil {
		t.Fatal(err)
	}
	listing := `{"artifacts":[{"id":1,"expired":false,"created_at":"2026-01-01T00:00:00Z","workflow_run":{"id":5,"repository_id":100,"head_repository_id":100,"head_branch":"main","head_sha":"abc123"}}]}`
	if err := os.WriteFile(filepath.Join(r.stub, "artifacts.json"), []byte(listing), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(art, filepath.Base(p))
}

func applyReviewed(r *repo, planKey string) result {
	return r.tfpls([]string{"tofu"}, "APPLY=force", "PLAN_MODE=reviewed_only", "HEAD_SHA=abc123", "PLAN_ENCRYPTION_KEY="+planKey)
}

func TestReviewedPlanAppliedInASeparateInvocation(t *testing.T) {
	r := newRepo(t, "basic")
	savePlan(t, r, key)
	eq(t, "planning applies nothing", r.stateAddresses(), "")

	x := applyReviewed(r, key)
	eq(t, "exit code", itoa(x.rc), "0")
	eq(t, "status", x.outputs["status"], "applied")
	eq(t, "the reviewed plan was used", x.outputs["reviewed"], "true")
	contains(t, "comment", x.comment, "**Applied:** 2 added, 0 changed, 0 destroyed.\n")
	contains(t, "console", x.out, "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.")
	eq(t, "state", r.stateAddresses(), "terraform_data.a\nterraform_data.b")

	// a saved plan can be applied once: the state moved, so the same plan is stale now
	x = applyReviewed(r, key)
	eq(t, "second apply of the same plan", x.outputs["status"], "error")
	contains(t, "reason", x.outputs["summary"], "reviewed plan can no longer be applied")
}

func TestReviewedPlanRefusals(t *testing.T) {
	t.Run("the tree changed after the plan", func(t *testing.T) {
		r := newRepo(t, "basic")
		savePlan(t, r, key)
		if err := os.WriteFile(filepath.Join(r.root, "README.md"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, r.root, "add", "-A")
		git(t, r.root, "commit", "-q", "-m", "change")
		x := applyReviewed(r, key)
		eq(t, "exit code", itoa(x.rc), "1")
		eq(t, "status", x.outputs["status"], "error")
		contains(t, "reason", x.outputs["summary"], "No reviewed plan to apply: the reviewed plan was made for different code (plan tree ")
		contains(t, "reason", x.outputs["summary"], "run /tofu plan again")
		eq(t, "nothing applied", r.stateAddresses(), "")
	})
	t.Run("wrong key", func(t *testing.T) {
		r := newRepo(t, "basic")
		savePlan(t, r, key)
		x := applyReviewed(r, "another-key")
		eq(t, "exit code", itoa(x.rc), "1")
		eq(t, "status", x.outputs["status"], "error")
		contains(t, "reason", x.outputs["summary"], "No reviewed plan to apply: could not decrypt the saved plan (wrong key or corrupted file)")
		eq(t, "nothing applied", r.stateAddresses(), "")
	})
	t.Run("tampered file", func(t *testing.T) {
		r := newRepo(t, "basic")
		p := savePlan(t, r, key)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b[len(b)/2] ^= 0xff
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		x := applyReviewed(r, key)
		eq(t, "exit code", itoa(x.rc), "1")
		eq(t, "status", x.outputs["status"], "error")
		contains(t, "reason", x.outputs["summary"], "No reviewed plan to apply: the saved plan failed its integrity check (wrong key or tampered)")
		eq(t, "nothing applied", r.stateAddresses(), "")
	})
	t.Run("no plan was saved", func(t *testing.T) {
		r := newRepo(t, "basic")
		x := applyReviewed(r, key)
		eq(t, "status", x.outputs["status"], "error")
		contains(t, "reason", x.outputs["summary"], "No reviewed plan to apply: no reviewed plan found (")
		eq(t, "nothing applied", r.stateAddresses(), "")
	})
}

// hold starts a second tofu that takes the state lock and keeps it (a provisioner sleeps) until the test ends.
func hold(t *testing.T, r *repo) {
	t.Helper()
	c := exec.Command("tofu", "apply", "-auto-approve", "-input=false", "-no-color", "-var", "hold=30")
	c.Dir = r.stack()
	c.Env = append(os.Environ(), "PATH="+pathEnv(), "TF_IN_AUTOMATION=1")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_, _ = c.Process.Wait()
	})
	for i := 0; i < 150; i++ {
		if m, _ := filepath.Glob(filepath.Join(r.stack(), ".terraform.tfstate.lock.info")); len(m) > 0 {
			// the file appears before the holder runs the provisioner; the lock is real from here on
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the second tofu never took the lock")
}

func lockInfo(t *testing.T, r *repo) (id, who, op string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.stack(), ".terraform.tfstate.lock.info"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ ID, Who, Operation string }
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v.ID, v.Who, v.Operation
}

func TestStateLockIsParsedFromTheRealBoxedText(t *testing.T) {
	r := newRepo(t, "basic")
	hold(t, r)
	id, who, op := lockInfo(t, r)
	eq(t, "operation of the holder", op, "OperationTypeApply")

	// the plan of a workspace whose state is locked says who holds the lock
	x := r.tfpls([]string{"tofu"}, "LOCK_TIMEOUT=0s")
	eq(t, "exit code", itoa(x.rc), "1")
	eq(t, "status", x.outputs["status"], "error")
	re := regexp.MustCompile(`^plan failed: the state is locked by ` + regexp.QuoteMeta(who) + ` since \d{4}-\d\d-\d\d \d\d:\d\d:\d\d \(OperationTypeApply\)$`)
	if !re.MatchString(x.outputs["summary"]) {
		t.Errorf("summary %q does not match %s", x.outputs["summary"], re)
	}
	contains(t, "console", x.out, "Error acquiring the state lock")

	// tfpls unlock reads the same text: the report names the real lock ID without releasing anything
	u := r.tfpls([]string{"unlock"}, "NAME=terraform/dev", "DIR=stack", "REPORT_ONLY=true")
	eq(t, "unlock exit code", itoa(u.rc), "0")
	eq(t, "unlock result", u.outputs["result"], "locked")
	eq(t, "unlock lock_id", u.outputs["lock_id"], id)
	contains(t, "unlock detail", u.outputs["detail"], "held by `"+who+"` since ")
	contains(t, "unlock detail", u.outputs["detail"], "operation OperationTypeApply, lock "+id)
	contains(t, "unlock console", u.out, "locked: "+who+" since ")

	// a lock this young is probably a live run: the real unlock refuses, with the parsed holder
	u = r.tfpls([]string{"unlock"}, "NAME=terraform/dev", "DIR=stack")
	eq(t, "refusal exit code", itoa(u.rc), "1")
	eq(t, "refusal result", u.outputs["result"], "refused")
	contains(t, "refusal detail", u.outputs["detail"], "the lock is only 0 min old (minimum 5); it is probably still in use. Holder: `"+who+"`, operation: `OperationTypeApply`")
	if _, err := os.Stat(filepath.Join(r.stack(), ".terraform.tfstate.lock.info")); err != nil {
		t.Error("the lock was released although it is fresh")
	}
}

// tamper changes the input of terraform_data.a in the state file: what someone else's change (or a manual edit) does between runs.
func tamper(t *testing.T, r *repo) {
	t.Helper()
	p := filepath.Join(r.stack(), "terraform.tfstate")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	res := st["resources"].([]any)[0].(map[string]any)
	if res["name"] != "a" {
		t.Fatalf("the first resource of the state is %v, not a", res["name"])
	}
	in := res["instances"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["input"].(map[string]any)
	in["value"] = "changed-out-of-band"
	out, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDriftIsDetectedAndFixed(t *testing.T) {
	r := newRepo(t, "basic")
	apply(r)
	x := r.tfpls([]string{"tofu"})
	eq(t, "in sync", x.outputs["status"], "no-changes")

	tamper(t, r)
	x = r.tfpls([]string{"tofu"}, "PR=")
	eq(t, "drift: exit code", itoa(x.rc), "0")
	eq(t, "drift: status", x.outputs["status"], "planned")
	eq(t, "drift: summary", x.outputs["summary"], "+0 ~1 -0")
	eq(t, "drift: changes", x.outputs["changes"], `[{"action":"changed","address":"terraform_data.a"}]`)
	contains(t, "drift: console", x.out, "# terraform_data.a will be updated in-place")
	contains(t, "drift: console", x.out, `~ input  = "changed-out-of-band" -> "alpha"`)

	// the drift-fix guard keeps a workspace that allows the fix and drops one that forbids it
	entries := `{"dev":{"label":"dev","name":"terraform/dev","dir":"stack"},"prod":{"label":"prod","name":"terraform/prod","dir":"prod","drift_fix":false},"stage":{"label":"stage","name":"terraform/stage","dir":"stage","drift_fix":"false"}}`
	f := r.tfpls([]string{"drift-fix", "filter"}, "ENTRIES="+entries, "REPO=o/r")
	eq(t, "filter exit code", itoa(f.rc), "0")
	eq(t, "filter matrix", f.outputs["matrix"], `{"include":[{"label":"dev"}]}`)
	contains(t, "filter console", f.out, "`prod stage`: fixing a drift on demand is switched off for this workspace (`drift_fix: false`)")

	// the fix: apply on demand, then the workspace is in sync again
	x = apply(r, "PR=")
	eq(t, "fix: status", x.outputs["status"], "applied")
	contains(t, "fix: console", x.out, "Apply complete! Resources: 0 added, 1 changed, 0 destroyed.")
	x = r.tfpls([]string{"tofu"}, "PR=")
	eq(t, "after the fix", x.outputs["status"], "no-changes")
}

func TestPolicy(t *testing.T) {
	if !haveConftest {
		t.Skip("conftest is not installed (tfpls setup could not fetch it): the policy scenario is skipped")
	}
	t.Run("a denied change fails", func(t *testing.T) {
		r := newRepo(t, "basic")
		copyDir(t, filepath.Join("testdata", "policy"), filepath.Join(r.root, "policies"))
		x := r.tfpls([]string{"tofu"}, "POLICY_DIR=policies")
		eq(t, "exit code", itoa(x.rc), "1")
		eq(t, "status", x.outputs["status"], "policy-failed")
		contains(t, "comment", x.comment, "Policy: 🛑 failed\n```\n")
		contains(t, "comment", x.comment, "terraform_data.b is not allowed")
		contains(t, "console", x.out, "terraform_data.b is not allowed")
	})
	t.Run("a policy that holds passes", func(t *testing.T) {
		r := newRepo(t, "basic")
		copyDir(t, filepath.Join("testdata", "policy"), filepath.Join(r.root, "policies"))
		if err := os.Remove(filepath.Join(r.root, "policies", "deny_b.rego")); err != nil {
			t.Fatal(err)
		}
		x := r.tfpls([]string{"tofu"}, "POLICY_DIR=policies")
		eq(t, "exit code", itoa(x.rc), "0")
		eq(t, "status", x.outputs["status"], "planned")
		contains(t, "comment", x.comment, "Policy: ✅ passed")
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
