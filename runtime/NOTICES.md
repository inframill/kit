# Kit image distribution notices

The image combines separately licensed programs. Kit and the resolved engine's
corresponding sources are in `/usr/share/inframill/sources/kit.tar.gz`; available Go
dependency notices are under `/usr/share/inframill/notices/`. The shared provider
executable is `/usr/local/bin/kit-exec`. `/usr/share/inframill/manifest.json` records
engine/provider identity and binary checksums.

OpenTofu is MPL-2.0. Its matching source archive and license accompany the image.
Ansible Core is GPL-3.0-or-later. Its matching source distribution and the sources
of its pinned Python dependencies are under `/usr/share/inframill/sources/`.
The callback is Kit-authored MIT code. Installed Python packages and source checksums
are recorded in `python-packages.json` and `sources/python-sources.json` respectively.

Python and Debian retain their shipped license/copyright files. The base image,
Debian snapshot, external tool versions and download hashes are recorded in the
included `runtime/dependencies.json`. OS packages are listed in
`/usr/share/inframill/os-packages.txt`; package sources are available through the
configured signed snapshot repositories. Python source: https://www.python.org/ftp/python/.

Guest packages installed by blueprints are separate from the Kit image. Review
updated dependency terms when distributing new runtime versions.
