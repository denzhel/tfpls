#!/usr/bin/env bash
# tools/onboard: files written, workspaces listed in the config, no overwrite without --force, colors only on request,
# generated files pass actionlint and the config validator.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
init="$here/tools/onboard"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT

mkdir -p "$t/a/workspaces/dev-eu-north-1" "$t/a/workspaces/stg-eu-north-1" "$t/a/workspaces/prod-eu-north-1"
out=$("$init" "$t/a"); eq "exit 0 on a fresh repo" "$?" 0
[ -f "$t/a/.github/workflows/tofu.yml" ] && ok "workflow written" || bad "workflow written"
[ -e "$t/a/.tfpls.yml" ] && bad "workspaces/* needs no config file" || ok "workspaces/* layout: no config file"
grep -q "3 workspace(s) in workspaces/: dev-eu-north-1 prod-eu-north-1 stg-eu-north-1" <<<"$out" && ok "workspaces detected and shown" || bad "workspaces detected: $out"
grep -q "gh variable set TFPLS_ROLE_ARN" <<<"$out" && grep -q "PLAN_ENCRYPTION_KEY" <<<"$out" && ok "checklist printed" || bad "checklist printed"
eq "output fits one screen" "$([ "$(wc -l <<<"$out")" -le 30 ] && echo yes || echo no)" yes
eq "uses the entry workflow at @v0" "$(yq -r .jobs.tofu.uses "$t/a/.github/workflows/tofu.yml")" "denzhel/tfpls/.github/workflows/tofu.yml@v0"
eq "secrets inherited" "$(yq -r .jobs.tofu.secrets "$t/a/.github/workflows/tofu.yml")" inherit
eq "no with: block for the default runner" "$(yq -r '.jobs.tofu.with // "none"' "$t/a/.github/workflows/tofu.yml")" none
if command -v actionlint >/dev/null; then actionlint "$t/a/.github/workflows/tofu.yml" && ok "generated workflow passes actionlint" || bad "generated workflow passes actionlint"; fi

valid() { jq -c -f "$here/actions/detect-workspaces/validate.jq" <<<"$(yq -o=json '.' "$1")"; }

# var-file layout
mkdir -p "$t/v/vars"; printf 'terraform {\n  backend "s3" {}\n}\n' > "$t/v/main.tf"; touch "$t/v/vars/dev.tfvars" "$t/v/vars/prod.tfvars" "$t/v/vars/terraform.tfvars"
git init -q "$t/v"; git -C "$t/v" remote add origin https://github.com/al-ice/my.repo.git
out=$("$init" "$t/v"); c="$t/v/.tfpls.yml"
eq "var files become workspaces of one stack" "$(yq -r '.stacks[0].workspaces | join(",")' "$c")" "dev,prod"
eq "var_file template" "$(yq -r '.stacks[0].var_file' "$c")" 'vars/{workspace}.tfvars'
eq "a var file selects only its workspace" "$(yq -r '.stacks[0].workspace_paths[0]' "$c")" 'vars/{workspace}.tfvars'
eq "the var-file config is valid" "$(valid "$c")" "[]"
eq "trust policy shows both subject forms from the remote" "$(grep -c -e '"repo:al-ice/my.repo:\*"' -e '"repo:al-ice@\*/my.repo@\*:\*"' <<<"$out")" 1
! grep -q 'auto_apply: never' "$c" && ok "no per-workspace no-op lines" || bad "no-op lines"

# separate root modules with a backend, modules watched
mkdir -p "$t/ev/envs/dev" "$t/ev/envs/prod" "$t/ev/modules/net"
for e in dev prod; do printf 'terraform {\n  backend "s3" {}\n}\nmodule "n" {\n  source = "../../modules/net"\n}\nmodule "x" {\n  source = "../../../outside"\n}\n' > "$t/ev/envs/$e/main.tf"; done
"$init" "$t/ev" >/dev/null; c="$t/ev/.tfpls.yml"
eq "each backend directory is a stack on default" "$(yq -r '[.stacks[] | .name + ":" + .dir + ":" + (.workspaces | join(","))] | join(" ")' "$c")" "envs-dev:envs/dev:default envs-prod:envs/prod:default"
eq "watch comes from the relative module sources" "$(yq -r '.stacks[0].watch | join(",")' "$c")" "modules/net"
eq "the envs config is valid" "$(valid "$c")" "[]"

# a comment-only config is a warning
mkdir "$t/n"; out=$("$init" "$t/n")
grep -q '! .*only comments' <<<"$out" && ! grep -q '✔ .tfpls.yml' <<<"$out" && ok "a comment-only config is a warning, not a tick" || bad "comment-only: $out"

# the push trigger names the default branch even when onboarding runs on a feature branch
git init -q -b main "$t/fb"; mkdir -p "$t/fb/workspaces/dev"; git -C "$t/fb" -c user.email=a@b -c user.name=a commit -q --allow-empty -m x; git -C "$t/fb" checkout -q -b add-pipeline
"$init" "$t/fb" >/dev/null; eq "a feature branch does not become the push trigger" "$(yq -r '.on.push.branches[0]' "$t/fb/.github/workflows/tofu.yml")" main
# --create-workspaces
out=$("$init" --force --create-workspaces "$t/fb"); eq "--create-workspaces writes the first-run setting" "$(yq -r .defaults.create_workspace "$t/fb/.tfpls.yml")" true
eq "that config is valid" "$(valid "$t/fb/.tfpls.yml")" "[]"
out=$("$init" --force "$t/v"); grep -q -e '--create-workspaces' <<<"$out" && ok "the first-run hint for named workspaces" || bad "hint"

# a hostile remote is never echoed, names are validated
mkdir -p "$t/r/workspaces/dev"; git init -q "$t/r"; git -C "$t/r" remote add origin 'https://github.com/x/$(touch pwned);y'
out=$("$init" "$t/r"); ! grep -q 'pwned' <<<"$out" && grep -q 'OWNER/REPO' <<<"$out" && ok "an odd remote falls back to the placeholder" || bad "remote: $out"
mkdir -p "$t/w/workspaces/ok" "$t/w/workspaces/b ad"; out=$("$init" "$t/w"); grep -q 'workspace(s) in workspaces/: ok' <<<"$out" && grep -q 'is skipped' <<<"$out" && ok "odd workspace names are skipped" || bad "names: $out"

# the runner
"$init" --force --runs-on ubuntu-slim "$t/a" >/dev/null; eq "--runs-on changes the runner" "$(yq -r .jobs.tofu.with.runs_on "$t/a/.github/workflows/tofu.yml")" ubuntu-slim

# no colors when piped, colors on request
grep -q $'\033' <<<"$out" && bad "colors in piped output" || ok "no colors when the output is not a terminal"
col=$(FORCE_COLOR=1 "$init" --force "$t/a"); grep -q $'\033\[' <<<"$col" && ok "FORCE_COLOR=1 gives colors" || bad "FORCE_COLOR"
col=$(FORCE_COLOR=1 NO_COLOR=1 "$init" --force "$t/a"); grep -q $'\033' <<<"$col" && bad "NO_COLOR ignored" || ok "NO_COLOR wins"

"$init" "$t/a" >/dev/null 2>&1; eq "refuses to overwrite" "$?" 1
"$init" --force "$t/a" >/dev/null 2>&1; eq "--force overwrites" "$?" 0
eq "--ref pins" "$("$init" --force --ref v0.2.3 "$t/a" >/dev/null; yq -r .jobs.tofu.uses "$t/a/.github/workflows/tofu.yml" | sed 's/.*@//')" v0.2.3

printf 'overrides: {}\n' > "$t/a/.tfpls.yml"; out=$("$init" --force "$t/a")
eq "keeps an existing config" "$(cat "$t/a/.tfpls.yml")" "overrides: {}"
grep -q "already exists: left as it is" <<<"$out" && ok "says the existing config is left alone" || bad "existing config note"

git init -q "$t/c"; mkdir -p "$t/c/workspaces/dev"; printf 'terraform {\n  backend "s3" {\n    region = "eu-north-1"\n    use_lockfile = true\n  }\n}\n' > "$t/c/main.tf"; echo 1.11.4 > "$t/c/.opentofu-version"
out=$("$init" "$t/c")
! grep -q '!' <<<"$out" && grep -q 'OpenTofu version from .opentofu-version (1.11.4)' <<<"$out" && ok "a root module with an S3 backend, native locking and a pinned version has no warnings" || bad "clean repo: $out"
mkdir -p "$t/d"; printf 'terraform {\n  backend "s3" {}\n}\n' > "$t/d/main.tf"
out=$("$init" "$t/d"); grep -q 'no use_lockfile' <<<"$out" && ok "warns about a missing use_lockfile" || bad "lockfile warning"

# no workspaces folder, and an empty one
mkdir "$t/b"; out=$("$init" "$t/b"); eq "no workspaces/ folder is fine" "$?" 0
grep -q 'no layout found' <<<"$out" && grep -q 'stacks:' "$t/b/.tfpls.yml" && ok "explains the stacks form" || bad "hint without a layout"
grep -q 'no .tf files here' <<<"$out" && ok "warns when run outside a root module" || bad "no .tf warning"
grep -q 'not pinned' <<<"$out" && ok "says the version is not pinned" || bad "version note"
mkdir -p "$t/e/workspaces"; "$init" "$t/e" >/dev/null 2>&1; eq "an empty workspaces/ folder is fine" "$?" 0

# checks: unknown flags, default branch, not a git repo, another workflow
"$init" --nope "$t/a" >/dev/null 2>&1; eq "an unknown option is a usage error" "$?" 2
out=$("$init" --force "$t/a"); grep -q 'not a git repository' <<<"$out" && ok "warns when not a git repository" || bad "git warning"
eq "main without git" "$(yq -r '.on.push.branches[0]' "$t/a/.github/workflows/tofu.yml")" main
git init -q -b trunk "$t/g"; mkdir -p "$t/g/workspaces/dev"; out=$("$init" "$t/g")
eq "the default branch is read from git" "$(yq -r '.on.push.branches[0]' "$t/g/.github/workflows/tofu.yml")" trunk
! grep -q 'not a git repository' <<<"$out" && ok "no git warning inside a repository" || bad "git warning in repo"
mkdir -p "$t/h/.github/workflows" "$t/h/workspaces/dev"; printf 'name: terraform\non: push\n' > "$t/h/.github/workflows/terraform.yml"
out=$("$init" "$t/h"); grep -q 'terraform.yml looks like another' <<<"$out" && ok "warns about another tofu/terraform workflow" || bad "other workflow warning"
err=$("$init" "$t/h" 2>&1 >/dev/null); grep -q '.tfpls.yml' <<<"$err" && ok "the exists message names the config" || bad "exists message"
mkdir "$t/i"; out=$("$init" "$t/i"); grep -q 'describe your layout' <<<"$out" && ok "loud note when no layout is found" || bad "no layout note"
grep -q 'cannot approve your own' <<<"$out" && grep -q -e '--now' <<<"$out" && ok "prints the self-approval note with the --now way out" || bad "notes"

# laptop habits that break CI
mkdir -p "$t/pr/workspaces/dev"; printf 'terraform {\n  backend "s3" {\n    bucket = "b"\n    profile = "me"\n    use_lockfile = true\n  }\n}\n' > "$t/pr/main.tf"
out=$("$init" "$t/pr"); grep -q 'profile' <<<"$out" && grep -q 'no region' <<<"$out" && ok "warns about a profile and a missing region" || bad "profile/region: $out"

done_tests
