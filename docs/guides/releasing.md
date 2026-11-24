# Branches and releases

How changes reach a release. Versioning itself, and what counts as a
breaking change, is described in the README's
[Versioning](../../README.md#versioning) section.

Changes move through three long-lived branches:

| Branch | Takes pull requests from | On every push |
|---|---|---|
| `main` (default) | Feature branches such as `feat/...` and `fix/...`, merged by the maintainer once reviewed | CI |
| `stage` | `main` only | CI, then the next release candidate is tagged and published |
| `prod` | `stage` only | CI, then the release is tagged and published |

A required `Promotion source` check refuses pull requests to `stage` or `prod` from any other branch. Both take merge commits only, so each holds exactly the commits of the branch before it and the next promotion merges cleanly.

To release:

1. On a branch from `main`, move the **Unreleased** entries in [`CHANGELOG.md`](../../CHANGELOG.md) under a new heading `## [X.Y.Z] - YYYY-MM-DD`, for the version being released, and merge it.
2. Run `mise run promote-stage`. It opens the pull request from `main` to `stage`, named after the candidate it will produce, and stops if the version has already been released, which means step 1 was missed. Once it is merged, the [stage workflow](../../.github/workflows/stage.yml) tags the next candidate, `vX.Y.Z-rc.1` for the first, and publishes it as a pre-release.
3. Try the candidate. To fix something, merge the fix into `main` and promote again; each merge into `stage` tags the next candidate, `vX.Y.Z-rc.2` and so on.
4. Run `mise run promote-prod`. It opens the pull request from `stage` to `prod`, after checking that `stage` is exactly the newest candidate. Once it is merged, the [production release workflow](../../.github/workflows/prod.yml) checks the same again, tags `vX.Y.Z`, and publishes the release.

Both commands print the open pull request instead if there already is one. They run the GitHub CLI, which mise installs for them, as you, so pull requests they open run CI like any other; log in once with `gh auth login`.

Publishing is done by the [release workflow](../../.github/workflows/release.yml): it runs the checks, builds archives for Linux, macOS and Windows on amd64 and arm64, publishes a GitHub release with checksums, and pushes the container image. Candidates become pre-releases and never move the image's `latest` tag. Pushing a `v*` tag by hand still runs it, for emergencies.

Tags are never moved or deleted once pushed.
