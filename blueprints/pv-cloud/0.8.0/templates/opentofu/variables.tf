variable "ssh_source_ips" {
  type = list(string)
  validation {
    condition = alltrue([for cidr in var.ssh_source_ips : can(cidrhost(cidr, 0))])
    error_message = "SSH sources must be valid CIDRs."
  }
}
variable "private_networks" { type = list(object({name = string, cidr = string})) }
variable "nodes" {
  type = map(object({location=string, server_type=string, roles=list(string), private_networks=list(string), architecture=string, image=string}))
  validation {
    condition = alltrue([for name in keys(var.nodes) : !contains(["all", "ungrouped"], name)])
    error_message = "Node names all and ungrouped are reserved for inventory groups."
  }
  validation {
    condition = alltrue([for name in keys(var.nodes) : can(regex("^[a-z]([a-z0-9-]{0,34}[a-z0-9])?$", name))])
    error_message = "Node names must be DNS labels of at most 36 characters, leaving room for the Instance ID."
  }
}
variable "cloudflare_zone_id" {
  type = string
  validation {
    condition = can(regex("^[a-f0-9]{32}$", var.cloudflare_zone_id))
    error_message = "Cloudflare zone ID must be 32 lowercase hexadecimal characters."
  }
}
