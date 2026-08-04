# Pinned one major ahead of dev. This skew is the fixture for terra_modules.
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"

  name = "${var.environment}-vpc"
  cidr = "10.0.0.0/16"
}
