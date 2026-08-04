output "artifacts_bucket_arn" {
  description = "ARN of the artifacts bucket, consumed by the CI pipeline."
  value       = module.artifacts_bucket.bucket_arn
}

output "logs_bucket_id" {
  description = "Name of the audit log bucket."
  value       = aws_s3_bucket.logs.id
}
