package main

import rego.v1

deny contains msg if {
	some rc in input.resource_changes
	rc.address == "terraform_data.b"
	msg := "terraform_data.b is not allowed"
}
