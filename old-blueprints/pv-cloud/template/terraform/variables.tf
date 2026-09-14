variable "hcloud_token" {
  description = "Hetzner Cloud API token for this root's configured project."
  type        = string
  sensitive   = true
}

variable "ssh_source_ips" {
  description = "Public CIDRs allowed to connect directly over SSH."
  type        = list(string)
  default     = ["0.0.0.0/0", "::/0"]
}

variable "private_networks" {
  description = "Named private Networks and their IPv4 CIDRs."
  type = list(object({
    name = string
    cidr = string
  }))
}

variable "nodes" {
  description = "PV Cloud nodes and their Ansible roles, keyed by server name."
  type = map(object({
    location         = string
    server_type      = string
    roles            = list(string)
    private_networks = list(string)
  }))
}
