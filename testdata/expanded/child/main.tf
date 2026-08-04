variable "seed" { type = string }
resource "terraform_data" "inner" {
  count = 2
  input = "${var.seed}-${count.index}"
}
