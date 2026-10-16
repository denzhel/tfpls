package main

import rego.v1

policy_docs[addr] := doc if {
	some rc in input.resource_changes
	rc.type in {"aws_iam_policy", "aws_iam_role_policy", "aws_iam_user_policy", "aws_iam_group_policy"}
	rc.change.actions[_] in {"create", "update"}
	is_string(rc.change.after.policy)
	doc := json.unmarshal(rc.change.after.policy)
	addr := rc.address
}

as_array(x) := x if is_array(x)

as_array(x) := [x] if not is_array(x)

deny contains msg if {
	some addr, doc in policy_docs
	some st in as_array(doc.Statement)
	st.Effect == "Allow"
	"*" in as_array(st.Action)
	"*" in as_array(st.Resource)
	msg := sprintf("aws-iam: %s allows Action \"*\" on Resource \"*\"", [addr])
}

deny contains msg if {
	some addr, doc in policy_docs
	some st in as_array(doc.Statement)
	st.Effect == "Allow"
	"*" in as_array(object.get(st, "Resource", []))
	some act in as_array(object.get(st, "Action", []))
	act in {"iam:*", "sts:AssumeRole"}
	msg := sprintf("aws-iam: %s grants %s on every resource", [addr, act])
}

deny contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_iam_role"
	rc.change.actions[_] in {"create", "update"}
	is_string(rc.change.after.assume_role_policy)
	doc := json.unmarshal(rc.change.after.assume_role_policy)
	some st in as_array(doc.Statement)
	st.Effect == "Allow"
	st.Principal == "*"
	msg := sprintf("aws-iam: %s can be assumed by anyone (Principal \"*\")", [rc.address])
}
