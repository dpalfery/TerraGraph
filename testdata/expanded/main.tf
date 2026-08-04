module "fleet" {
  source   = "./child"
  for_each = toset(["eu", "us"])
  seed     = each.value
}
