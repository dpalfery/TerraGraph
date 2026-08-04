variable "seed" { type = string }
resource "terraform_data" "inner" {
  count = 2
  input = "${var.seed}-${count.index}"
}
output "ids" { value = terraform_data.inner[*].id }
