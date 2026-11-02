# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Status page (`STATUS_ADDR`, default `:8080`) listing the synced hostnames.
- Trivy filesystem and image scans in CI, reported to the GitHub Security tab.
- Support for containers attached to different Docker networks.
- Portuguese translation of the README.
- Release process: version embedded in the binary and logged on startup,
  GitHub Releases with linux binaries and checksums.
