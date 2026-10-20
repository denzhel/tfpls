package onboard

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

func pretty(p string) string {
	switch p {
	case "aws":
		return "AWS"
	case "cloudflare":
		return "Cloudflare"
	case "google", "google-beta":
		return "Google Cloud"
	case "azurerm", "azuread":
		return "Azure"
	case "github":
		return "GitHub"
	case "digitalocean":
		return "DigitalOcean"
	case "hcloud":
		return "Hetzner Cloud"
	}
	return p
}

// credVars: the environment variables a provider's credentials usually come from (nothing when unknown).
func credVars(p string) []string {
	switch p {
	case "cloudflare":
		return []string{"CLOUDFLARE_API_TOKEN"}
	case "google", "google-beta":
		return []string{"GOOGLE_CREDENTIALS"}
	case "azurerm", "azuread":
		return []string{"ARM_CLIENT_ID", "ARM_CLIENT_SECRET", "ARM_TENANT_ID", "ARM_SUBSCRIPTION_ID"}
	case "github":
		return []string{"GITHUB_TOKEN"}
	case "digitalocean":
		return []string{"DIGITALOCEAN_TOKEN"}
	case "hcloud":
		return []string{"HCLOUD_TOKEN"}
	}
	return nil
}

func (s *state) clouds() string {
	var out []string
	for _, p := range s.providers {
		if pn := pretty(p); !contains(out, pn) {
			out = append(out, pn)
		}
	}
	return strings.Join(out, ", ")
}

func (s *state) understand() {
	for _, p := range s.providers {
		switch {
		case p == "aws":
			s.awsProvider = true
		case len(credVars(p)) > 0:
			s.others = append(s.others, p)
		default:
			s.unknown = append(s.unknown, p)
		}
	}
	// AWS credentials (the OIDC role) are needed for an AWS provider, for state in AWS S3, and when nothing says otherwise
	if s.awsProvider || s.s3aws > 0 || (len(s.providers) == 0 && s.s3r2+s.s3other == 0) {
		s.needsAWS = true
	}
	s.credsWord = "the keys come from TFPLS_ENV_JSON"
	if s.needsAWS {
		s.credsWord = "the OIDC role is used"
	}
}

func (s *state) report() {
	bs := s.docs + "/backends.md"
	s.section("🔎 Looking around")
	switch {
	case s.inGit && s.owner != "":
		s.row("Repository", fmt.Sprintf("%s/%s on GitHub, default branch %s (%s)", s.owner, s.repo, s.branch, s.branchSrc))
	case s.inGit:
		s.row("Repository", fmt.Sprintf("git without a GitHub remote (origin), default branch %s (%s)", s.branch, s.branchSrc))
	default:
		s.row("Repository", "not a git repository")
	}
	switch s.layout {
	case "folders":
		s.row("Layout", fmt.Sprintf("%d workspace(s) in workspaces/: %s%s%s", len(s.found), s.B, strings.Join(s.found, " "), s.R))
		s.note(s.D + "the folder names are the tofu workspaces; tofu runs in the repository root, so your root module must load what is in them" + s.R)
	case "varfile":
		s.row("Layout", fmt.Sprintf("one root module with a var file per workspace in %s/: %s%s%s", s.vardir, s.B, strings.Join(s.varfiles, " "), s.R))
	case "envs":
		s.row("Layout", fmt.Sprintf("%d separate root module(s) with a backend: %s%s%s", len(s.envdirs), s.B, strings.Join(s.envdirs, " "), s.R))
		if s.rootStack {
			s.note("the root module has a backend too: it becomes a stack of its own")
		}
	case "root":
		s.row("Layout", "one root stack: the root module in ., a single state (the tofu workspace default)")
	default:
		s.row("Layout", "none found")
	}
	if len(s.tfAll) == 0 {
		s.row("State", "-")
	} else {
		var where []string
		if s.s3aws > 0 {
			where = append(where, "AWS S3")
		}
		if s.s3r2 > 0 {
			where = append(where, "Cloudflare R2 (custom endpoints)")
		}
		if s.s3other > 0 {
			where = append(where, "an S3-compatible service (custom endpoints)")
		}
		switch {
		case len(where) > 0:
			lk := "native locking (use_lockfile)"
			if s.lockMissing > 0 {
				lk = "no native locking"
			}
			s.row("State", fmt.Sprintf("backend \"s3\" on %s, %s", strings.Join(where, ", "), lk))
		case s.beOther != "":
			s.row("State", fmt.Sprintf("backend \"%s\"", s.beOther))
		default:
			s.row("State", "no backend block")
		}
	}
	switch cl := s.clouds(); {
	case cl == "":
		s.row("Providers", "none found")
	case s.needsAWS:
		s.row("Providers", cl)
	default:
		s.row("Providers", cl+" (no AWS: no IAM role needed)")
	}
	if s.ver != "" {
		s.row("OpenTofu", fmt.Sprintf("%s (from %s)", s.ver, s.verSrc))
	} else {
		s.row("OpenTofu", fmt.Sprintf("%s, the default (%s)", TofuDefault, s.verHint))
	}
	mods := ""
	if len(s.modsLocal) > 0 {
		mods = "local: " + strings.Join(s.modsLocal, " ")
	}
	if s.modsRemote > 0 {
		if mods != "" {
			mods += "; "
		}
		mods += fmt.Sprintf("%d from a registry or git", s.modsRemote)
	}
	if mods == "" {
		mods = "none"
	}
	s.row("Modules", mods)
	var wfs []string
	for _, f := range s.callers {
		wfs = append(wfs, "tfpls caller "+path.Base(f))
	}
	for _, f := range s.pipelines {
		wfs = append(wfs, "tofu/terraform pipeline "+path.Base(f))
	}
	if s.wfOthers > 0 {
		wfs = append(wfs, fmt.Sprintf("%d other(s)", s.wfOthers))
	}
	if len(wfs) == 0 {
		s.row("Workflows", "none yet")
	} else {
		s.row("Workflows", strings.Join(wfs, "; "))
	}
	if s.codeowners != "" {
		s.row("CODEOWNERS", s.codeowners)
	} else {
		s.row("CODEOWNERS", "none (optional: a review rule for .github/ and *.tf)")
	}
	if s.cfgExists {
		s.row("Config", ".tfpls.yml exists: left as it is")
	} else {
		s.row("Config", ".tfpls.yml: none")
	}

	// what needs attention
	if !s.inGit {
		s.warn("this is not a git repository: the workflow assumes the default branch is main")
	}
	if !s.topOK {
		s.warn("this is not the root of the git repository: GitHub reads workflows only from .github/workflows in the root")
	}
	if s.layout == "none" {
		s.warn("no layout found (no workspaces/<name>/ folders, no var files next to a root module, no directories with a backend block, no .tf files in the root)")
	}
	for _, n := range s.skipped {
		s.warn(fmt.Sprintf("workspaces/%s: the name has characters other than letters, digits . _ - and is skipped", n))
	}
	if len(s.tfAll) == 0 {
		s.warn("no .tf files here: run this in the root of your repository")
	} else {
		if s.beOther != "" {
			s.warn(fmt.Sprintf("backend \"%s\": tfpls supports S3 state with native locking only (%s)", s.beOther, bs))
		}
		if s.cloudBlock {
			s.warn(fmt.Sprintf("a cloud {} block (HCP Terraform): tfpls supports S3 state only (%s)", bs))
		}
		if s.s3aws+s.s3r2+s.s3other == 0 && s.beOther == "" && !s.cloudBlock {
			s.warn(fmt.Sprintf("no backend \"s3\" block: state must be in S3 with native locking (%s)", bs))
		}
		if s.lockMissing > 0 {
			s.warn(fmt.Sprintf("no use_lockfile = true: S3 native locking is the supported way to lock state (%s)", bs))
		}
		if s.dynamo {
			s.warn(fmt.Sprintf("dynamodb_table: DynamoDB lock tables are not supported, native locking (use_lockfile = true) is (%s)", bs))
		}
		if s.regionMiss > 0 {
			s.warn("no region = ... in the backend block: set it there, CI has no AWS_REGION unless your role or credentials map provides one")
		}
		if s.profileFound {
			s.warn(fmt.Sprintf("a profile = \"...\" setting was found: it belongs to your laptop; in CI %s, so remove it from the backend and provider blocks (or set it from a variable)", s.credsWord))
		}
		if s.awsProvider && s.s3r2+s.s3other > 0 {
			s.warn(fmt.Sprintf("state in an S3-compatible service and an AWS provider: the role's AWS keys win over TFPLS_ENV_JSON, so the backend would get them too; this combination is not verified (%s)", bs))
		}
	}
	if s.ver != "" {
		if strings.HasPrefix(s.ver, "0.") || (strings.HasPrefix(s.ver, "1.") && len(s.ver) > 2 && s.ver[2] >= '0' && s.ver[2] <= '5' && s.ver[3] == '.') {
			s.warn(fmt.Sprintf("OpenTofu %s: that is a Terraform version (OpenTofu starts at 1.6.0), so the pipeline cannot install it: pin an OpenTofu version in .opentofu-version", s.ver))
		}
	} else if s.verDeep != "" {
		s.warn(fmt.Sprintf("a root module asks for OpenTofu %s (required_version), but the pipeline reads the version in the repository root only and would use %s: add a .opentofu-version file with %s", s.verDeep, TofuDefault, s.verDeep))
	}
	for _, f := range s.appliers {
		s.warn(f + " looks like another tofu/terraform pipeline that applies: two pipelines on one repository will both comment and apply")
	}
	for _, f := range s.pipelines {
		if !contains(s.appliers, f) {
			s.note(f + " looks like another tofu/terraform workflow (no apply found): fine next to tfpls if it only checks")
		}
	}
	if len(s.callers) > 1 {
		s.warn(fmt.Sprintf("%d workflows call tfpls (%s): each runs the whole pipeline, keep one", len(s.callers), strings.Join(s.callers, ", ")))
	}
	if s.cfgExists && !s.cfgHasStacks && s.layout != "folders" && s.layout != "none" {
		s.warn(".tfpls.yml has no stacks: block, but this layout needs one (it is never overwritten; the block to add is under Files)")
	}
	if !s.interactive {
		switch {
		case s.dry:
			s.note("--dry-run: nothing is asked and nothing is written")
		case s.yes:
			s.note("--yes: no questions, the defaults are taken")
		default:
			s.note("no terminal: no questions, the defaults are taken")
		}
	}
}

// ---- the GitHub step (only with a yes, only with an authenticated gh) -------------------------------------------------

func namesHave(out, name string) bool {
	for _, l := range strings.Split(out, "\n") {
		if f, _, _ := strings.Cut(l, "\t"); f == name {
			return true
		}
	}
	return false
}

func (s *state) onlineLookup() {
	s.stRole, s.stKey, s.stEnv = "unknown", "unknown", "unknown"
	if _, err := exec.LookPath("gh"); !(s.interactive && s.owner != "" && err == nil) {
		return
	}
	or := s.owner + "/" + s.repo
	s.section("🌐 GitHub (optional)")
	if !s.ask(fmt.Sprintf("Look %s up on GitHub with gh? Read-only: the IDs for the trust policy, the variable and the secrets", or), false) {
		s.note("skipped: nothing goes online")
		return
	}
	if _, _, err := s.quietly("asking gh who you are", nil, "gh", "auth", "status"); err != nil {
		s.warn("gh is not logged in (gh auth login): skipped")
		return
	}
	out, _, err := s.quietly("reading "+or, nil, "gh", "api", "repos/"+or, "--jq", "[.id, .owner.id, .default_branch, .visibility] | @tsv")
	if err != nil {
		s.warn(fmt.Sprintf("gh could not read %s: skipped", or))
		return
	}
	f := strings.FieldsFunc(firstLine(out), func(r rune) bool { return r == '\t' })
	for len(f) < 4 {
		f = append(f, "")
	}
	repoID, ownerID, b, v := f[0], f[1], f[2], strings.Join(f[3:], "\t")
	if !digitsRe.MatchString(repoID) || !digitsRe.MatchString(ownerID) {
		s.warn("gh gave an unexpected answer: skipped")
		return
	}
	s.ghRepoID, s.ghOwnerID, s.online = repoID, ownerID, true
	if v == "public" || v == "private" || v == "internal" {
		s.ghVis = v
	}
	vis := s.ghVis
	if vis == "" {
		vis = "unknown"
	}
	s.row("Visibility", vis)
	s.row("IDs", fmt.Sprintf("owner %s, repository %s (for the trust policy)", ownerID, repoID))
	if s.needsAWS {
		s.row("Trust", fmt.Sprintf("sub %s\"repo:%s/%s:*\"%s  and  %s\"repo:%s@%s/%s@%s:*\"%s", s.C, s.owner, s.repo, s.R, s.C, s.owner, ownerID, s.repo, repoID, s.R))
	}
	if b != "" && !pathCharsRe.MatchString(b) && b != s.branch {
		s.note(fmt.Sprintf("GitHub says the default branch is %s (here: %s): the push trigger uses %s", b, s.branch, b))
		s.branch, s.branchSrc = b, "GitHub"
	}
	if out, _, err := s.quietly("reading the variables", nil, "gh", "variable", "list", "--repo", or); err == nil {
		s.stRole = "missing"
		if namesHave(out, "TFPLS_ROLE_ARN") {
			s.stRole = "set"
		}
	}
	if out, _, err := s.quietly("reading the secrets", nil, "gh", "secret", "list", "--repo", or); err == nil {
		s.stKey, s.stEnv = "missing", "missing"
		if namesHave(out, "PLAN_ENCRYPTION_KEY") {
			s.stKey = "set"
		}
		if namesHave(out, "TFPLS_ENV_JSON") {
			s.stEnv = "set"
		}
	}
	s.row("Variable", "TFPLS_ROLE_ARN: "+s.stRole)
	s.row("Secrets", fmt.Sprintf("PLAN_ENCRYPTION_KEY: %s, TFPLS_ENV_JSON: %s", s.stKey, s.stEnv))
}

func (s *state) onlineSettings() {
	if !s.online {
		return
	}
	or := s.owner + "/" + s.repo
	s.section("🔐 Secrets and variables")
	if s.stKey == "missing" && s.ask(fmt.Sprintf("Generate a random PLAN_ENCRYPTION_KEY and set it as a secret of %s? It is never shown", or), true) {
		key, err := genKey()
		var e string
		if err == nil {
			_, e, err = s.quietly("setting PLAN_ENCRYPTION_KEY", strings.NewReader(key), "gh", "secret", "set", "PLAN_ENCRYPTION_KEY", "--repo", or)
		}
		if err == nil {
			s.ok("PLAN_ENCRYPTION_KEY set (generated here, never shown)")
			s.stKey = "set"
		} else {
			s.warn("could not set PLAN_ENCRYPTION_KEY: " + firstLine(e))
		}
	} else if s.stKey == "set" {
		s.ok("PLAN_ENCRYPTION_KEY is set")
	}
	if s.needsAWS && s.stRole == "missing" {
		ans := s.askText("The role for plan and apply, for TFPLS_ROLE_ARN (arn:aws:iam::<account>:role/<name>)", "", validARN)
		if ans != "" {
			if _, e, err := s.quietly("setting TFPLS_ROLE_ARN", nil, "gh", "variable", "set", "TFPLS_ROLE_ARN", "--body", ans, "--repo", or); err == nil {
				s.ok("TFPLS_ROLE_ARN set")
				s.stRole = "set"
			} else {
				s.warn("could not set TFPLS_ROLE_ARN: " + firstLine(e))
			}
		}
	} else if s.needsAWS && s.stRole == "set" {
		s.ok("TFPLS_ROLE_ARN is set")
	}
	if !s.needsAWS {
		if s.stEnv == "set" {
			s.ok("TFPLS_ENV_JSON is set")
		} else {
			s.note("TFPLS_ENV_JSON is not set: it holds your keys, so you set it yourself (the checklist says how)")
		}
	}
}

// ---- what tfpls will do -----------------------------------------------------------------------------------------------

func (s *state) inShort() {
	var sum string
	switch s.layout {
	case "folders":
		sum = fmt.Sprintf("one root module with %d workspace(s) in workspaces/", len(s.found))
	case "varfile":
		sum = fmt.Sprintf("one root module with a var file per workspace in %s/", s.vardir)
	case "envs":
		sum = fmt.Sprintf("%d separate root module(s)", len(s.envdirs))
		if s.rootStack {
			sum += " plus the root module"
		}
	case "root":
		sum = "one root module with a single state"
	default:
		sum = "no OpenTofu root module that I recognise"
	}
	s.section("🧠 In short")
	if cl := s.clouds(); cl != "" {
		sum += ", managing " + cl
	}
	var state, ci string
	switch {
	case s.s3r2 > 0:
		state = "state in Cloudflare R2"
	case s.s3other > 0:
		state = "state in an S3-compatible service"
	case s.s3aws > 0:
		state = "state in S3"
	case s.beOther != "":
		state = "state in a " + s.beOther + " backend"
	default:
		state = "no remote state yet"
	}
	switch {
	case len(s.callers) > 0:
		ci = "tfpls already set up"
	case len(s.pipelines) > 0:
		ci = "another tofu/terraform pipeline"
	default:
		ci = "no CI yet"
	}
	sum = fmt.Sprintf("%s, %s, %s.", sum, state, ci)
	r, n := utf8.DecodeRuneInString(sum)
	s.p("  %s%s\n", string(unicode.ToUpper(r)), sum[n:])
	if s.rootStack {
		s.stackName = s.askText("Name of the stack (shown in comments, used by /tofu apply <name>)", s.stackName, safe)
	}
	switch s.layout {
	case "folders", "varfile":
		s.p("  tfpls plans the workspaces a pull request changes; %s/tofu apply <workspace>%s applies one.\n", s.C, s.R)
	case "envs":
		s.p("  tfpls plans the root modules a pull request changes; %s/tofu apply <name>%s applies one (for example %s).\n", s.C, s.R, strings.ReplaceAll(s.envdirs[0], "/", "-"))
	case "root":
		s.p("  tfpls plans it on every pull request as the stack %s%s%s; %s/tofu apply %s%s applies it.\n", s.B, s.stackName, s.R, s.C, s.stackName, s.R)
	default:
		s.p("  Describe your layout in .tfpls.yml (stacks:) and run onboarding again.\n")
	}
}

// ---- the files --------------------------------------------------------------------------------------------------------

func (s *state) fileline(c, sym, name, text string) {
	s.p("  %s%s%s %-28s %s%s%s\n", c, sym, s.R, name, s.D, text, s.R)
}

// canWrite: the file (when it exists) or its nearest existing parent directory can be written.
func (s *state) canWrite(rel string) bool {
	p := s.abs(rel)
	if _, err := os.Lstat(p); err == nil {
		return writable(p) && writable(path.Dir(p))
	}
	p = path.Dir(p)
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		p = path.Dir(p)
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir() && writable(p)
}

// put writes through a temporary file next to the target, so a failed write leaves the old file whole.
func (s *state) put(rel, content string) error {
	p := s.abs(rel)
	if err := os.MkdirAll(path.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tfpls-new"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// filesStep returns false when the run ends here (the person said no to new files).
func (s *state) filesStep() bool {
	s.section("📝 Files")
	wfText := s.renderWorkflow()
	old, rerr := os.ReadFile(s.abs(s.wf))
	_, serr := os.Lstat(s.abs(s.wf))
	switch {
	case serr != nil:
		s.wfAction = "new"
	case rerr == nil && string(old) == wfText:
		s.wfAction = "same"
	default:
		s.wfAction = "differs"
	}
	cfgText := s.renderConfig()
	s.cfgAction = "none"
	if s.cfgExists {
		s.cfgAction = "keep"
	} else if s.layout != "folders" {
		s.cfgAction = "new"
	}
	switch s.wfAction {
	case "new":
		s.fileline(s.G, "+", s.wf, fmt.Sprintf("new: the caller (push to %s, runner %s)", s.branch, s.runsOn))
	case "same":
		s.fileline(s.D, "=", s.wf, "already up to date")
	default:
		s.fileline(s.Y, "~", s.wf, "differs from the recommended caller (the diff is below)")
	}
	switch s.cfgAction {
	case "new":
		if s.cfgActive {
			s.fileline(s.G, "+", ".tfpls.yml", "new: "+s.cfgWhat)
		} else {
			s.fileline(s.G, "+", ".tfpls.yml", "new: comments only, describe your layout in it")
		}
	case "keep":
		s.fileline(s.D, "=", ".tfpls.yml", "exists: left as it is (never overwritten)")
	default:
		s.fileline(s.D, " ", ".tfpls.yml", "not needed for workspaces/<name>/ folders")
	}
	if s.cfgAction == "keep" && !s.cfgHasStacks && s.layout != "folders" && s.layout != "none" {
		s.note("add this to your .tfpls.yml (onboarding never changes it):")
		in := false
		for _, l := range strings.Split(strings.TrimSuffix(cfgText, "\n"), "\n") {
			if !in && stacksRe.MatchString(l) {
				in = true
			}
			if in && !strings.HasPrefix(l, "#") {
				s.p("      %s\n", l)
			}
		}
	}
	if s.dry {
		if s.wfAction != "same" {
			s.showDiff(s.wf, string(old), serr == nil, wfText)
		}
		if s.cfgAction == "new" {
			s.showDiff(".tfpls.yml", "", false, cfgText)
		}
		if s.wfAction == "same" && s.cfgAction != "new" {
			s.ok("nothing would change: everything is already up to date")
		} else {
			s.note("dry run: nothing was written")
			s.left("Run onboarding again without --dry-run to write what is shown above")
		}
		return true
	}
	wfWrite, cfgWrite, changed := false, false, false
	if s.wfAction == "differs" {
		s.showDiff(s.wf, string(old), true, wfText)
		if s.force || s.ask(fmt.Sprintf("Replace %s with the recommended caller?", s.wf), false) {
			wfWrite = true
		} else {
			s.note(fmt.Sprintf("kept your %s (a yes or --force replaces it; .tfpls.yml is never overwritten)", s.wf))
			s.left(fmt.Sprintf("Your %s differs from the recommended caller (the diff above): keep it, or run onboarding again with --force", s.wf))
		}
	}
	if s.wfAction == "new" || s.cfgAction == "new" {
		if s.ask("Does that look right? Write the new file(s)?", true) {
			wfWrite = wfWrite || s.wfAction == "new"
			cfgWrite = s.cfgAction == "new"
		} else {
			s.note(fmt.Sprintf("nothing new was written. Tell me more with a .tfpls.yml of your own (%s/getting-started.md#another-layout) or the options (--help), then run me again", s.docs))
			if !wfWrite {
				return false
			}
		}
	}
	var targets []string
	if wfWrite {
		targets = append(targets, s.wf)
	}
	if cfgWrite {
		targets = append(targets, ".tfpls.yml")
	}
	for _, f := range targets {
		if !s.canWrite(f) {
			s.die(fmt.Sprintf("cannot write %s: the directory is not writable (check its permissions); nothing was changed", f))
		}
	}
	if wfWrite {
		if err := s.put(s.wf, wfText); err != nil {
			s.die(fmt.Sprintf("cannot write %s: %v", s.wf, err))
		}
		changed = true
		s.ok(fmt.Sprintf("%s written %s(runs on %s)%s", s.wf, s.D, s.runsOn, s.R))
		if s.wfAction == "new" {
			s.done(fmt.Sprintf("the caller workflow %s (written)", s.wf))
		} else {
			s.done(fmt.Sprintf("the caller workflow %s (replaced)", s.wf))
		}
	} else if s.wfAction == "same" {
		s.done(fmt.Sprintf("the caller workflow %s (already up to date)", s.wf))
	}
	switch {
	case cfgWrite:
		if err := s.put(".tfpls.yml", cfgText); err != nil {
			s.die(fmt.Sprintf("cannot write .tfpls.yml: %v", err))
		}
		changed = true
		if s.cfgActive {
			s.ok(fmt.Sprintf(".tfpls.yml written %s(describes your layout)%s", s.D, s.R))
			s.done(".tfpls.yml: " + s.cfgWhat + " (written)")
		} else {
			s.warn(fmt.Sprintf(".tfpls.yml has only comments: %sdescribe your layout%s with a stacks: block, otherwise the pipeline finds nothing to run", s.B, s.R))
			s.left("Describe your layout in .tfpls.yml (stacks:): " + s.docs + "/getting-started.md#another-layout")
		}
	case s.cfgAction == "keep":
		s.done(".tfpls.yml (yours, left as it is)")
	case s.cfgAction == "none":
		s.done("no .tfpls.yml needed for workspaces/<name>/ folders")
	}
	if !changed && s.wfAction != "differs" {
		s.ok("✨ nothing to change: everything is already up to date")
	}
	for _, f := range s.appliers {
		if s.ask(fmt.Sprintf("Retire %s? You delete it in the same pull request (onboarding never deletes a workflow)", f), false) {
			s.left(fmt.Sprintf("Delete %s in the same pull request: %sgit rm %s%s", f, s.C, f, s.R))
		} else {
			s.left(fmt.Sprintf("Keep only one pipeline that applies: %s applies too (%sgit rm %s%s retires it)", f, s.C, f, s.R))
		}
	}
	return true
}

// ---- what is left -----------------------------------------------------------------------------------------------------

// envJSONExample: the keys TFPLS_ENV_JSON needs: the providers' tokens, and the state keys of an S3-compatible service.
func (s *state) envJSONExample() string {
	var parts []string
	for _, p := range s.others {
		for _, v := range credVars(p) {
			if v == "CLOUDFLARE_API_TOKEN" {
				parts = append(parts, fmt.Sprintf("\"%s\": \"<API token>\"", v))
			} else {
				parts = append(parts, fmt.Sprintf("\"%s\": \"...\"", v))
			}
		}
	}
	if s.s3r2 > 0 {
		parts = append(parts, `"AWS_ACCESS_KEY_ID": "<R2 access key ID>"`, `"AWS_SECRET_ACCESS_KEY": "<R2 secret access key>"`)
	} else if s.s3other > 0 {
		parts = append(parts, `"AWS_ACCESS_KEY_ID": "<access key>"`, `"AWS_SECRET_ACCESS_KEY": "<secret key>"`)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (s *state) checklist() {
	c, r, d := s.C, s.R, s.D
	if s.needsAWS {
		if s.stRole == "set" {
			s.done("TFPLS_ROLE_ARN is set on GitHub")
		} else {
			var sub string
			if s.owner != "" {
				idform := fmt.Sprintf("repo:%s@*/%s@*:*", s.owner, s.repo)
				if s.ghOwnerID != "" {
					idform = fmt.Sprintf("repo:%s@%s/%s@%s:*", s.owner, s.ghOwnerID, s.repo, s.ghRepoID)
				}
				sub = fmt.Sprintf("Trust policy condition on token.actions.githubusercontent.com:sub, StringLike, both forms:\n  %s\"repo:%s/%s:*\"%s  and  %s\"%s\"%s", c, s.owner, s.repo, r, c, idform, r)
			} else {
				sub = d + `Trust policy: sub StringLike "repo:OWNER/REPO:*" and "repo:OWNER@*/REPO@*:*" (no GitHub remote found to fill it in)` + r
			}
			more := ""
			if len(s.others) > 0 {
				more = fmt.Sprintf("\nAlso: the secret TFPLS_ENV_JSON with %s (the role covers AWS only)", s.envJSONExample())
			}
			s.left(fmt.Sprintf("One IAM role for plan and apply (OIDC, no stored keys), then: %sgh variable set TFPLS_ROLE_ARN --body arn:aws:iam::<account>:role/<role>%s\n%s\n%s(or one secret with a JSON map of variables: gh secret set TFPLS_ENV_JSON < env.json)  More: %s/credentials.md%s%s",
				c, r, sub, d, s.docs, r, more))
		}
	} else if s.stEnv == "set" {
		s.done("TFPLS_ENV_JSON is set on GitHub")
	} else {
		item := fmt.Sprintf("Credentials: no AWS role is needed, one secret holds a JSON map of environment variables: %sgh secret set TFPLS_ENV_JSON < env.json%s\nenv.json: %s%s%s", c, r, c, s.envJSONExample(), r)
		if s.s3r2 > 0 {
			item += "\nThe R2 keys are an R2 API token (access key ID and secret) that can write the state bucket, including the .tflock object."
		}
		for _, p := range s.unknown {
			item += "\n" + p + ": put its credentials (environment variables) into the same map."
		}
		s.left(fmt.Sprintf("%s\n%sKeep env.json out of the repository (delete it after). More: %s/credentials.md and %s/backends.md%s", item, d, s.docs, s.docs, r))
	}
	switch {
	case s.stKey == "set":
		s.done("PLAN_ENCRYPTION_KEY is set on GitHub")
	case s.ghVis == "public":
		s.left(fmt.Sprintf("Encrypt saved plans (required: the repository is public): %sopenssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY%s", c, r))
	default:
		s.left(fmt.Sprintf("Encrypt saved plans: %sopenssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY%s", c, r))
	}
	if s.nwarn > 0 {
		s.left(fmt.Sprintf("Look at the %d warning(s) above (marked !)", s.nwarn))
	}
	name := "<workspace>"
	if s.layout == "root" {
		name = s.stackName
	}
	item := fmt.Sprintf("Commit the files and open ONE pull request: it plans everything and shows each plan as a comment. Merge it (nothing is applied); %s/tofu apply %s%s works on the next pull request.", c, name, r)
	if s.layout == "folders" || s.layout == "varfile" {
		item += fmt.Sprintf("\n%sWorkspaces that do not exist in your state yet fail the first plan: create each once with `tofu workspace new <name>` (tfpls never creates workspaces).%s", d, r)
	}
	s.left(item)
	s.left(fmt.Sprintf("Approvals follow your repository: the pull request rule of a ruleset on the default branch (1 approval when there is none). You cannot approve your own pull request: add a second account, set the ruleset to 0 approvals, or, as an admin, add %s--now%s to /tofu apply.", c, r))
	s.left(fmt.Sprintf("Optional: %sgh workflow run %s -f task=drift%s for a first drift check.", c, path.Base(s.wf), r))

	s.section("✅ Checklist")
	for _, it := range s.doneItems {
		s.ok(it)
	}
	for i, it := range s.leftItems {
		first, rest, multi := strings.Cut(it, "\n")
		s.p("  %s☐%s %s%d.%s %s\n", s.Y, r, s.B, i+1, r, first)
		if multi {
			for _, l := range strings.Split(rest, "\n") {
				s.p("       %s\n", l)
			}
		}
	}
}
