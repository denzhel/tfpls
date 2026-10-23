# Two resources of the built-in terraform_data provider: no cloud, no provider download, state in the local backend.
variable "hold" {
  type    = number
  default = 0
}

resource "terraform_data" "a" {
  input = "alpha"
}

resource "terraform_data" "b" {
  input = "beta"
}

# Only for the lock test: while it is applied, tofu holds the state lock.
resource "terraform_data" "hold" {
  count = var.hold > 0 ? 1 : 0
  provisioner "local-exec" {
    command = "sleep ${var.hold}"
  }
}

output "a" {
  value = terraform_data.a.output
}
