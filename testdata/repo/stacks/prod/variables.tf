variable "environment" {
  description = "Deployment environment name, used as the resource name prefix."
  type        = string
  default     = "prod"
}

variable "region" {
  description = "Primary AWS region."
  type        = string
  default     = "us-west-2"
}
