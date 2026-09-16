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
}
