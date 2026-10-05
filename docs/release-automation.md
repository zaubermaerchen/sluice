# Release automation

The [release workflow](../.github/workflows/release.yml) publishes sluice archives
when a `v*` tag is pushed. Stable tags matching `vX.Y.Z` also propose an update
to `zaubermaerchen/homebrew-tap`. The tap PR requires maintainer review and merge;
it is not merged automatically.

## Shared Homebrew workflow

Follow the tap's [release automation guide](https://github.com/zaubermaerchen/homebrew-tap/blob/main/docs/release-automation.md)
for the GitHub App setup, trusted workflow contract, and tap-side recovery.
The caller follows [khsier #26](https://github.com/zaubermaerchen/khsier/pull/26)
and [outage #68](https://github.com/zaubermaerchen/outage/pull/68).

The sluice repository must have:

- Actions variable `HOMEBREW_TAP_APP_ID`.
- Actions secret `HOMEBREW_TAP_APP_PRIVATE_KEY`.
- The App installation and permissions described in the shared guide.

Both the reusable workflow reference and `automation-ref` are pinned to
`18a30289f199e21a6ddd1d5be1b57f6249fe0622`. Update them together only after reviewing
the new tap implementation. The caller passes `formula: sluice`, the published
tag, and App credentials, with `contents: read`. Keep formula generation and
installation checks in the tap workflow instead of copying them here.

## Release procedure

1. Merge the release preparation changes and confirm the main CI jobs pass
   on Ubuntu, macOS, Windows, and with the race detector. Run the repository's
   local checks and relevant cross-build checks before tagging.
2. Prepare release notes, including any observable compatibility changes.
   The workflow generates notes automatically; use
   `gh release edit vX.Y.Z --notes-file <notes-file>` to supply the reviewed
   notes after publication. For v0.2.2, explain that block-mode EOF while
   CLOSED waits until OPEN and can wait indefinitely without a future OPEN event.
3. Create the release tag on the intended merged commit and push it. The
   workflow tests and vets the source, builds seven archives, verifies checksums
   and tag provenance, and publishes the release. Binary version information
   is set from the tag through `-X main.version`; no source version bump is needed.
4. Confirm all seven archives and `SHA256SUMS` are published. Verify downloaded
   archive checksums and run a binary for the host platform with `--version`.
5. Confirm the Homebrew `prepare`, Linux and macOS `brew-test`, and `pull-request`
   jobs succeed. The shared workflow validates the generated formula before
   opening a tap PR. Link that PR from the release issue and track its review
   and merge.

The tap consumes these four archives from the published release and their
entries in `SHA256SUMS`:

- `sluice-vX.Y.Z-darwin-amd64.tar.gz`
- `sluice-vX.Y.Z-darwin-arm64.tar.gz`
- `sluice-vX.Y.Z-linux-amd64.tar.gz`
- `sluice-vX.Y.Z-linux-arm64.tar.gz`

The release also contains Linux armv6 and Windows amd64/arm64 archives.
After the formula is merged, installation is:

```sh
brew install zaubermaerchen/tap/sluice
```

## Failure handling

A failed Homebrew job does not undo the already-published GitHub release.
Check the failing job and shared guide before retrying. Missing assets,
checksum mismatches, or installation failures must be resolved before accepting
the formula update. App authentication failures require checking the App
installation, variable, secret, and permissions; never print the private key.

The release creation step is not an idempotent retry mechanism for an existing
release. Do not delete or move a published tag to retrigger automation. For a
partial failure, use the tap guide's recovery procedure and record the release
and follow-up PR status in the release issue.

The shared guide's khsier-first rollout describes the initial deployment plan.
The shared caller has since been exercised by the
[outage v0.5.2 release](https://github.com/zaubermaerchen/outage/actions/runs/37219712269),
including Linux and macOS installation checks and the merged
[tap PR #2](https://github.com/zaubermaerchen/homebrew-tap/pull/2).
