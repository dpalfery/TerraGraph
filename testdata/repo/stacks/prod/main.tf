terraform {
  required_version = ">= 1.6"

  backend "s3" {
    bucket = "acme-tfstate"
    key    = "prod/terraform.tfstate"
    region = "us-west-2"
  }
}

provider "aws" {
  region = var.region
}

# A second provider for the replication target.
provider "aws" {
  alias  = "replica"
  region = "us-east-1"
}

# The audit trail lands in aws_s3_bucket.logs — see the CloudTrail runbook before
# touching aws_s3_bucket.logs, because the retention policy is contractual.
#
# Neither mention above is a reference. A grep for "aws_s3_bucket.logs" finds them and
# cannot tell them from the real one on line 44.
resource "aws_s3_bucket" "logs" {
  bucket = "acme-prod-logs"

  tags = local.common_tags
}

resource "aws_s3_bucket_policy" "logs" {
  # This one IS a reference — a traversal, not text.
  bucket = aws_s3_bucket.logs.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = "s3:PutObject"
      # A quoted string that happens to spell an address. Also not a reference.
      Resource = "arn:aws:s3:::aws_s3_bucket.logs/*"
    }]
  })
}

module "artifacts_bucket" {
  source = "../../modules/bucket"

  name_prefix = var.environment
  purpose     = "artifacts"
  tags        = local.common_tags
}

module "backups_bucket" {
  source = "../../modules/bucket"

  name_prefix        = var.environment
  purpose            = "backups"
  tags               = local.common_tags
  versioning_enabled = false

  retention_rules = [
    { id = "expire-old", days = 90 },
  ]
}

data "aws_caller_identity" "current" {}

resource "aws_s3_bucket" "replica" {
  provider = aws.replica

  bucket = "acme-prod-replica-${data.aws_caller_identity.current.account_id}"
  tags   = local.common_tags
}

locals {
  common_tags = {
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# The bucket was renamed when the logging stack was split out.
moved {
  from = aws_s3_bucket.audit
  to   = aws_s3_bucket.logs
}
