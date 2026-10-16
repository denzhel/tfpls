package main

import rego.v1

# Stateful resources must not be destroyed or replaced by accident.
stateful := {
	"aws_s3_bucket", "aws_db_instance", "aws_rds_cluster", "aws_dynamodb_table", "aws_kms_key",
	"aws_efs_file_system", "aws_ebs_volume", "aws_elasticache_replication_group", "aws_opensearch_domain",
	"google_sql_database_instance", "azurerm_mssql_database", "cloudflare_zone", "kubernetes_persistent_volume_claim",
}

# Parameters (config `policy_params: { safety: { protected_types: [...], max_destroys: N } }`, passed to conftest as data.params.safety)
default safety_params := {}

safety_params := data.params.safety

protected := stateful | {t | some t in object.get(safety_params, "protected_types", [])}

destroyed contains rc.address if {
	some rc in input.resource_changes
	"delete" in rc.change.actions
}

deny contains msg if {
	max := object.get(safety_params, "max_destroys", -1)
	max >= 0
	n := count(destroyed)
	n > max
	msg := sprintf("safety: the plan destroys %d resources, more than the allowed %d", [n, max])
}

deny contains msg if {
	some rc in input.resource_changes
	rc.type in protected
	"delete" in rc.change.actions
	not "create" in rc.change.actions
	msg := sprintf("safety: %s is stateful and would be destroyed", [rc.address])
}

deny contains msg if {
	some rc in input.resource_changes
	rc.type in protected
	"delete" in rc.change.actions
	"create" in rc.change.actions
	msg := sprintf("safety: %s is stateful and would be replaced (destroy and re-create)", [rc.address])
}

warn contains msg if {
	some rc in input.resource_changes
	not rc.type in protected
	rc.change.actions == ["delete"]
	msg := sprintf("safety: %s will be destroyed", [rc.address])
}
