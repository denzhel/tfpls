package main

import rego.v1

deny contains msg if {
	some rc in input.resource_changes
	rc.address == "terraform_data.nothing"
	msg := "never"
}
