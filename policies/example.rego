package main

import rego.v1

# Input is `tofu show -json` output. Each deny message fails the check.

deny contains msg if {
	some rc in input.resource_changes
	"delete" in rc.change.actions
	startswith(rc.type, "cloudflare_zone")
	msg := sprintf("%s: zones must not be destroyed", [rc.address])
}
