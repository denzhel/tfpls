package main

import rego.v1

sg(cidr, from, to) := {"resource_changes": [{"address": "aws_security_group.s", "type": "aws_security_group", "change": {"actions": ["create"], "after": {"ingress": [{"from_port": from, "to_port": to, "cidr_blocks": [cidr], "ipv6_cidr_blocks": []}]}}}]}

test_ssh_from_anywhere_denied if {
	count(deny) == 1 with input as sg("0.0.0.0/0", 22, 22)
}

test_wide_range_from_anywhere_denied if {
	count(deny) >= 1 with input as sg("0.0.0.0/0", 0, 65535)
}

test_https_from_anywhere_allowed if {
	count(deny) == 0 with input as sg("0.0.0.0/0", 443, 443)
}

test_ssh_from_private_range_allowed if {
	count(deny) == 0 with input as sg("10.0.0.0/8", 22, 22)
}
