#!/usr/bin/env bash
# tools/onboard: the layouts (folders, var files, separate roots, one root stack), the files it writes (valid workflow and
# config), a second run changes nothing, a differing workflow is never replaced without a yes or --force, answers through
# the test hook and (a real terminal under `curl | bash`: internal/onboard TestRealTerminal), no questions and no colours without a terminal,
# --yes, --dry-run, the optional GitHub step (gh stub, offline otherwise), jokes, a non-writable target, bash 3.2.
# The program is the Go binary (internal/onboard), built once per run and called through a 2-line sh wrapper.
# TFPLS_ONBOARD=<path> runs the same checks against another program (for example the previous Bash version).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
t=$(mktemp -d); trap 'chmod -R u+w "$t" 2>/dev/null; rm -rf "$t"' EXIT
init="${TFPLS_ONBOARD:-}"
if [ -z "$init" ]; then
  build_bin tfpls "$t/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
  printf '#!/bin/sh\nexec "%s" onboard "$@"\n' "$t/tfpls" > "$t/onboard"; chmod +x "$t/onboard"; init="$t/onboard"
fi
unset NO_COLOR FORCE_COLOR TFPLS_ONBOARD_TTY
export TFPLS_JOKE_SEED=0

# a gh stub logs every call: nothing may call gh without a yes to the GitHub step (offline by default)
mkdir -p "$t/bin"; cat > "$t/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
case "$1 $2" in
  "auth status") [ "${STUB_AUTH:-ok}" = ok ] || exit 1 ;;
  "api repos/"*) printf '5678\t1234\tmain\t%s\n' "${STUB_VIS:-private}" ;;
  "variable list") printf '%b' "${STUB_VARS:-}" ;;
  "secret list") printf '%b' "${STUB_SECRETS:-}" ;;
  "secret set") cat > "$STUB_DIR/secret.$3" ;;
  "variable set") printf '%s\n' "$5" > "$STUB_DIR/variable.$3" ;;
esac
exit 0
STUB
chmod +x "$t/bin/gh"; export PATH="$t/bin:$PATH" STUB_LOG="$t/gh.log" STUB_DIR="$t"; : > "$t/gh.log"

ob() { "$init" "$@" </dev/null 2>&1; }                                     # no terminal: stdout captured, no input
ans() { local a=$1; shift; printf '%b' "$a" | TFPLS_ONBOARD_TTY=/dev/stdin "$init" "$@" 2>&1; }   # answers via stdin
snap() { (cd "$1" && find . -path ./.git -prune -o -type f -print | LC_ALL=C sort | while IFS= read -r f; do cksum "$f"; done); }
lc() { grep -cxF -- "$2" "$1"; }   # lc <file> <line>: how many lines of the file are exactly this text (the files are written by the program, so their text is fixed)
valid() { [ -x "$t/tfpls" ] || build_bin tfpls "$t/tfpls"; "$t/tfpls" validate-config < "$1"; }
noop() { # noop <label> <dir> [options]: a second run changes no file and says so
  local d=$1 s1 o; shift; d=$1; shift
  s1=$(snap "$d"); o=$(ob "$@" "$d"); local rc=$?
  [ "$rc" -eq 0 ] && [ "$(snap "$d")" = "$s1" ] && grep -q 'already up to date' <<<"$o" && ok "second run changes nothing and says so: $d" || bad "second run: rc=$rc $o"
}

# ---- layout 1: one folder per workspace (no config needed)
mkdir -p "$t/a/workspaces/dev-eu-north-1" "$t/a/workspaces/stg-eu-north-1" "$t/a/workspaces/prod-eu-north-1"
out=$(ob "$t/a"); eq "exit 0 on a fresh repo" "$?" 0
[ -f "$t/a/.github/workflows/tofu.yml" ] && ok "workflow written" || bad "workflow written"
[ -e "$t/a/.tfpls.yml" ] && bad "workspaces/* needs no config file" || ok "workspaces/* layout: no config file"
grep -q "3 workspace(s) in workspaces/: dev-eu-north-1 prod-eu-north-1 stg-eu-north-1" <<<"$out" && ok "workspaces detected and shown" || bad "workspaces detected: $out"
grep -q "gh variable set TFPLS_ROLE_ARN" <<<"$out" && grep -q "PLAN_ENCRYPTION_KEY" <<<"$out" && ok "checklist printed" || bad "checklist printed"
eq "output fits one screen (50 lines)" "$([ "$(wc -l <<<"$out")" -le 50 ] && echo yes || echo no)" yes
grep -q 'tfpls.*onboarding' <<<"$out" && grep -q '|_______|/' <<<"$out" && ok "the ASCII logo" || bad "logo"
grep -q '🔎 Looking around' <<<"$out" && grep -q '🧠 In short' <<<"$out" && grep -q '✅ Checklist' <<<"$out" && ok "an icon per step" || bad "icons"
grep -q '✔ the caller workflow .github/workflows/tofu.yml (written)' <<<"$out" && grep -q '☐ 1\. One IAM role' <<<"$out" && ok "the checklist shows what is done and what is left" || bad "done/left: $out"
grep -q 'no terminal: no questions' <<<"$out" && ! grep -qE '\[Y/n\]|\[y/N\]' <<<"$out" && ok "no questions without a terminal" || bad "prompts without a terminal: $out"
eq "uses the entry workflow at @v0" "$(lc "$t/a/.github/workflows/tofu.yml" "    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0")" 1
eq "secrets inherited" "$(lc "$t/a/.github/workflows/tofu.yml" "    secrets: inherit")" 1
eq "no with: block for the default runner" "$(grep -c '^    with:' "$t/a/.github/workflows/tofu.yml")" 0
if command -v actionlint >/dev/null; then actionlint "$t/a/.github/workflows/tofu.yml" && ok "generated workflow passes actionlint" || bad "generated workflow passes actionlint"; fi
noop - "$t/a"

# ---- layout 2: var files
mkdir -p "$t/v/vars"; printf 'terraform {\n  backend "s3" {}\n}\n' > "$t/v/main.tf"; touch "$t/v/vars/dev.tfvars" "$t/v/vars/prod.tfvars" "$t/v/vars/terraform.tfvars"
git init -q "$t/v"; git -C "$t/v" remote add origin https://github.com/al-ice/my.repo.git
out=$(ob "$t/v"); c="$t/v/.tfpls.yml"
eq "var files become workspaces of one stack" "$(lc "$c" "    workspaces: [dev, prod]")|$(grep -c '^  - ' "$c")" "1|1"
eq "var_file template" "$(lc "$c" '    var_file: "vars/{workspace}.tfvars"')" 1
eq "a var file selects only its workspace" "$(grep -cF '    workspace_paths: ["vars/{workspace}.tfvars"]' "$c")" 1
eq "the var-file config is valid" "$(valid "$c")" "[]"
eq "trust policy shows both subject forms from the remote" "$(grep -c -e '"repo:al-ice/my.repo:\*"' -e '"repo:al-ice@\*/my.repo@\*:\*"' <<<"$out")" 1
! grep -q 'auto_apply: never' "$c" && ok "no per-workspace no-op lines" || bad "no-op lines"
noop - "$t/v"
mkdir -p "$t/vr"; printf 'terraform {\n  backend "s3" {}\n}\n' > "$t/vr/main.tf"; touch "$t/vr/dev.tfvars" "$t/vr/prod.tfvars"; ob "$t/vr" >/dev/null
eq "var files in the root: the paths have no ./ (a change to dev.tfvars plans only dev)" "$(grep -cF '    workspace_paths: ["{workspace}.tfvars"]' "$t/vr/.tfpls.yml")" 1

# ---- layout 3: separate root modules with a backend, modules watched
mkdir -p "$t/ev/envs/dev" "$t/ev/envs/prod" "$t/ev/modules/net"
for e in dev prod; do printf 'terraform {\n  backend "s3" {}\n}\nmodule "n" {\n  source = "../../modules/net"\n}\nmodule "x" {\n  source = "../../../outside"\n}\n' > "$t/ev/envs/$e/main.tf"; done
out=$(ob "$t/ev"); c="$t/ev/.tfpls.yml"
eq "each backend directory is a stack on default" "$(grep -E '^  - ' "$c" | sed 's/ *watch: \[[^]]*\]//; s/,  *}/ }/' | tr '\n' '|')" "  - { name: envs-dev, dir: envs/dev, workspaces: [default] }|  - { name: envs-prod, dir: envs/prod, workspaces: [default] }|"
eq "watch comes from the relative module sources" "$(grep -cF 'dir: envs/dev, workspaces: [default], watch: [modules/net] }' "$c")" 1
eq "the envs config is valid" "$(valid "$c")" "[]"
grep -q 'Modules     local: modules/net' <<<"$out" && ok "module relations are summarised" || bad "modules row: $out"
noop - "$t/ev"

# ---- layout 4: one root stack, the shape of a Cloudflare repository with state in R2 (synthetic names, no real ids)
r="$t/edge"; mkdir -p "$r/workers"; git init -q -b main "$r"; git -C "$r" remote add origin https://github.com/acme-co/edge-infra.git
cat > "$r/versions.tf" <<'TF'
terraform {
  required_version = "1.13.1"
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.27.0"
    }
  }
  backend "s3" {
    bucket = "state"
    key    = "edge/terraform.tfstate"
    region = "auto"
    endpoints = {
      s3 = "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com"
    }
    use_lockfile                = true
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
  }
}
TF
printf 'provider "cloudflare" {}\n' > "$r/providers.tf"; printf 'resource "cloudflare_zone" "z" {\n  name = "example.com"\n}\n' > "$r/zones.tf"; echo x > "$r/workers/w.js"
cp -R "$r" "$t/edge-dry"
out=$(ob "$r"); eq "the one root stack: exit 0" "$?" 0; c="$r/.tfpls.yml"
eq "one stack named after the repository, in ., on the default workspace" "$(grep -E '^(  - |    (dir|workspaces):)' "$c" | tr '\n' '|')" "  - name: edge-infra|    dir: .|    workspaces: [default]|"
eq "the one-root-stack config is valid" "$(valid "$c")" "[]"
eq "docs and the workflow itself plan nothing" "$(grep -c '^    ignore: \["\*\*/\*.md", "docs/\*\*", ".github/\*\*", "LICENSE\*", ".gitignore"\]   # changes here plan nothing$' "$c")" 1
grep -q 'one root stack' <<<"$out" && grep -q 'Cloudflare R2' <<<"$out" && grep -q 'no AWS: no IAM role needed' <<<"$out" && ok "understands the root stack, R2 and a non-AWS provider" || bad "understanding: $out"
grep -q 'gh secret set TFPLS_ENV_JSON' <<<"$out" && grep -q '"CLOUDFLARE_API_TOKEN"' <<<"$out" && grep -q '"AWS_ACCESS_KEY_ID": "<R2 access key ID>"' <<<"$out" && ok "credentials guidance: TFPLS_ENV_JSON with the Cloudflare token and the R2 keys" || bad "R2 guidance: $out"
! grep -q 'TFPLS_ROLE_ARN' <<<"$out" && ! grep -qi 'trust policy' <<<"$out" && ok "no AWS role steps for a repository without AWS" || bad "role steps shown: $out"
! grep -q '0123456789abcdef' <<<"$out" && ok "the endpoint (it carries an account id) is never printed" || bad "endpoint printed"
grep -q 'OpenTofu    1.13.1 (from required_version in versions.tf)' <<<"$out" && ok "the version from an exact required_version" || bad "version row: $out"
grep -q '/tofu apply edge-infra' <<<"$out" && ! grep -q 'tofu workspace new' <<<"$out" && ok "names /tofu apply <stack>, no workspace to create" || bad "apply hint: $out"
! grep -qE '^  ! ' <<<"$out" && ok "a clean root stack has no warnings" || bad "warnings: $out"
noop - "$r"
s1=$(snap "$t/edge-dry"); out=$(ob --dry-run "$t/edge-dry")
[ "$(snap "$t/edge-dry")" = "$s1" ] && grep -q '+  - name: edge-infra' <<<"$out" && grep -q '+name: tofu' <<<"$out" && grep -q 'nothing was written' <<<"$out" && ok "--dry-run shows the new files as diffs and writes nothing" || bad "dry run: $out"
out=$(ob --dry-run "$r"); grep -q 'nothing would change' <<<"$out" && ok "--dry-run on an onboarded repository: nothing would change" || bad "dry run up to date: $out"
out=$(ans 'n\nweb-edge\ny\n' "$t/edge-dry"); eq "answers via stdin (no to GitHub, a stack name, yes to the files)" "$(lc "$t/edge-dry/.tfpls.yml" "  - name: web-edge")" 1
grep -q 'Name of the stack (shown in comments, used by /tofu apply <name>) \[edge-infra\] web-edge' <<<"$out" && ok "the question shows its default and the answer" || bad "question: $out"
# the root module has a backend and so do other directories: the root stack ignores them, they are stacks of their own
mkdir -p "$t/mix/bootstrap"; cp "$r/versions.tf" "$t/mix/"; printf 'terraform {\n  backend "s3" {\n    region = "eu-north-1"\n    use_lockfile = true\n  }\n}\n' > "$t/mix/bootstrap/main.tf"
ob "$t/mix" >/dev/null; c="$t/mix/.tfpls.yml"
eq "root plus another root module: two stacks" "$(grep -E '^  - ' "$c" | tr '\n' '|')" "  - name: mix|  - { name: bootstrap, dir: bootstrap, workspaces: [default] }|"
eq "the root stack ignores the other root module" "$(grep -cF ', ".gitignore", "bootstrap/**"]   # changes here plan nothing' "$c")" 1
eq "that config is valid" "$(valid "$c")" "[]"

# ---- a comment-only config is a warning
mkdir "$t/n"; out=$(ob "$t/n")
grep -q '! .*only comments' <<<"$out" && ! grep -q '✔ .tfpls.yml' <<<"$out" && ok "a comment-only config is a warning, not a tick" || bad "comment-only: $out"

# ---- the push trigger names the default branch even when onboarding runs on a feature branch
git init -q -b main "$t/fb"; mkdir -p "$t/fb/workspaces/dev"; git -C "$t/fb" -c user.email=a@b -c user.name=a commit -q --allow-empty -m x; git -C "$t/fb" checkout -q -b add-pipeline
ob "$t/fb" >/dev/null; eq "a feature branch does not become the push trigger" "$(lc "$t/fb/.github/workflows/tofu.yml" "  push: { branches: [main] }")" 1
out=$(ob --force --create-workspaces "$t/fb"); rc=$?; [ $rc -eq 2 ] && grep -q 'unknown option: --create-workspaces' <<<"$out" && ok "--create-workspaces is rejected" || bad "flag: rc=$rc $out"
out=$(ob --force "$t/v"); grep -q 'tofu workspace new' <<<"$out" && ! grep -q -e '--create-workspaces' <<<"$out" && ok "the hint for missing workspaces names tofu workspace new" || bad "hint"

# ---- a hostile remote is never echoed, names are validated
mkdir -p "$t/r/workspaces/dev"; git init -q "$t/r"; git -C "$t/r" remote add origin 'https://github.com/x/$(touch pwned);y'
out=$(ob "$t/r"); ! grep -q 'pwned' <<<"$out" && grep -q 'OWNER/REPO' <<<"$out" && ok "an odd remote falls back to the placeholder" || bad "remote: $out"
mkdir -p "$t/w/workspaces/ok" "$t/w/workspaces/b ad"; out=$(ob "$t/w"); grep -q 'workspace(s) in workspaces/: ok' <<<"$out" && grep -q 'is skipped' <<<"$out" && ok "odd workspace names are skipped" || bad "names: $out"

# ---- the runner, and a differing workflow: shown as a diff, replaced only with --force or a yes
ob --force --runs-on ubuntu-slim "$t/a" >/dev/null; eq "--runs-on changes the runner" "$(lc "$t/a/.github/workflows/tofu.yml" "    with: { runs_on: ubuntu-slim }")" 1
grep -q $'\033' <<<"$out" && bad "colors in piped output" || ok "no colors when the output is not a terminal"
before=$(cat "$t/a/.github/workflows/tofu.yml"); out=$(ob "$t/a"); rc=$?
[ $rc -eq 0 ] && [ "$(cat "$t/a/.github/workflows/tofu.yml")" = "$before" ] && ok "a differing workflow is not overwritten without a yes" || bad "overwritten: rc=$rc"
grep -q -- '-    with: { runs_on: ubuntu-slim }' <<<"$out" && grep -q -e '--force' <<<"$out" && grep -q '.tfpls.yml is never overwritten' <<<"$out" && ok "the difference is shown as a diff, with the way to replace it" || bad "diff: $out"
out=$(ans 'n\n' "$t/a"); [ "$(cat "$t/a/.github/workflows/tofu.yml")" = "$before" ] && grep -q 'Replace .github/workflows/tofu.yml with the recommended caller? \[y/N\] n' <<<"$out" && ok "answered no: kept" || bad "no: $out"
out=$(ans 'y\n' "$t/a"); eq "answered yes: replaced" "$(grep -c '^    with:' "$t/a/.github/workflows/tofu.yml" | sed 's/^0$/none/')" none
ob --force --runs-on '["self-hosted","linux"]' "$t/a" >/dev/null; eq "a JSON array of labels is passed as a string" "$(lc "$t/a/.github/workflows/tofu.yml" "    with: { runs_on: '[\"self-hosted\",\"linux\"]' }")" 1
if command -v actionlint >/dev/null; then actionlint "$t/a/.github/workflows/tofu.yml" && ok "the labels array passes actionlint" || bad "labels array: actionlint"; fi
ob --runs-on 'x y' "$t/a" >/dev/null; eq "an odd runner label is a usage error" "$?" 2
col=$(FORCE_COLOR=1 "$init" --force "$t/a" 2>&1 </dev/null); grep -q $'\033\[' <<<"$col" && ok "FORCE_COLOR=1 gives colors" || bad "FORCE_COLOR"
col=$(FORCE_COLOR=1 NO_COLOR=1 "$init" --force "$t/a" 2>&1 </dev/null); grep -q $'\033' <<<"$col" && bad "NO_COLOR ignored" || ok "NO_COLOR wins"
ob --force "$t/a" >/dev/null; eq "--force overwrites" "$?" 0
eq "--ref pins" "$(ob --force --ref v0.2.3 "$t/a" >/dev/null; grep '^    uses: ' "$t/a/.github/workflows/tofu.yml" | sed 's/.*@//')" v0.2.3
printf 'overrides: {}\n' > "$t/a/.tfpls.yml"; out=$(ob --force "$t/a")
eq "keeps an existing config, even with --force" "$(cat "$t/a/.tfpls.yml")" "overrides: {}"
grep -q "left as it is" <<<"$out" && ok "says the existing config is left alone" || bad "existing config note"

# ---- answers via stdin, --yes, and a real terminal
mkdir -p "$t/q/workspaces/dev"; out=$(ans 'n\n' "$t/q")
[ ! -e "$t/q/.github/workflows/tofu.yml" ] && grep -q 'Does that look right? Write the new file(s)? \[Y/n\] n' <<<"$out" && ok "answered no: nothing is written" || bad "no to the files: $out"
out=$(ans 'n\n' --yes "$t/q"); [ -f "$t/q/.github/workflows/tofu.yml" ] && ! grep -qE '\[Y/n\]|\[y/N\]' <<<"$out" && ok "--yes asks nothing and takes the defaults" || bad "--yes: $out"

# ---- an existing tfpls caller under another name is updated, never doubled; another pipeline is asked about, never deleted
mkdir -p "$t/dup/workspaces/dev" "$t/dup/.github/workflows"; ob "$t/dup" >/dev/null; mv "$t/dup/.github/workflows/tofu.yml" "$t/dup/.github/workflows/infra.yml"
out=$(ob "$t/dup"); [ ! -e "$t/dup/.github/workflows/tofu.yml" ] && grep -q 'infra.yml.*already up to date' <<<"$out" && ok "an existing tfpls caller under another name: no second caller" || bad "duplicate caller: $out"
printf 'name: deploy\non: push\njobs:\n  d:\n    runs-on: ubuntu-latest\n    steps:\n      - run: tofu apply -auto-approve\n' > "$t/dup/.github/workflows/deploy.yml"
out=$(ans 'y\n' "$t/dup"); [ -f "$t/dup/.github/workflows/deploy.yml" ] && grep -q 'deploy.yml looks like another tofu/terraform pipeline that applies' <<<"$out" && grep -q 'git rm .github/workflows/deploy.yml' <<<"$out" && ok "another pipeline that applies: asked about, never deleted" || bad "other pipeline: $out"

# ---- versions, CODEOWNERS, a clean repository
git init -q "$t/c"; mkdir -p "$t/c/workspaces/dev"; printf 'terraform {\n  backend "s3" {\n    region = "eu-north-1"\n    use_lockfile = true\n  }\n}\n' > "$t/c/main.tf"; echo 1.11.4 > "$t/c/.opentofu-version"
mkdir -p "$t/c/.github"; printf '* @acme/infra\n' > "$t/c/.github/CODEOWNERS"
out=$(ob "$t/c")
! grep -qE '^  ! ' <<<"$out" && grep -q 'OpenTofu    1.11.4 (from .opentofu-version)' <<<"$out" && ok "a root module with an S3 backend, native locking and a pinned version has no warnings" || bad "clean repo: $out"
grep -q 'CODEOWNERS  .github/CODEOWNERS' <<<"$out" && ok "CODEOWNERS found" || bad "codeowners: $out"
mkdir -p "$t/d"; printf 'terraform {\n  backend "s3" {}\n}\n' > "$t/d/main.tf"
out=$(ob "$t/d"); grep -q 'no use_lockfile' <<<"$out" && ok "warns about a missing use_lockfile" || bad "lockfile warning"
mkdir -p "$t/rg"; printf 'terraform {\n  required_version = ">= 1.6"\n  backend "s3" {\n    region = "eu-north-1"\n    use_lockfile = true\n  }\n}\n' > "$t/rg/main.tf"
out=$(ob "$t/rg"); grep -q 'required_version is a range (>= 1.6), not pinned' <<<"$out" && ok "a version range is not a pin" || bad "range: $out"
echo 1.5.7 > "$t/rg/.terraform-version"; out=$(ob "$t/rg"); grep -q 'that is a Terraform version' <<<"$out" && ok "a Terraform version is called out" || bad "terraform version: $out"
for e in dev prod; do printf 'terraform {\n  required_version = "1.11.4"\n}\n' > "$t/ev/envs/$e/versions.tf"; done
out=$(ob "$t/ev"); grep -q 'add a .opentofu-version file with 1.11.4' <<<"$out" && ok "a version pinned only below the root: says the pipeline reads the root" || bad "deep version: $out"
eq "the default version is the one of tfpls setup (internal/setup)" "$(sed -nE 's/^[[:space:]]*DefaultTofu[[:space:]]*=[[:space:]]*"([0-9.]+)".*/\1/p' "$here/internal/setup/setup.go")" "$(sed -nE 's/^const TofuDefault = "([0-9.]+)".*/\1/p' "$here"/internal/onboard/onboard.go)"

# ---- no workspaces folder, and an empty one
mkdir "$t/b"; out=$(ob "$t/b"); eq "no workspaces/ folder is fine" "$?" 0
grep -q 'no layout found' <<<"$out" && grep -q 'stacks:' "$t/b/.tfpls.yml" && ok "explains the stacks form" || bad "hint without a layout"
grep -q 'no .tf files here' <<<"$out" && ok "warns when run outside a root module" || bad "no .tf warning"
grep -q 'not pinned' <<<"$out" && ok "says the version is not pinned" || bad "version note"
mkdir -p "$t/e/workspaces"; ob "$t/e" >/dev/null; eq "an empty workspaces/ folder is fine" "$?" 0

# ---- checks: unknown flags, default branch, not a git repo, another workflow
ob --nope "$t/a" >/dev/null; eq "an unknown option is a usage error" "$?" 2
out=$(ob --force "$t/a"); grep -q 'not a git repository' <<<"$out" && ok "warns when not a git repository" || bad "git warning"
eq "main without git" "$(lc "$t/a/.github/workflows/tofu.yml" "  push: { branches: [main] }")" 1
git init -q -b trunk "$t/g"; mkdir -p "$t/g/workspaces/dev"; out=$(ob "$t/g")
eq "the default branch is read from git" "$(lc "$t/g/.github/workflows/tofu.yml" "  push: { branches: [trunk] }")" 1
! grep -q 'not a git repository' <<<"$out" && ok "no git warning inside a repository" || bad "git warning in repo"
mkdir -p "$t/h/.github/workflows" "$t/h/workspaces/dev"; printf 'name: terraform\non: push\n' > "$t/h/.github/workflows/terraform.yml"
out=$(ob "$t/h"); grep -q 'terraform.yml looks like another' <<<"$out" && ok "warns about another tofu/terraform workflow" || bad "other workflow warning"
mkdir "$t/i"; out=$(ob "$t/i"); grep -q 'describe your layout' <<<"$out" && ok "loud note when no layout is found" || bad "no layout note"
grep -q 'cannot approve your own' <<<"$out" && grep -q -e '--now' <<<"$out" && ok "prints the self-approval note with the --now way out" || bad "notes"

# ---- laptop habits that break CI
mkdir -p "$t/pr/workspaces/dev"; printf 'terraform {\n  backend "s3" {\n    bucket = "b"\n    profile = "me"\n    use_lockfile = true\n  }\n}\n' > "$t/pr/main.tf"
out=$(ob "$t/pr"); grep -q 'profile' <<<"$out" && grep -q 'no region' <<<"$out" && ok "warns about a profile and a missing region" || bad "profile/region: $out"

# ---- a non-writable target fails with a clear message and changes nothing
if [ "$(id -u)" != 0 ]; then
  mkdir -p "$t/ro/workspaces/dev"; chmod 555 "$t/ro"; out=$(ob "$t/ro"); rc=$?
  [ $rc -eq 1 ] && grep -q 'cannot write .github/workflows/tofu.yml: the directory is not writable' <<<"$out" && [ ! -e "$t/ro/.github" ] && ok "a non-writable target fails with a clear message" || bad "read-only: rc=$rc $out"
  chmod 755 "$t/ro"
fi

# ---- jokes: at least 20, deterministic with a seed, --no-jokes
mkdir "$t/j"; js=$(for s in $(seq 0 19); do TFPLS_JOKE_SEED=$s "$init" --dry-run "$t/j" </dev/null 2>&1 | grep '💬'; done | sort -u | wc -l | tr -d ' ')
eq "20 seeds, 20 different jokes" "$js" 20
eq "the same seed, the same joke" "$(TFPLS_JOKE_SEED=7 "$init" --dry-run "$t/j" </dev/null | grep '💬')" "$(TFPLS_JOKE_SEED=7 "$init" --dry-run "$t/j" </dev/null | grep '💬')"
out=$(ob --no-jokes --dry-run "$t/j"); ! grep -q '💬' <<<"$out" && ok "--no-jokes" || bad "--no-jokes"

# ---- offline by default: nothing above called gh
eq "no gh call without a yes" "$(wc -l < "$t/gh.log" | tr -d ' ')" 0

# ---- the optional GitHub step (gh stub): IDs for the trust policy, the variable and the secrets, a key that is never shown
mkdir -p "$t/gh/workspaces/dev"; git init -q -b main "$t/gh"; git -C "$t/gh" remote add origin git@github.com:acme/app.git
out=$(ans 'n\n' "$t/gh"); eq "answered no to the GitHub step: gh is never called" "$(wc -l < "$t/gh.log" | tr -d ' ')" 0
rm -f "$t/gh/.github/workflows/tofu.yml"
out=$(STUB_VARS='OTHER\tx\t2026-01-01\n' STUB_SECRETS='SLACK_WEBHOOK\t2026-01-01\n' ans 'y\ny\ny\narn:aws:iam::123456789012:role/tfpls\n' "$t/gh")
grep -q '"repo:acme/app:\*"  and  "repo:acme@1234/app@5678:\*"' <<<"$out" && grep -q 'IDs         owner 1234, repository 5678' <<<"$out" && ok "the trust policy uses the numeric IDs read with gh" || bad "ids: $out"
key=$(cat "$t/secret.PLAN_ENCRYPTION_KEY" 2>/dev/null)
[ "$(printf '%s' "$key" | tr -d '\n' | wc -c | tr -d ' ')" = 44 ] && ! grep -qF "$key" <<<"$out" && grep -q 'PLAN_ENCRYPTION_KEY set (generated here, never shown)' <<<"$out" && ok "the plan key is generated, set through stdin and never printed" || bad "key: $out"
eq "the role ARN is set as the variable" "$(cat "$t/variable.TFPLS_ROLE_ARN" 2>/dev/null)" "arn:aws:iam::123456789012:role/tfpls"
grep -q '✔ PLAN_ENCRYPTION_KEY is set on GitHub' <<<"$out" && grep -q '✔ TFPLS_ROLE_ARN is set on GitHub' <<<"$out" && ok "the checklist counts them as done" || bad "online checklist: $out"
: > "$t/gh.log"; rm -f "$t/secret.PLAN_ENCRYPTION_KEY"
out=$(STUB_SECRETS='PLAN_ENCRYPTION_KEY\t2026-01-01\n' ans 'y\n' "$t/gh"); [ ! -e "$t/secret.PLAN_ENCRYPTION_KEY" ] && grep -q 'PLAN_ENCRYPTION_KEY is set' <<<"$out" && ok "an existing key is never replaced" || bad "existing key: $out"
: > "$t/gh.log"; out=$(STUB_AUTH=no ans 'y\n' "$t/gh"); grep -q 'gh is not logged in' <<<"$out" && ! grep -q '^api' "$t/gh.log" && ok "gh not logged in: skipped" || bad "auth: $out"

done_tests
