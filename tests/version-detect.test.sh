#!/usr/bin/env bash
# Tests for actions/setup/detect-version.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
D="$here/actions/setup/detect-version.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fresh() { rm -rf "$T/r"; mkdir -p "$T/r"; }

fresh; eq "nothing asked for: nothing printed" "$(bash "$D" "$T/r")" ""
fresh; echo "1.11.4" > "$T/r/.opentofu-version"; eq ".opentofu-version" "$(bash "$D" "$T/r")" "1.11.4"
fresh; printf '# pinned\nv1.9.0  # tenv\n' > "$T/r/.terraform-version"; eq ".terraform-version with a v, comments and spaces" "$(bash "$D" "$T/r")" "1.9.0"
fresh; echo 1.8.0 > "$T/r/.terraform-version"; echo 1.11.4 > "$T/r/.opentofu-version"; eq ".opentofu-version wins over .terraform-version" "$(bash "$D" "$T/r")" "1.11.4"
fresh; echo 1.7.1 > "$T/r/.tofu-version"; echo 1.8.0 > "$T/r/.terraform-version"; eq ".tofu-version wins over .terraform-version" "$(bash "$D" "$T/r")" "1.7.1"
fresh; printf 'terraform {\n  required_version = "1.11.4"\n}\n' > "$T/r/terraform.tf"; eq "an exact required_version" "$(bash "$D" "$T/r")" "1.11.4"
fresh; printf 'terraform {\n  required_version = "= 1.10.2"\n}\n' > "$T/r/versions.tf"; eq "an '=' required_version" "$(bash "$D" "$T/r")" "1.10.2"
fresh; printf 'terraform {\n  required_version = ">= 1.6.0"\n}\n' > "$T/r/terraform.tf"; eq "a range is ignored" "$(bash "$D" "$T/r")" ""
fresh; printf 'terraform {\n  required_version = "~> 1.6"\n}\n' > "$T/r/terraform.tf"; eq "a pessimistic constraint is ignored" "$(bash "$D" "$T/r")" ""
fresh; echo "latest" > "$T/r/.opentofu-version"; printf 'terraform {\n  required_version = "1.5.0"\n}\n' > "$T/r/terraform.tf"; eq "an unusable version file falls through to required_version" "$(bash "$D" "$T/r")" "1.5.0"
fresh; echo 1.11.4 > "$T/r/.opentofu-version"; printf 'terraform {\n  required_version = "1.5.0"\n}\n' > "$T/r/terraform.tf"; eq "the version file wins over required_version" "$(bash "$D" "$T/r")" "1.11.4"

done_tests
