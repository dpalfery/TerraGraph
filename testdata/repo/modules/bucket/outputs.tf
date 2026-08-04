output "bucket_id" {
  description = "The bucket name."
  value       = aws_s3_bucket.this.id
}

output "bucket_arn" {
  description = "The bucket ARN."
  value       = aws_s3_bucket.this.arn
}

# No caller consumes this one.
output "versioning_status" {
  description = "Resolved versioning status."
  value       = aws_s3_bucket_versioning.this.versioning_configuration[0].status
}
