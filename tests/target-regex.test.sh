#!/usr/bin/env bash
# shellcheck disable=SC2154  # target_re is assigned by the eval below
# Tests for the -target allowlist used by actions/tofu/run.sh. Run: bash tests/target-regex.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# take the real definition from run.sh so the test cannot drift from it
eval "$(grep -m1 '^target_re=' "$here/../actions/tofu/run.sh")"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
allow() { [[ "$1" =~ $target_re ]] && ok "allows $1" || bad "should allow $1"; }
deny()  { [[ "$1" =~ $target_re ]] && bad "should deny $1" || ok "denies $1"; }

allow 'aws_sqs_queue.extra'
allow 'module.vpc'
allow 'module.x.aws_s3_bucket.a["k"]'
allow 'aws_instance.web[0]'
allow "module.m.aws_x.y['k']"
deny  'a;rm'
deny  '$(id)'
deny  'x`y`'
deny  'a b'
deny  'a|b'
deny  'a&b'
deny  '-var=x=1'
deny  ''
deny  'a>b'
deny  'a\\b'

done_tests
