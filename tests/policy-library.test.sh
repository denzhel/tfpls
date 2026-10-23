#!/usr/bin/env bash
# Tests for policies/library/*: every pack must fail on a bad plan and stay quiet on a good one.
# Needs conftest (found on PATH, in $TFPLS_CONFTEST, or downloaded once with tfpls setup).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
L="$here/policies/library"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

C="${TFPLS_CONFTEST:-$(command -v conftest || true)}"
if [ -z "$C" ]; then
  C=$(find "${RUNNER_TOOL_CACHE:-$HOME/.cache}" -path '*conftest*' -name conftest -type f 2>/dev/null | head -1)
fi
if [ -z "$C" ]; then
  build_bin tfpls "$T/tfpls" && TFPLS_TOOL_CACHE="$T/cache" GITHUB_PATH="$T/path" INSTALL_CONFTEST=true "$T/tfpls" setup >/dev/null 2>&1 || true
  C=$(find "$T/cache" -name conftest -type f 2>/dev/null | head -1)
fi
[ -n "$C" ] || { echo "conftest not available"; exit 1; }

# plan <type> <actions-json> <after-json> [after_unknown-json]  => plan json on stdout
plan() { jq -cn --arg t "$1" --argjson a "$2" --argjson af "$3" --argjson au "${4:-{\}}" \
  '{resource_changes:[{address:($t+".x"), type:$t, change:{actions:$a, before:null, after:$af, after_unknown:$au}}]}'; }

# run <pack> <plan> => sets out and rc
run() { echo "$2" > "$T/p.json"; out=$("$C" test "$T/p.json" -p "$L/$1" --no-color 2>&1); rc=$?; }
fails() { run "$1" "$2"; [ "$rc" -ne 0 ] && grep -q "$3" <<<"$out"; }
clean() { run "$1" "$2"; [ "$rc" -eq 0 ] && ! grep -qE 'WARN|FAIL' <<<"$out"; }
warns() { run "$1" "$2"; [ "$rc" -eq 0 ] && grep -q "WARN" <<<"$out" && grep -q "$3" <<<"$out"; }

# ---- safety
fails safety "$(plan aws_s3_bucket '["delete"]' null)" "stateful and would be destroyed" && ok "safety: bucket destroy denied" || bad "safety destroy: $out"
fails safety "$(plan aws_db_instance '["delete","create"]' '{}')" "would be replaced" && ok "safety: database replace denied" || bad "safety replace: $out"
warns safety "$(plan aws_sqs_queue '["delete"]' null)" "will be destroyed" && ok "safety: other destroys only warn" || bad "safety warn: $out"
clean safety "$(plan aws_s3_bucket '["update"]' '{"bucket":"b"}')" && ok "safety: in-place update is clean" || bad "safety clean: $out"

# ---- aws-tags
warns aws-tags "$(plan aws_s3_bucket '["create"]' '{"tags":{},"tags_all":{}}')" "has no tags" && ok "aws-tags: untagged bucket warns" || bad "tags warn: $out"
clean aws-tags "$(plan aws_s3_bucket '["create"]' '{"tags":{"team":"a"}}')" && ok "aws-tags: tagged bucket is clean" || bad "tags clean: $out"
clean aws-tags "$(plan aws_s3_bucket '["create"]' '{"tags":{}}' '{"tags_all":true}')" && ok "aws-tags: tags_all computed counts as tagged" || bad "tags unknown: $out"
clean aws-tags "$(plan aws_s3_bucket_policy '["create"]' '{}')" && ok "aws-tags: non-taggable types ignored" || bad "tags other: $out"

# ---- aws-s3
fails aws-s3 "$(plan aws_s3_bucket '["create"]' '{"acl":"public-read"}')" "public ACL" && ok "aws-s3: public ACL denied" || bad "s3 acl: $out"
fails aws-s3 "$(plan aws_s3_bucket_public_access_block '["create"]' '{"block_public_acls":true,"block_public_policy":false}')" "block_public_policy" && ok "aws-s3: disabled public access block denied" || bad "s3 pab: $out"
clean aws-s3 "$(plan aws_s3_bucket_public_access_block '["create"]' '{"block_public_acls":true,"block_public_policy":true,"ignore_public_acls":true,"restrict_public_buckets":true}')" && ok "aws-s3: full public access block is clean" || bad "s3 clean: $out"
warns aws-s3 "$(plan aws_s3_bucket_versioning '["create"]' '{"versioning_configuration":[{"status":"Disabled"}]}')" "disables versioning" && ok "aws-s3: versioning disabled warns" || bad "s3 versioning: $out"

# ---- aws-network
fails aws-network "$(plan aws_security_group '["create"]' '{"ingress":[{"from_port":22,"to_port":22,"cidr_blocks":["0.0.0.0/0"],"ipv6_cidr_blocks":[]}]}')" "port 22 to the world" && ok "aws-network: ssh from anywhere denied" || bad "net ssh: $out"
fails aws-network "$(plan aws_security_group '["create"]' '{"ingress":[{"from_port":0,"to_port":65535,"cidr_blocks":[],"ipv6_cidr_blocks":["::/0"]}]}')" "to the world" && ok "aws-network: wide range from anywhere denied" || bad "net range: $out"
fails aws-network "$(plan aws_vpc_security_group_ingress_rule '["create"]' '{"from_port":3389,"to_port":3389,"cidr_ipv4":"0.0.0.0/0"}')" "3389" && ok "aws-network: standalone rdp rule denied" || bad "net rdp: $out"
clean aws-network "$(plan aws_security_group '["create"]' '{"ingress":[{"from_port":443,"to_port":443,"cidr_blocks":["0.0.0.0/0"],"ipv6_cidr_blocks":[]}]}')" && ok "aws-network: https from anywhere is allowed" || bad "net https: $out"
clean aws-network "$(plan aws_security_group '["create"]' '{"ingress":[{"from_port":22,"to_port":22,"cidr_blocks":["10.0.0.0/8"],"ipv6_cidr_blocks":[]}]}')" && ok "aws-network: ssh from a private range is allowed" || bad "net private: $out"

# ---- aws-iam
star='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}'
fails aws-iam "$(plan aws_iam_policy '["create"]' "$(jq -cn --arg p "$star" '{policy:$p}')")" 'Action "\*"' && ok "aws-iam: Action * on Resource * denied" || bad "iam star: $out"
ok_doc='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"arn:aws:s3:::b/*"}]}'
clean aws-iam "$(plan aws_iam_policy '["create"]' "$(jq -cn --arg p "$ok_doc" '{policy:$p}')")" && ok "aws-iam: scoped policy is clean" || bad "iam clean: $out"
anyone='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"}]}'
fails aws-iam "$(plan aws_iam_role '["create"]' "$(jq -cn --arg p "$anyone" '{assume_role_policy:$p}')")" "assumed by anyone" && ok "aws-iam: role assumable by anyone denied" || bad "iam principal: $out"
clean aws-iam "$(plan aws_iam_policy '["create"]' '{"policy":null}' '{"policy":true}')" && ok "aws-iam: unknown policy document is skipped" || bad "iam unknown: $out"

# ---- cost
warns cost "$(plan aws_instance '["create"]' '{"instance_type":"p5.48xlarge"}')" "expensive instance" && ok "cost: huge GPU instance warns" || bad "cost p5: $out"
warns cost "$(plan aws_instance '["create"]' '{"instance_type":"m5.24xlarge"}')" "very large" && ok "cost: 24xlarge warns" || bad "cost 24x: $out"
warns cost "$(plan aws_ebs_volume '["create"]' '{"size":2000}')" "2000 GiB" && ok "cost: huge volume warns" || bad "cost ebs: $out"
clean cost "$(plan aws_instance '["create"]' '{"instance_type":"t3.micro"}')" && ok "cost: small instance is clean" || bad "cost clean: $out"

# ---- the rego unit tests of every pack (conftest verify), as CI runs them
for p in "$L"/*; do
  "$C" verify -p "$p" --no-color >"$T/v.out" 2>&1 && grep -q ' 0 failures' "$T/v.out" && ok "$(basename "$p"): conftest verify passes ($(grep -oE '[0-9]+ tests' "$T/v.out" | head -1))" || bad "$(basename "$p") conftest verify: $(cat "$T/v.out" | tail -3)"
done

# ---- parameters (policy_params -> data.params) and combining with your own policy_dir
mkdir -p "$T/pd"; echo '{"params":{"safety":{"max_destroys":1},"aws-tags":{"required_tags":["team","env"]}}}' > "$T/pd/params.json"
three=$(jq -cn '{resource_changes:[range(3)|{address:"a.r\(.)",type:"a",change:{actions:["delete"]}}]}')
echo "$three" > "$T/p.json"
out=$("$C" test "$T/p.json" -p "$L/safety" --no-color 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "safety without params: three plain destroys only warn" || bad "safety no params: $out"
out=$("$C" test "$T/p.json" -p "$L/safety" --data "$T/pd" --no-color 2>&1); rc=$?
[ "$rc" -ne 0 ] && grep -q "destroys 3 resources, more than the allowed 1" <<<"$out" && ok "safety: max_destroys from policy_params denies" || bad "safety max_destroys: $out"
echo '{"resource_changes":[{"address":"aws_sqs_queue.q","type":"aws_sqs_queue","change":{"actions":["create"],"after":{"tags":{"team":"a"},"tags_all":{"team":"a"}},"after_unknown":{}}}]}' > "$T/p.json"
out=$("$C" test "$T/p.json" -p "$L/aws-tags" --data "$T/pd" --no-color 2>&1)
grep -q 'missing the required tag `env`' <<<"$out" && ! grep -q 'tag `team`' <<<"$out" && ok "aws-tags: required_tags from policy_params names the missing tag" || bad "aws-tags params: $out"
# a pack can be switched on and off: the same plan gives different results
echo "$(plan aws_s3_bucket '["delete"]' null)" > "$T/p.json"
mkdir -p "$T/empty"; printf 'package main\n' > "$T/empty/none.rego"
"$C" test "$T/p.json" -p "$T/empty" --no-color >/dev/null 2>&1 && with_off=pass || with_off=fail
"$C" test "$T/p.json" -p "$L/safety" --no-color >/dev/null 2>&1 && with_on=pass || with_on=fail
[ "$with_off|$with_on" = "pass|fail" ] && ok "enabling a pack changes the plan result, disabling removes it" || bad "pack on/off: $with_off|$with_on"
mkdir -p "$T/own"; printf 'package main\nimport rego.v1\ndeny contains msg if {\n\tsome rc in input.resource_changes\n\trc.type == "aws_s3_bucket"\n\tmsg := "own: no buckets at all"\n}\n' > "$T/own/own.rego"
out=$("$C" test "$T/p.json" -p "$T/own" -p "$L/safety" --no-color 2>&1)
grep -q "own: no buckets at all" <<<"$out" && grep -q "safety: " <<<"$out" && ok "your own policy_dir and a pack are evaluated together" || bad "combined: $out"

# ---- all packs together on an empty plan
for p in "$L"/*; do clean "$(basename "$p")" '{"resource_changes":[]}' && ok "$(basename "$p"): empty plan is clean" || bad "$(basename "$p") empty: $out"; done

done_tests
