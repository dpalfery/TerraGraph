variable "names" {
  type    = set(string)
  default = ["alpha", "beta", "gamma"]
}

resource "terraform_data" "fleet" {
  for_each = var.names
  input    = each.value
}

resource "terraform_data" "counted" {
  count = 2
  input = "n-${count.index}"
}

resource "terraform_data" "single" {
  triggers_replace = "v2"
  input = terraform_data.fleet["alpha"].output
}

module "child" {
  source = "./child"
  seed   = terraform_data.single.id
}
