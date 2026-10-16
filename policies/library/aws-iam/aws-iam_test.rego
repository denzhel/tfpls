package main

import rego.v1

pol(doc) := {"resource_changes": [{"address": "aws_iam_policy.p", "type": "aws_iam_policy", "change": {"actions": ["create"], "after": {"policy": json.marshal(doc)}}}]}

test_star_star_denied if {
	count(deny) >= 1 with input as pol({"Statement": [{"Effect": "Allow", "Action": "*", "Resource": "*"}]})
}

test_scoped_policy_clean if {
	count(deny) == 0 with input as pol({"Statement": [{"Effect": "Allow", "Action": ["s3:GetObject"], "Resource": "arn:aws:s3:::b/*"}]})
}

test_assume_role_by_anyone_denied if {
	r := {"resource_changes": [{"address": "aws_iam_role.r", "type": "aws_iam_role", "change": {"actions": ["create"], "after": {"assume_role_policy": json.marshal({"Statement": [{"Effect": "Allow", "Principal": "*", "Action": "sts:AssumeRole"}]})}}}]}
	count(deny) == 1 with input as r
}
