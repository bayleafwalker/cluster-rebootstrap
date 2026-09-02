# Releasing

`rebootstrap` ships as a pinned, standalone binary because G3 ("recovery
tooling is ready") is only meaningful if the tool that computed a plan
digest — and will later re-verify it during an actual rebuild — is itself a
fixed, reproducible artifact. An unversioned local build on a developer's
machine is not recovery tooling; a tagged release with a published checksum
is.

## Cutting a release

Releases are cut by an operator, never by an automated agent.

1. Confirm `main` is green: CI (`.github/workflows/ci.yml`) must be passing
   on the commit being released, including the site-vocabulary guard —
   `rebootstrap` must stay usable against any cluster, not just this one.
2. Update `CHANGELOG.md` with the new version's entry.
3. Tag the release commit and push the tag:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. Pushing the tag triggers `.github/workflows/release.yml`, which:
   - builds `linux/amd64`, `linux/arm64`, and `darwin/arm64` archives with
     GoReleaser (`CGO_ENABLED=0`, `-trimpath`, and the version linked in via
     `-ldflags -X .../internal/version.Version=vX.Y.Z`, so the binary is
     reproducible from the tagged source and `internal/version.Version`
     never reports the `dev` sentinel for a real release),
   - publishes each archive plus a `checksums.txt` covering all of them to
     the GitHub release for the tag,
   - attests build provenance for every published archive and for
     `checksums.txt`.
5. Confirm the release: `gh release view vX.Y.Z --json tagName,assets`.

## Recording the release in appservice

The `appservice` repo binds G3's `boundTo` evidence to two independent
facts about this release, both of which must be recorded — a version string
alone is not sufficient evidence:

1. **Exact version** — the `vX.Y.Z` tag, matched against what
   `rebootstrap version` reports on the box actually running recovery.
2. **Release asset SHA-256** — the checksum of the specific archive
   (matching the recovery machine's OS/arch) downloaded for that run, taken
   from the release's `checksums.txt` and re-verified against the
   downloaded file:

   ```sh
   gh release download vX.Y.Z -p 'rebootstrap_*_<os>_<arch>.tar.gz' -p checksums.txt
   sha256sum --check --ignore-missing checksums.txt
   ```

Record both the tag and the matching checksum line from `checksums.txt` as
the `boundTo` evidence in appservice's G3 record. Neither the tag name by
itself, nor a rebuilt-locally binary's checksum, can stand in for a
downloaded release asset that has been checked against the published
`checksums.txt` — that verification is what proves the binary the operator
is about to run is the one CI produced and attested, not a substitute.
