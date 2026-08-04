# Illustrative only. Indexed, but demoted in ranking: it is not the operative config.
module "bucket" {
  source = "../../modules/bucket"

  name_prefix = "example"
  purpose     = "demo"
}
