"""Log in before Compose without putting the registry token in arguments or logs."""
import json
from pathlib import Path
import subprocess
import sys


def main():
    try:
        registry = json.loads(Path("registry.json").read_text())
        if registry == {}:
            return
        host, username = registry["host"], registry["username"]
        if not isinstance(host, str) or not isinstance(username, str) or not host or not username:
            raise ValueError("invalid registry login settings")
        with Path("secrets/registry_token").open("rb") as token:
            if not token.read().strip():
                raise ValueError("empty registry token")
            token.seek(0)
            subprocess.run(["docker", "login", host, "--username", username, "--password-stdin"],
                           stdin=token, check=True)
    except subprocess.CalledProcessError:
        sys.exit("Docker registry login failed; Compose was not started")
    except (OSError, ValueError, KeyError, TypeError):
        sys.exit("Registry login requires registry.json with a host and username and a nonempty secrets/registry_token file")


if __name__ == "__main__":
    main()
