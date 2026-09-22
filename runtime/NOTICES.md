# Kit image distribution notices

This image combines separately licensed programs. It is not an all-MIT product.

* Kit's image assembly sources and LICENSE are under `/usr/share/inframill/sources/kit/`.
  Each provider ships its corresponding sources and LICENSE beside the executable
  in `/opt/inframill/providers/<name>/<version>/sources/`. Available Go dependency
  notices, including those for the pinned engine, are in the adjacent `notices/`.
  Inframill is copied from its prebuilt release. The bundle lock records its
  version, source revision and binary checksums; its source tree is not embedded.
* OpenTofu is MPL-2.0. Its matching source archive and license accompany the image.
* Ansible Core is GPL-3.0-or-later. Its matching source distribution is included
  under `/usr/share/inframill/sources/`, together with the source distributions of
  its pinned Python dependencies. The callback is Kit-authored MIT code.
* Python and Debian retain the copyright/license files shipped in the base image.
  Debian package identities are recorded in `/usr/share/inframill/os-packages.txt`;
  matching package sources are available from the signed snapshot repositories
  configured in `/etc/apt/sources.list`. Python's version and immutable base image
  digest are pinned in the included `runtime/Dockerfile`; upstream source: https://www.python.org/ftp/python/.

The bundle lock records engine and provider selections. The Dockerfile pins runtime
tools; `/usr/share/inframill/python-packages.json` records installed Python packages
and download hashes, and `sources/python-sources.json` records their source checksums.
Review changes to these materials and dependency license terms before distributing
new versions. Guest packages installed by a blueprint are separate from the Kit image.
