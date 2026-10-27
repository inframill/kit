"""Probe the authenticated HTTPS API through the container's loopback interface."""
import json
from pathlib import Path
import ssl
import sys
import urllib.request

try:
    context = ssl.create_default_context()
    context.load_verify_locations(cafile="/etc/inframill/tls/fullchain.pem")
    # The deployed certificate names the public endpoint, rather than loopback.
    # Retain certificate-chain verification for this local readiness probe.
    context.check_hostname = False
    token = Path("/run/secrets/inframill_root_token").read_text().rstrip("\n")
    request = urllib.request.Request(
        "https://127.0.0.1:443/inframill.v1.EngineService/GetServerVersion",
        data=b"{}", headers={"Content-Type": "application/json",
                              "Connect-Protocol-Version": "1", "X-Root-Password": token})
    with urllib.request.urlopen(request, context=context, timeout=3) as response:
        if not json.load(response).get("version"):
            raise ValueError("missing server version")
except Exception:
    sys.exit("Inframill authenticated HTTPS readiness check failed")
