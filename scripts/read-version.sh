#!/bin/sh
# Print the authoritative Kit build version without changing any files.
set -eu

if [ ! -f VERSION ] || [ ! -r VERSION ]; then
  echo 'VERSION must be a readable file containing one SemVer value' >&2
  exit 1
fi

# Some awk implementations discard NUL bytes; translate them to an invalid
# SemVer character so malformed binary content cannot become a valid version.
LC_ALL=C tr '\000' '?' < VERSION | LC_ALL=C awk '
  BEGIN {
    number = "(0|[1-9][0-9]*)"
    identifier = "(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    prerelease = "(-" identifier "(\\." identifier ")*)?"
    metadata = "(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?"
    semver = "^" number "\\." number "\\." number prerelease metadata "$"
  }
  NR == 1 { version = $0 }
  END {
    if (NR != 1 || version !~ semver) {
      print "VERSION must contain exactly one SemVer value without whitespace or a v prefix" > "/dev/stderr"
      exit 1
    }
    print version
  }
'
