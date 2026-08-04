variable "name_prefix" {
  description = "Prefix applied to the bucket name."
  type        = string
}

variable "purpose" {
  description = "What the bucket holds; becomes part of its name and its Purpose tag."
  type        = string
}

variable "tags" {
  description = "Tags merged onto the bucket."
  type        = map(string)
  default     = {}
}

variable "versioning_enabled" {
  description = "Whether object versioning is on."
  type        = bool
  default     = true
}

variable "retention_rules" {
  description = "Lifecycle expiration rules."
  type        = list(object({ id = string, days = number }))
  default     = []
}

# Nothing references this. It is here so the orphan detector has something true to find.
variable "unused_legacy_flag" {
  description = "Left over from the pre-migration module. Referenced nowhere."
  type        = bool
  default     = false
}
