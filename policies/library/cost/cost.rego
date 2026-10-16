package main

import rego.v1

# Cheap guard rail without a price list: flag very large instance classes.
big_ec2 := {"p4d.24xlarge", "p5.48xlarge", "x2idn.32xlarge", "u-6tb1.112xlarge"}

warn contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_instance"
	rc.change.actions[_] in {"create", "update"}
	t := rc.change.after.instance_type
	endswith(t, "24xlarge")
	msg := sprintf("cost: %s uses a very large instance type (%s)", [rc.address, t])
}

warn contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_instance"
	rc.change.actions[_] in {"create", "update"}
	rc.change.after.instance_type in big_ec2
	msg := sprintf("cost: %s uses an expensive instance type (%s)", [rc.address, rc.change.after.instance_type])
}

warn contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_db_instance"
	rc.change.actions[_] in {"create", "update"}
	rc.change.after.multi_az == true
	startswith(object.get(rc.change.after, "instance_class", ""), "db.r")
	msg := sprintf("cost: %s is a Multi-AZ memory-optimised database", [rc.address])
}

warn contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_ebs_volume"
	rc.change.actions[_] in {"create", "update"}
	rc.change.after.size > 1000
	msg := sprintf("cost: %s is a %d GiB volume", [rc.address, rc.change.after.size])
}
