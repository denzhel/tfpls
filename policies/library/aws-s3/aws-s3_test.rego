package main

import rego.v1

test_public_acl_denied if {
	r := {"resource_changes": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "change": {"actions": ["create"], "after": {"acl": "public-read"}}}]}
	count(deny) == 1 with input as r
}

test_private_acl_clean if {
	r := {"resource_changes": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "change": {"actions": ["create"], "after": {"acl": "private"}}}]}
	count(deny) == 0 with input as r
}

test_public_access_block_flag_denied if {
	r := {"resource_changes": [{"address": "aws_s3_bucket_public_access_block.p", "type": "aws_s3_bucket_public_access_block", "change": {"actions": ["create"], "after": {"block_public_acls": true, "block_public_policy": false, "ignore_public_acls": true, "restrict_public_buckets": true}}}]}
	count(deny) == 1 with input as r
}
