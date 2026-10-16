#!/usr/bin/env bash
# Reviewed-plan store: pack/unpack a saved tofu plan, name its artifact, find it, validate it.
# Sourced by run.sh, the tofu action steps, tools/tfpls-plan and tests/plan-store.test.sh.
# Functions never `exit`; they return non-zero and set TFPLS_ERR with a human-readable reason.
#
# Format (plan.tgz.enc, when PLAN_ENCRYPTION_KEY is set):
#   AES-256-CBC (PBKDF2, salted) over:  <64 hex sha256 of tgz>\n<tgz bytes>
#   The inner digest detects tampering/corruption (and a wrong key, which yields garbage).
# Format (plan.tgz, no key): the tgz as is.  tgz = { tfplan, meta.json }.
# The key is only ever passed through the environment, never on a command line.

# shellcheck disable=SC2034  # TFPLS_ERR and TFPLS_OUT are read by the scripts that source this file
TFPLS_ERR=""
TFPLS_OUT=""   # last result of tfpls_pack / tfpls_lookup / tfpls_fetch (use instead of $(...) so TFPLS_ERR survives)

tfpls_slug() { printf '%s' "$1" | sed 's#/#__#g; s#[^A-Za-z0-9._-]#_#g'; }

tfpls_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi
}

# first 8 hex of sha256 over the sorted, space-joined targets; "all" when none
tfpls_targets_hash() {
  local t
  t=$(printf '%s\n' $1 | LC_ALL=C sort | tr '\n' ' ' | sed 's/ *$//')
  if [ -z "$t" ]; then echo all; else printf '%s' "$t" | tfpls_sha256 | cut -c1-8; fi
}

# tfpls_plan_name <workspace name> <head sha> <targets>
tfpls_plan_name() { echo "tfpls-plan-$(tfpls_slug "$1")-$(printf '%s' "$2" | cut -c1-12)-$(tfpls_targets_hash "$3")"; }

# tfpls_pack <dir containing tfplan> <outdir> <meta.json path>  -> echoes the produced file path
tfpls_pack() {
  local dir=$1 out=$2 meta=$3 tmp tgz
  [ -f "$dir/tfplan" ] || { TFPLS_ERR="no tfplan in $dir"; return 1; }
  mkdir -p "$out"; tmp=$(mktemp -d)
  cp "$dir/tfplan" "$tmp/tfplan"; cp "$meta" "$tmp/meta.json"
  tgz="$tmp/plan.tgz"
  tar -C "$tmp" -czf "$tgz" tfplan meta.json || { TFPLS_ERR="tar failed"; rm -rf "$tmp"; return 1; }
  if [ -n "${PLAN_ENCRYPTION_KEY:-}" ]; then
    { tfpls_sha256 < "$tgz"; cat "$tgz"; } \
      | openssl enc -aes-256-cbc -pbkdf2 -md sha256 -salt -pass env:PLAN_ENCRYPTION_KEY -out "$out/plan.tgz.enc" \
      || { TFPLS_ERR="encryption failed"; rm -rf "$tmp"; return 1; }
    TFPLS_OUT="$out/plan.tgz.enc"
  else
    cp "$tgz" "$out/plan.tgz"; TFPLS_OUT="$out/plan.tgz"
  fi
  echo "$TFPLS_OUT"
  rm -rf "$tmp"
}

# tfpls_unpack <file> <outdir>   extracts tfplan + meta.json into outdir
tfpls_unpack() {
  local file=$1 out=$2 tmp
  mkdir -p "$out"; tmp=$(mktemp -d)
  case "$file" in
    *.enc)
      [ -n "${PLAN_ENCRYPTION_KEY:-}" ] || { TFPLS_ERR="the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set"; rm -rf "$tmp"; return 1; }
      openssl enc -d -aes-256-cbc -pbkdf2 -md sha256 -pass env:PLAN_ENCRYPTION_KEY -in "$file" -out "$tmp/dec" 2>/dev/null \
        || { TFPLS_ERR="could not decrypt the saved plan (wrong key or corrupted file)"; rm -rf "$tmp"; return 1; }
      local want have
      want=$(head -c 64 "$tmp/dec")
      tail -c +66 "$tmp/dec" > "$tmp/plan.tgz"
      have=$(tfpls_sha256 < "$tmp/plan.tgz")
      [ "$want" = "$have" ] || { TFPLS_ERR="the saved plan failed its integrity check (wrong key or tampered)"; rm -rf "$tmp"; return 1; }
      ;;
    *.tgz)
      [ -z "${PLAN_ENCRYPTION_KEY:-}" ] || { TFPLS_ERR="the saved plan is NOT encrypted but PLAN_ENCRYPTION_KEY is set; refusing a possible downgrade (run /tofu plan again)"; rm -rf "$tmp"; return 1; }
      cp "$file" "$tmp/plan.tgz"
      ;;
    *) TFPLS_ERR="unknown plan file type: $file"; rm -rf "$tmp"; return 1;;
  esac
  tar -C "$out" -xzf "$tmp/plan.tgz" tfplan meta.json 2>/dev/null \
    || { TFPLS_ERR="the saved plan archive is invalid"; rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
}

# tfpls_check_meta <meta.json> <tree> <workspace name> <dir> <tofu version>
tfpls_check_meta() {
  local m=$1 tree=$2 name=$3 dir=$4 ver=$5 got
  got=$(jq -r '.tree // ""' "$m"); [ "$got" = "$tree" ] || { TFPLS_ERR="the reviewed plan was made for different code (plan tree ${got:0:12}, current tree ${tree:0:12}); run /tofu plan again"; return 1; }
  got=$(jq -r '.name // ""' "$m"); [ "$got" = "$name" ] || { TFPLS_ERR="the reviewed plan is for workspace '$got', not '$name'"; return 1; }
  got=$(jq -r '.dir // ""' "$m"); [ "$got" = "$dir" ] || { TFPLS_ERR="the reviewed plan is for directory '$got', not '$dir'"; return 1; }
  got=$(jq -r '.tofu_version // ""' "$m"); [ "$got" = "$ver" ] || { TFPLS_ERR="the reviewed plan was made with tofu $got, this run uses $ver; run /tofu plan again"; return 1; }
}

# tfpls_lookup <owner/repo> <artifact name>  -> echoes "<artifact id> <run id>" of the newest non-expired match
# Only artifacts made by workflow runs of THIS repository count (repository_id and head_repository_id both equal the repository's
# id): a fork's pull request runs its own workflow file and can upload an artifact with a predictable name, but that run has
# another head repository. The listing is paginated, so many foreign artifacts cannot push the real one out.
# A run of this repository counts only when its code is trusted for this plan: it ran on the default branch (its workflow is
# the default branch's), or on exactly the commit being applied ($HEAD_SHA, so its workflow files are the reviewed ones).
# Without this a writer could push a branch with its own workflow and upload a plan under the name of someone else's pull request.
# returns 1 when there is none (TFPLS_ERR says whether it expired or never existed), 2 when the API call failed
tfpls_lookup() {
  local repo=$1 name=$2 r list rid=${GITHUB_REPOSITORY_ID:-} db=${DEFAULT_BRANCH:-} sha=${HEAD_SHA:-}
  [ -n "$db" ] || db=$(gh api "repos/$repo" --jq .default_branch 2>/dev/null) || db=""
  [ -n "$db" ] || { TFPLS_ERR="could not tell the default branch (needed to trust only reviewed plans)"; return 2; }
  if ! [[ "$rid" =~ ^[0-9]+$ ]]; then rid=$(gh api "repos/$repo" --jq .id 2>/dev/null) || rid=""; fi
  [[ "$rid" =~ ^[0-9]+$ ]] || { TFPLS_ERR="could not tell the repository id (needed to trust only this repository's own plans)"; return 2; }
  list=$(gh api --paginate "repos/$repo/actions/artifacts?name=$name&per_page=100" | jq -s '{artifacts: [.[] | .artifacts[]?]}') \
    || { TFPLS_ERR="could not list artifacts (does the workflow grant 'actions: read'?)"; return 2; }
  list=$(jq -c --argjson rid "$rid" --arg db "$db" --arg sha "$sha" '{artifacts: [.artifacts[] | select(.workflow_run != null and .workflow_run.repository_id == $rid and .workflow_run.head_repository_id == $rid and (.workflow_run.head_branch == $db or ($sha != "" and .workflow_run.head_sha == $sha)))]}' <<<"$list") \
    || { TFPLS_ERR="could not read the artifact list"; return 2; }
  r=$(jq -r '[.artifacts[] | select(.expired|not)] | sort_by(.created_at) | reverse | .[0] | select(.) | "\(.id) \(.workflow_run.id)"' <<<"$list") \
    || { TFPLS_ERR="could not read the artifact list"; return 2; }
  if [ -z "$r" ]; then
    local exp
    exp=$(jq -r '[.artifacts[] | select(.expired)] | sort_by(.created_at) | reverse | .[0].expires_at // empty' <<<"$list" 2>/dev/null)
    if [ -n "$exp" ]; then TFPLS_ERR="the reviewed plan expired on ${exp%%T*} ($name)"; else TFPLS_ERR="no reviewed plan found ($name)"; fi
    return 1
  fi
  TFPLS_OUT="$r"; echo "$r"
}

# tfpls_fetch <owner/repo> <artifact name> <outdir>  -> echoes the downloaded plan file path
tfpls_fetch() {
  local repo=$1 name=$2 out=$3 hit rid f rc
  tfpls_lookup "$repo" "$name" >/dev/null; rc=$?
  [ "$rc" -eq 0 ] || return "$rc"
  hit=$TFPLS_OUT; rid=${hit#* }
  mkdir -p "$out"
  gh run download "$rid" --repo "$repo" -n "$name" -D "$out" >/dev/null 2>&1 \
    || { TFPLS_ERR="could not download the reviewed plan artifact ($name)"; return 2; }
  f=$(ls "$out"/plan.tgz.enc "$out"/plan.tgz 2>/dev/null | head -1)
  [ -n "$f" ] || { TFPLS_ERR="the artifact ($name) contains no plan file"; return 2; }
  TFPLS_OUT="$f"; echo "$f"
}

# tfpls_hint <reason> <label> <targets>  -> one sentence telling the user how to fix this particular refusal
tfpls_hint() {
  local reason=$1 label=$2 targets=${3:-} plan_cmd
  plan_cmd="/tofu plan $label"; [ -n "$targets" ] && plan_cmd="$plan_cmd -target=${targets// / -target=}"
  case "$reason" in
    *"is encrypted but PLAN_ENCRYPTION_KEY is not set"*)
      echo "This workflow does not receive the key. Pass the secret to the workflow that applies (\`secrets: inherit\` in the caller, or \`PLAN_ENCRYPTION_KEY: \${{ secrets.PLAN_ENCRYPTION_KEY }}\`); it must be the same key that saved the plan. See https://github.com/${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}/blob/main/docs/reviewed-plans.md.";;
    *"could not decrypt"*|*"integrity check"*)
      echo "The key here differs from the one that saved the plan, or the file was altered. Make sure the plan and apply workflows receive the same PLAN_ENCRYPTION_KEY, or run \`$plan_cmd\` again to save a new plan.";;
    *"is NOT encrypted but PLAN_ENCRYPTION_KEY is set"*)
      echo "The plan was saved without the key. Run \`$plan_cmd\` again so it is saved encrypted.";;
    *"actions: read"*)
      echo "Grant \`actions: read\` to the caller workflow's permissions so the saved plan can be found and downloaded.";;
    *"expired"*)
      echo "Saved plans are kept for 14 days. Run \`$plan_cmd\` again and review the new plan.";;
    *"different code"*)
      echo "The branch changed or is behind its base since the plan. Update the branch, then run \`$plan_cmd\` again.";;
    *"tofu"*"this run uses"*)
      echo "The OpenTofu version changed since the plan was made. Run \`$plan_cmd\` again.";;
    *)
      echo "Run \`$plan_cmd\` (or push a commit so the PR plan runs) and review it, then apply.";;
  esac
}
