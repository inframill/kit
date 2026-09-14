output "server_ipv4_addresses" {
  description = "Public IPv4 address of each server, by name."
  value       = { for k, s in hcloud_server.this : k => s.ipv4_address }
}

output "server_private_ipv4_addresses" {
  description = "Private IPv4 addresses of each server, by logical Network name."
  value = {
    for node_name, node in var.nodes : node_name => {
      for network_name in node.private_networks :
      network_name => hcloud_server_network.this["${node_name}/${network_name}"].ip
    }
  }
}

output "private_network_ids" {
  description = "Hetzner Network IDs by logical Network name."
  value       = { for name, network in hcloud_network.this : name => network.id }
}

output "inframill_nodes" {
  description = "Provider-independent Node contract consumed by Inframill."
  value = {
    for name, server in hcloud_server.this : name => {
      name         = name
      fqdn         = null
      public_ipv4  = server.ipv4_address
      private_ipv4 = length(var.nodes[name].private_networks) > 0 ? hcloud_server_network.this["${name}/${var.nodes[name].private_networks[0]}"].ip : ""
      roles        = var.nodes[name].roles
    }
  }
}
