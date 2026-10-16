package main

import rego.v1

inst(t) := {"resource_changes": [{"address": "aws_instance.i", "type": "aws_instance", "change": {"actions": ["create"], "after": {"instance_type": t}}}]}

test_huge_instance_warns if {
	count(warn) >= 1 with input as inst("p5.48xlarge")
}

test_24xlarge_warns if {
	count(warn) >= 1 with input as inst("m5.24xlarge")
}

test_small_instance_clean if {
	count(warn) == 0 with input as inst("t3.micro")
}
