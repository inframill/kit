terraform {
  required_version = "= 1.11.5"
  required_providers {
    hcloud = { source = "hetznercloud/hcloud", version = "= 1.68.0" }
    cloudflare = { source = "cloudflare/cloudflare", version = "= 5.27.0" }
  }
}
