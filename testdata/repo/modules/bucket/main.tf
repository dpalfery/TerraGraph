# Encrypted S3 bucket with a retention lifecycle. Shared by every stack.

resource "aws_s3_bucket" "this" {
  bucket = "${var.name_prefix}-${var.purpose}"

  tags = local.tags
}

resource "aws_s3_bucket_versioning" "this" {
  bucket = aws_s3_bucket.this.id

  versioning_configuration {
    status = var.versioning_enabled ? "Enabled" : "Suspended"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "this" {
  bucket = aws_s3_bucket.this.id

  dynamic "rule" {
    for_each = var.retention_rules

    content {
      id     = rule.value.id
      status = "Enabled"

      expiration {
        days = rule.value.days
      }
    }
  }
}

locals {
  tags = merge(var.tags, {
    Purpose = var.purpose
  })
}
