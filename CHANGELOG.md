# Changelog

All notable changes to this project are documented in this file.

## v0.1.0

First pinned, standalone release of the `rebootstrap` CLI. Ships as
reproducible `linux/amd64`, `linux/arm64`, and `darwin/arm64` archives with
published `checksums.txt`, so a clean-room recovery machine with no Go
toolchain can run recovery tooling whose version and integrity are both
verifiable — see `RELEASING.md`.

Command surface shipped in this release:

- `rebootstrap profile validate`
- `rebootstrap gate evaluate`
- `rebootstrap status`
- `rebootstrap report`
- `rebootstrap reconcile`
- `rebootstrap plan validate`
- `rebootstrap plan digest`
- `rebootstrap plan render`
- `rebootstrap plan bind`
- `rebootstrap plan dry-run`
- `rebootstrap version`
