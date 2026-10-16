package main

import rego.v1

bucket_delete := {"resource_changes": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "change": {"actions": ["delete"]}}]}

test_stateful_destroy_is_denied if {
	count(deny) == 1 with input as bucket_delete
}

test_replace_is_denied if {
	r := {"resource_changes": [{"address": "aws_db_instance.d", "type": "aws_db_instance", "change": {"actions": ["delete", "create"]}}]}
	count(deny) == 1 with input as r
}

test_other_destroy_only_warns if {
	r := {"resource_changes": [{"address": "aws_sqs_queue.q", "type": "aws_sqs_queue", "change": {"actions": ["delete"]}}]}
	count(deny) == 0 with input as r
	count(warn) == 1 with input as r
}

test_update_is_clean if {
	r := {"resource_changes": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "change": {"actions": ["update"]}}]}
	count(deny) == 0 with input as r
	count(warn) == 0 with input as r
}

test_extra_protected_type_from_params if {
	r := {"resource_changes": [{"address": "aws_sqs_queue.q", "type": "aws_sqs_queue", "change": {"actions": ["delete"]}}]}
	count(deny) == 1 with input as r with data.params as {"safety": {"protected_types": ["aws_sqs_queue"]}}
}

test_max_destroys_from_params if {
	r := {"resource_changes": [
		{"address": "a.x", "type": "a", "change": {"actions": ["delete"]}},
		{"address": "a.y", "type": "a", "change": {"actions": ["delete"]}},
		{"address": "a.z", "type": "a", "change": {"actions": ["delete"]}},
	]}
	count(deny) == 1 with input as r with data.params as {"safety": {"max_destroys": 2}}
	count(deny) == 0 with input as r with data.params as {"safety": {"max_destroys": 3}}
	count(deny) == 0 with input as r
}
