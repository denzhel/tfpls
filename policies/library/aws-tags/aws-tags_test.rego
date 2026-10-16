package main

import rego.v1

res(tags) := {"resource_changes": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "change": {"actions": ["create"], "after": {"tags": tags, "tags_all": tags}, "after_unknown": {}}}]}

test_untagged_warns if {
	count(warn) == 1 with input as res({})
}

test_tagged_is_clean if {
	count(warn) == 0 with input as res({"team": "a"})
}

test_required_tags_param if {
	count(warn) == 1 with input as res({"team": "a"}) with data.params as {"aws-tags": {"required_tags": ["team", "env"]}}
	count(warn) == 0 with input as res({"team": "a", "env": "x"}) with data.params as {"aws-tags": {"required_tags": ["team", "env"]}}
}
