#!/usr/bin/env bash
# Plan (and optionally apply) a single workspace.
#
# Plan modes (PLAN_MODE, only relevant when an apply may happen):
#   fresh          plan now, apply that plan (PR plans, drift, auto_apply=never)
#   reviewed       apply the plan saved from the PR when it is valid; otherwise re-plan (merge-time default)
#   reviewed_only  apply ONLY a valid saved plan, never re-plan (/tofu apply, require_reviewed_plan: true)
# A plan is "reviewed" when it was produced by plan.yml or /tofu plan for this PR head SHA + targets and still
# matches the code being applied. It is always applied as the saved file, never re-planned.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=plan-store.sh
. "$here/plan-store.sh"
# shellcheck source=format.sh
. "$here/format.sh"
# shellcheck source=post.sh
. "$here/post.sh"
# shellcheck source=entry.sh
. "$here/entry.sh"

# step outputs and annotations are ONE line: text that comes from a saved plan (its meta.json) must not add outputs or workflow commands
# code of the pull request runs inside these commands (providers, external data sources, modules, policies): they get neither the
# GitHub token nor the plan key. (The cloud credentials stay: tofu needs them; the trust policy of the role is the control there.)
tofu() { env -u GH_TOKEN -u GITHUB_TOKEN -u PLAN_ENCRYPTION_KEY tofu "$@"; }
conftest() { env -u GH_TOKEN -u GITHUB_TOKEN -u PLAN_ENCRYPTION_KEY conftest "$@"; }
oneline() { printf '%s' "$1" | tr '\r\n' '  '; }
out() { echo "$1=$(oneline "$2")" >> "$GITHUB_OUTPUT"; }
# a multi-line step output (the plan text); the delimiter is random, so no line of the value can end the output early
outml() { local d; d="TFPLS_$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"; { echo "$1<<$d"; printf '%s\n' "$2"; echo "$d"; } >> "$GITHUB_OUTPUT"; }
# the readable plan, clipped like a plan comment, for the callers that show it (the drift issue). It is an output, not a file:
# self-hosted runners keep their workspace, and cleanup() removes everything that can hold plan text.
plan_text_out() {
  case "$status" in planned|skipped|policy-failed) ;; *) return 0;; esac
  [ -s "$REPO_ROOT/$DIR/show.txt" ] || return 0
  outml plan_text "$(tfpls_clip_plan "$REPO_ROOT/$DIR/show.txt" 45000)"
}

tfpls_load_entry
AUTO_APPLY="${AUTO_APPLY:-never}"
status=error; additive=false; summary=""; body_extra=""; counts=""; destroy_banner=""; changes="[]"; changes_more=0
reviewed_used=false; plan_note=""
# set when this run is the re-plan fallback after a stale reviewed plan (see the apply step)
if [ -n "${PLAN_FALLBACK_NOTE:-}" ]; then body_extra="⚠️ ${PLAN_FALLBACK_NOTE}"; plan_note="**${NAME}**: ${PLAN_FALLBACK_NOTE}"; fi
STEP_SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"

# Self-hosted runners keep their workspace between jobs: remove everything that can hold secret values (the saved
# plan, its JSON, plan/apply output) and the provider directory when this script ends, however it ends.
cleanup() {
  if [ -n "${REPO_ROOT:-}" ] && [ -n "${DIR:-}" ] && [ -d "$REPO_ROOT/$DIR" ]; then
    ( cd "$REPO_ROOT/$DIR" && rm -rf tfplan plan.json plan.txt show.txt apply.txt apply_view.txt init.txt policy.txt .terraform )
  fi
}
trap cleanup EXIT

# which credentials this job runs with (set by the env action): env | oidc | none
echo "credentials: ${TFPLS_CREDENTIALS:-unknown}"

comment() {
  [ -z "${PR:-}" ] && return 0
  local marker="<!-- tfpls:${NAME} -->" icon
  case "$status" in
    applied) icon="✅";; no-changes) icon="🟢";; planned) icon="📝";;
    skipped) icon="⏭️";; policy-failed) icon="🛑";; *) icon="❌";;
  esac
  local body limit=45000 shown="$REPO_ROOT/$DIR/plan.txt"
  # the readable plan (show.txt) for a plan that was made; the raw output (errors, apply failures) otherwise
  case "$status" in planned|applied|policy-failed) [ -s "$REPO_ROOT/$DIR/show.txt" ] && shown="$REPO_ROOT/$DIR/show.txt";; esac
  # a successful apply shows what the apply did (not the plan again)
  [ "$status" = applied ] && [ -s "$REPO_ROOT/$DIR/apply_view.txt" ] && shown="$REPO_ROOT/$DIR/apply_view.txt"
  while :; do
    body=$(tfpls_comment_body "$marker" "$icon" "$status" "${LABEL:-$NAME}" "$RUN_URL" "$shown" "$summary" "${destroy_banner:+$destroy_banner

}$body_extra" "$limit")
    # GitHub rejects comments over 65536 characters: shrink the plan until the whole comment fits
    if [ "$(printf '%s' "$body" | wc -c | tr -d ' ')" -le 62000 ] || [ "$limit" -le 4000 ]; then break; fi
    limit=$((limit / 2))
  done
  tfpls_post_comment "$marker" "$body"   # a new comment per run; the bot's older ones for this workspace are marked superseded
}

finish() {
  out status "$status"; out summary "$summary"; out reviewed "$reviewed_used"; out counts "$counts"; out changes "$changes"; out changes_more "$changes_more"; out duration "$SECONDS"
  [ -n "$plan_note" ] && echo "$plan_note" >> "$STEP_SUMMARY"
  plan_text_out
  comment
  case "$status" in error|policy-failed) exit 1;; esac
  exit 0
}

# ---- validate targets (prevents flag injection from PR comments)
# POSIX bracket: "]" must come first; no backslash escapes inside the set
target_re='^[][A-Za-z0-9_."'"'"'-]+$'
target_args=()
set -f   # target values are never glob-expanded against the runner directory
for t in $TARGETS; do
  if [[ ! "$t" =~ $target_re ]]; then
    summary="Invalid -target value: \`$t\`"; finish
  fi
  target_args+=("-target=$t")
done

cd "$REPO_ROOT/$DIR" || { summary="dir not found: $DIR"; finish; }

tofu init -input=false -no-color > init.txt 2>&1 || {
  cat init.txt; cp init.txt plan.txt; summary="init failed"
  why=$("$here/failure.sh" init.txt "${LABEL:-$NAME}" 2>/dev/null) && [ -n "$why" ] && body_extra="${body_extra:+$body_extra
}$why"
  finish
}
if [ -n "$WS" ]; then
  if ! tofu workspace select "$WS" >/dev/null 2>&1; then
    summary="workspace \`$WS\` does not exist: create it once with \`tofu workspace new $WS\` in \`$DIR\` (tfpls never creates workspaces)"; finish
  fi
fi

tofu_version=$(tofu version -json 2>/dev/null | jq -r '.terraform_version // empty')
[ -n "$tofu_version" ] || tofu_version=$(tofu version | head -1 | sed 's/^[^0-9]*//')
tree=$(git -C "$REPO_ROOT" rev-parse 'HEAD^{tree}' 2>/dev/null || echo "")

# ---- should this run apply a *reviewed* plan?
PLAN_MODE="${PLAN_MODE:-fresh}"
need_reviewed=false
case "$APPLY" in
  force) need_reviewed=true;;
  auto) [ "$AUTO_APPLY" != never ] && need_reviewed=true;;
esac
[ "$PLAN_MODE" = fresh ] && need_reviewed=false

use_reviewed=false
if [ "$need_reviewed" = true ]; then
  pname=$(tfpls_plan_name "$NAME" "${HEAD_SHA:-}" "$TARGETS")
  tmpd="${RUNNER_TEMP:-/tmp}/tfpls-reviewed-$$"; rm -rf "$tmpd"; mkdir -p "$tmpd"
  reason=""
  if [ -z "${HEAD_SHA:-}" ]; then
    reason="no pull request is associated with this commit (direct push?)"
  elif tfpls_fetch "$GITHUB_REPOSITORY" "$pname" "$tmpd/dl" >/dev/null \
       && tfpls_unpack "$TFPLS_OUT" "$tmpd/x" \
       && tfpls_check_meta "$tmpd/x/meta.json" "$tree" "$NAME" "$DIR" "$tofu_version"; then
    cp "$tmpd/x/tfplan" tfplan
    planned_at=$(jq -r '.created_at // ""' "$tmpd/x/meta.json")
    use_reviewed=true; reviewed_used=true
  else
    reason=$(oneline "$TFPLS_ERR")
  fi
  rm -rf "$tmpd"
  if [ "$use_reviewed" = false ]; then
    hint=$(tfpls_hint "$reason" "${LABEL:-$NAME}" "$TARGETS")
    if [ "$PLAN_MODE" = reviewed_only ]; then
      echo "::error::No usable reviewed plan: $reason"
      summary="No reviewed plan to apply: $reason. $hint"
      plan_note="**$NAME**: refused to apply, no usable reviewed plan ($reason)"
      finish
    fi
    body_extra="⚠️ No reviewed plan was used ($reason), so this run planned again."
    plan_note="**$NAME**: no reviewed plan used ($reason); re-planned and applied under \`auto_apply\`."
    echo "::warning::No reviewed plan used: $reason; re-planning"
  fi
fi

if [ "$use_reviewed" = true ]; then
  tofu show -no-color tfplan > plan.txt 2>&1 || { summary="could not read the reviewed plan"; finish; }
  cat plan.txt
  rc=2
  echo "::notice::Using the reviewed plan $pname (planned ${planned_at:-earlier}); not re-planning"
  plan_note="**$NAME**: used the reviewed plan \`$pname\` (planned ${planned_at:-earlier})."
else
  plan_args=(-input=false -no-color -detailed-exitcode -parallelism="${PARALLELISM:-10}" -lock-timeout="${LOCK_TIMEOUT:-0s}" -out=tfplan)
  # a plan that will never be applied (PR plans, /tofu plan, drift reports) may skip the lock so that it cannot block an apply;
  # a plan that may be applied keeps the lock (the apply of the saved plan is rejected if the state moved in the meantime)
  if [ "${PLAN_LOCK:-true}" = false ] && [ "$APPLY" = none ]; then plan_args+=(-lock=false); fi
  [ -n "$VAR_FILE" ] && plan_args+=("-var-file=$VAR_FILE")
  tofu plan "${plan_args[@]}" ${target_args[@]+"${target_args[@]}"} 2>&1 | tee plan.txt
  rc=${PIPESTATUS[0]}
fi

if [ "$rc" -eq 1 ]; then
  summary="plan failed"
  # a held state lock is the most common reason: say who holds it and since when (drift issues and alerts show the summary)
  # shellcheck source=../unlock/parse.sh
  . "$here/../unlock/parse.sh"
  if parse_lock "$(cat plan.txt)" >/dev/null 2>&1 || [ -n "${LOCK_WHO:-}${LOCK_CREATED:-}" ]; then
    summary="plan failed: the state is locked by ${LOCK_WHO:-someone} since ${LOCK_CREATED%%.*}${LOCK_OP:+ ($LOCK_OP)}"
    summary=$(oneline "$summary")
  fi
  why=$("$here/failure.sh" plan.txt "${LABEL:-$NAME}" 2>/dev/null) && [ -n "$why" ] && body_extra="${body_extra:+$body_extra
}$why"
  finish
fi
if [ "$rc" -eq 0 ]; then status=no-changes; summary="No changes."; finish; fi

# the readable plan for the comment: tofu's own rendering has no "Refreshing state..." lines; if it fails, filter the console output
tofu show -no-color tfplan > show.txt 2>/dev/null || tfpls_strip_refresh < plan.txt > show.txt
tofu show -json tfplan > plan.json
# the plan is summarized once; header, counts, the destroy warning and the changed addresses all come from that JSON
sjson=$("$here/summarize.sh" plan.json json 2>/dev/null) || sjson=""
summary=$(jq -r .header <<<"$sjson" 2>/dev/null) || summary=""
counts=$(jq -c .counts <<<"$sjson" 2>/dev/null) || counts=""
# changed resources for notifications: addresses only (never values), each once, at most 50
changes_all=$(jq -c '[.resources[]? | {action: ({"create":"added","update":"changed","delete":"deleted","replace":"replaced"}[.action] // .action), address}]' <<<"$sjson" 2>/dev/null) || changes_all="[]"
changes=$(jq -c '.[0:50]' <<<"$changes_all" 2>/dev/null) || changes="[]"
changes_more=$(jq -r '[length - 50, 0] | max' <<<"$changes_all" 2>/dev/null) || changes_more=0
# the warning about destroyed/replaced resources stays visible above the collapsed plan
destroy_banner=$("$here/summarize.sh" - banner <<<"$sjson" 2>/dev/null) || destroy_banner=""
[ -n "$summary" ] || summary="(could not summarize the plan, see the full plan below)"
if jq -e '[.resource_changes[]?.change.actions] | all(. == ["create"] or . == ["no-op"] or . == ["read"])' plan.json >/dev/null; then
  additive=true
fi

# ---- policy (re-evaluated even for a reviewed plan: policy files may have changed since the plan)
policy_args=()
[ -n "$POLICY_DIR" ] && policy_args+=(-p "$REPO_ROOT/$POLICY_DIR")
if [ -n "${POLICY_PACKS:-}" ]; then
  lib="$here/../../policies/library"
  IFS=',' read -r -a packs <<<"$POLICY_PACKS"
  for pack in "${packs[@]}"; do
    pack="${pack// /}"; [ -n "$pack" ] || continue
    if [[ ! "$pack" =~ ^[a-z0-9][a-z0-9-]*$ ]] || [ ! -d "$lib/$pack" ]; then
      available=$(ls "$lib" 2>/dev/null | tr '\n' ' ')
      summary="Unknown policy pack \`$pack\` (available: ${available:-none})"; status=error; finish
    fi
    policy_args+=(-p "$lib/$pack")
  done
fi
if [ "${#policy_args[@]}" -gt 0 ] && [ -n "${POLICY_PARAMS:-}" ]; then
  pdir=$(mktemp -d); jq -c '{params: .}' <<<"$POLICY_PARAMS" > "$pdir/params.json" 2>/dev/null \
    && policy_args+=(--data "$pdir") || echo "::warning::policy_params is not valid JSON, ignored"
fi
if [ "${#policy_args[@]}" -gt 0 ]; then
  if conftest test plan.json "${policy_args[@]}" --no-color > policy.txt 2>&1; then
    warns=$(grep -c '^WARN' policy.txt || true)
    if [ "${warns:-0}" -gt 0 ]; then
      body_extra="${body_extra:+$body_extra
}$(printf 'Policy: ✅ passed with %s warning(s)\n```\n%s\n```' "$warns" "$(grep -E '^WARN' policy.txt | head -c 4000)")"
    else
      body_extra="${body_extra:+$body_extra
}Policy: ✅ passed"
    fi
  else
    status=policy-failed
    cat policy.txt
    body_extra="${body_extra:+$body_extra
}$(printf 'Policy: 🛑 failed\n```\n%s\n```' "$(head -c 8000 policy.txt)")"
    finish
  fi
fi

# ---- save the plan for later review/apply (PR plans and /tofu plan)
if [ "${SAVE_PLAN:-false}" = true ] && [ "$use_reviewed" = false ]; then
  meta="$(mktemp)"
  jq -n --arg name "$NAME" --arg dir "$DIR" --arg v "$tofu_version" --arg tree "$tree" --arg sha "${HEAD_SHA:-}" \
        --arg targets "$TARGETS" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg run "$RUN_URL" \
        '{name:$name,dir:$dir,tofu_version:$v,tree:$tree,head_sha:$sha,targets:$targets,created_at:$at,plan_run_url:$run}' > "$meta"
  sname=$(tfpls_plan_name "$NAME" "${HEAD_SHA:-}" "$TARGETS")
  sdir="${RUNNER_TEMP:-/tmp}/tfpls-save-$$"; rm -rf "$sdir"
  if [ -z "${HEAD_SHA:-}" ]; then
    echo "::warning::not saving the plan: no head SHA"
  elif [ -z "${PLAN_ENCRYPTION_KEY:-}" ] && [ "${REPO_PRIVATE:-}" != true ]; then
    # a public repository (or one whose visibility is unknown): an unencrypted plan artifact would be readable by anyone
    # shellcheck disable=SC2034
    TFPLS_NO_HINT=1   # read by format.sh (sourced above)
    body_extra="${body_extra:+$body_extra
}🔒 Plan not saved: this repository is public, so anyone could download a saved plan (it can contain secret values). Run once \`openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY\`, then push or comment \`/tofu plan ${LABEL:-$NAME}\`. Until then \`/tofu apply\` has nothing to apply."
    echo "::warning::plan not saved: public repository without PLAN_ENCRYPTION_KEY"
  elif tfpls_pack "$PWD" "$sdir" "$meta" >/dev/null; then
    spath=$TFPLS_OUT
    out plan_path "$spath"; out plan_name "$sname"
    enc="encrypted"; case "$spath" in *.tgz) enc="NOT encrypted";; esac
    if [ "$enc" = "NOT encrypted" ] && tfpls_unencrypted_warns "${ALLOW_UNENCRYPTED_PLANS:-false}"; then
      body_extra="$body_extra
⚠️ \`PLAN_ENCRYPTION_KEY\` is not set: the saved plan is stored unencrypted and may contain secret values. See https://github.com/${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}/blob/main/docs/reviewed-plans.md."
      echo "::warning::PLAN_ENCRYPTION_KEY is not set; the saved plan is unencrypted"
    fi
  else
    summary="$summary (could not save the plan: $(oneline "$TFPLS_ERR"))"; echo "::error::could not save the plan: $(oneline "$TFPLS_ERR")"
    status=error; finish
  fi
  rm -f "$meta"
fi

# ---- apply decision
status=planned
do_apply=false
case "$APPLY" in
  force) do_apply=true;;
  auto)
    if [ "$AUTO_APPLY" = always ] || { [ "$AUTO_APPLY" = additive_only ] && [ "$additive" = true ]; }; then
      do_apply=true
    else
      status=skipped; body_extra="${body_extra:+$body_extra
}Auto-apply not allowed for this change (auto_apply=\`$AUTO_APPLY\`, additive=\`$additive\`). It is applied by \`/tofu apply ${LABEL:-$NAME}\` on an approved pull request, or from its drift issue once the next drift check reports it."
      echo "::notice::auto-apply not allowed for this change (auto_apply=$AUTO_APPLY, additive=$additive)"
    fi;;
esac

if [ "$do_apply" = true ]; then
  tofu apply -input=false -no-color -parallelism="${PARALLELISM:-10}" -lock-timeout="${LOCK_TIMEOUT:-0s}" tfplan 2>&1 | tee apply.txt
  if [ "${PIPESTATUS[0]}" -eq 0 ]; then
    status=applied
    tfpls_apply_view < apply.txt > apply_view.txt
  else
    status=error; summary="apply failed"
    if grep -qiE "plan is stale|can no longer be applied" apply.txt; then
      if [ "$use_reviewed" = true ] && [ "$PLAN_MODE" = reviewed ]; then
        # merge-time default: a stale reviewed plan counts as "no usable plan" => re-plan under auto_apply
        echo "::warning::reviewed plan is stale; re-planning"
        rm -f tfplan
        export PLAN_MODE=fresh PLAN_FALLBACK_NOTE="The reviewed plan was stale (state changed after it was made), so this run planned again and applied under \`auto_apply\`."
        exec bash "${BASH_SOURCE[0]}"
      fi
      summary="The state changed after the plan was made, so the reviewed plan can no longer be applied. Run \`/tofu plan ${LABEL:-$NAME}\` again and review the new plan."
    fi
    cp apply.txt plan.txt
    [ "$status" = error ] && [ -z "$(grep -iE 'plan is stale|can no longer be applied' apply.txt)" ] \
      && body_extra="${body_extra:+$body_extra
}$("$here/rollback.sh" apply.txt "${GITHUB_SHA:-}" 2>/dev/null)"
  fi
fi
finish
