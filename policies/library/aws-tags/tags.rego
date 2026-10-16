package main

import rego.v1

# Taggable AWS resources that are created or updated should carry at least one tag.
# Provider-level default_tags show up in tags_all, so they count.
taggable := {
	"aws_s3_bucket", "aws_sqs_queue", "aws_sns_topic", "aws_instance", "aws_db_instance", "aws_rds_cluster",
	"aws_dynamodb_table", "aws_lambda_function", "aws_kms_key", "aws_vpc", "aws_subnet", "aws_security_group",
	"aws_lb", "aws_ebs_volume", "aws_efs_file_system", "aws_cloudwatch_log_group", "aws_ecr_repository",
}

has_tags(after) if count(object.get(after, "tags_all", {})) > 0

has_tags(after) if count(object.get(after, "tags", {})) > 0


warn contains msg if {
	some rc in input.resource_changes
	rc.type in taggable
	rc.change.actions[_] in {"create", "update"}
	after := rc.change.after
	not has_tags(after)
	not object.get(object.get(rc.change, "after_unknown", {}), "tags_all", false)
	msg := sprintf("aws-tags: %s has no tags", [rc.address])
}

# Parameter: `policy_params: { aws-tags: { required_tags: ["team", "env"] } }` (data.params["aws-tags"]): every listed tag must be present
default tag_params := {}

tag_params := data.params["aws-tags"]

warn contains msg if {
	some rc in input.resource_changes
	rc.type in taggable
	rc.change.actions[_] in {"create", "update"}
	after := rc.change.after
	tags := object.union(object.get(after, "tags_all", {}), object.get(after, "tags", {}))
	not object.get(object.get(rc.change, "after_unknown", {}), "tags_all", false)
	some tag in object.get(tag_params, "required_tags", [])
	not tags[tag]
	msg := sprintf("aws-tags: %s is missing the required tag `%s`", [rc.address, tag])
}
