package main

import rego.v1

public_acls := {"public-read", "public-read-write", "authenticated-read"}

deny contains msg if {
	some rc in input.resource_changes
	rc.type in {"aws_s3_bucket", "aws_s3_bucket_acl"}
	rc.change.actions[_] in {"create", "update"}
	rc.change.after.acl in public_acls
	msg := sprintf("aws-s3: %s uses a public ACL (%s)", [rc.address, rc.change.after.acl])
}

deny contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_s3_bucket_public_access_block"
	rc.change.actions[_] in {"create", "update"}
	some flag in ["block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"]
	rc.change.after[flag] == false
	msg := sprintf("aws-s3: %s turns off %s", [rc.address, flag])
}

warn contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_s3_bucket_versioning"
	rc.change.actions[_] in {"create", "update"}
	some v in object.get(rc.change.after, "versioning_configuration", [])
	v.status == "Disabled"
	msg := sprintf("aws-s3: %s disables versioning", [rc.address])
}
