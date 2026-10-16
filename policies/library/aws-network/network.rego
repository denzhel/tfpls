package main

import rego.v1

admin_ports := {22, 3389}
world := {"0.0.0.0/0", "::/0"}

covers(from, to, port) if {
	from <= port
	to >= port
}

# inline rules of aws_security_group
deny contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_security_group"
	rc.change.actions[_] in {"create", "update"}
	some rule in object.get(rc.change.after, "ingress", [])
	some cidr in array.concat(object.get(rule, "cidr_blocks", []), object.get(rule, "ipv6_cidr_blocks", []))
	cidr in world
	some port in admin_ports
	covers(rule.from_port, rule.to_port, port)
	msg := sprintf("aws-network: %s opens port %d to the world (%s)", [rc.address, port, cidr])
}

# standalone rules
deny contains msg if {
	some rc in input.resource_changes
	rc.type in {"aws_security_group_rule", "aws_vpc_security_group_ingress_rule"}
	rc.change.actions[_] in {"create", "update"}
	a := rc.change.after
	object.get(a, "type", "ingress") == "ingress"
	cidr := object.get(a, "cidr_ipv4", object.get(a, "cidr_ipv6", ""))
	cidr in world
	some port in admin_ports
	covers(a.from_port, a.to_port, port)
	msg := sprintf("aws-network: %s opens port %d to the world (%s)", [rc.address, port, cidr])
}

deny contains msg if {
	some rc in input.resource_changes
	rc.type == "aws_security_group_rule"
	rc.change.actions[_] in {"create", "update"}
	a := rc.change.after
	a.type == "ingress"
	some cidr in object.get(a, "cidr_blocks", [])
	cidr in world
	some port in admin_ports
	covers(a.from_port, a.to_port, port)
	msg := sprintf("aws-network: %s opens port %d to the world (%s)", [rc.address, port, cidr])
}
