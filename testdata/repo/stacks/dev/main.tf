provider "aws" {
  region = "us-west-2"
}

module "artifacts_bucket" {
  source = "../../modules/bucket"

  name_prefix        = var.environment
  purpose            = "artifacts"
  versioning_enabled = false
}

# Still on the 4.x line. terra_modules should surface this against prod's 5.1.0.
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.2"

  name = "${var.environment}-vpc"
  cidr = "10.1.0.0/16"
}

resource "aws_s3_bucket" "logs" {
  bucket = "acme-dev-logs"
}
