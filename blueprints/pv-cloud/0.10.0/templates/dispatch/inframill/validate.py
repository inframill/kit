"""Validate externally provisioned TLS material and token without printing them."""
from pathlib import Path
import ssl
import sys

try:
    token = Path("secrets/root_token").read_text().rstrip("\n")
    if not token.strip() or any(ord(char) < 32 or ord(char) > 126 for char in token):
        raise ValueError("invalid token")
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain("tls/fullchain.pem", "secrets/tls_private_key", password=lambda: "")
except (OSError, ValueError, ssl.SSLError):
    sys.exit("Inframill requires a nonempty single-line root token and matching PEM certificate/private key")
