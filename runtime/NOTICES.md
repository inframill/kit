# Kit image distribution notices

This image combines separately licensed programs. It is not an all-MIT product.

* Inframill Kit retains its LICENSE in `/usr/share/inframill/sources/kit/`.
  Inframill is copied from the sibling project's prebuilt release. Its version,
  source revision and binary checksums are recorded in the bundle lock. Available
  Go dependency notices are under `sources/kit/go-dependency-notices/`; the
  Inframill source tree is not embedded in the image.
* OpenTofu is MPL-2.0. Its matching source archive and license accompany the image.
* Ansible Core is GPL-3.0-or-later. Its matching source distribution is included
  under `/usr/share/inframill/sources/`, together with the source distributions of
  its pinned Python dependencies. The callback is Kit-authored MIT code.
* Python and Debian retain the copyright/license files shipped in the base image.
  Debian package identities are recorded in `/usr/share/inframill/os-packages.txt`;
  matching package sources are available from the signed snapshot repositories
  configured in `/etc/apt/sources.list`. Python's version and immutable base image
  digest are recorded in the bundle lock; upstream source: https://www.python.org/ftp/python/.

The bundle lock and Python requirements record exact versions and checksums.
Review changes to these materials and dependency license terms before distributing
new versions. Guest packages installed by a blueprint are separate from the Kit image.
