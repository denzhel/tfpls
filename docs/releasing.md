# Versions, upgrading and releasing

## For consumers

Call the shared workflows by **release tag**, never by `@main`:

```yaml
jobs:
  tofu:
    uses: OWNER/tfpls/.github/workflows/tofu.yml@v0
    secrets: inherit
```

| Reference | Meaning | Use when |
|---|---|---|
| `@v0` | newest `0.x.y` release (the tag moves with each minor/patch release) | you accept backwards-compatible updates automatically |
| `@v0.2.3` | exactly that release; the tag is never moved | you want changes only when you edit the file |
| `@<40-hex release commit SHA>` | exactly that commit of the workflow; its internal references use the release tag, so enable immutable releases on the repository to make a recreated tag impossible | highest assurance |

A release is self-contained: inside a release commit the workflows reference their own composite actions by the exact release tag (not `@main`), so `plan.yml@v0` runs the actions of that same release, not whatever is on `main`. Third-party actions are pinned by commit SHA in every release.

### Upgrade

1. Read the release notes for breaking changes (a new major version, `v1`, signals them).
2. Change the tag in your caller workflow (`@v0` -> `@v1`, or `@v0.2.3` -> `@v0.3.0`) in a pull request.
3. The PR plan run shows the new version working against your workspaces before you merge.

### Roll back

Change the tag back in the caller (`@v0.3.0` -> `@v0.2.9`) and merge. Exact tags are immutable, so the previous release behaves exactly as before. If a bad release was published, a maintainer deletes the bad exact tag and re-points the major tag (below).

## For maintainers: cutting a release

On a clean, pushed `main` with green CI:

```bash
tools/release.sh --dry-run v0.1.0     # shows how many internal references would be rewritten; changes nothing
tools/release.sh v0.1.0               # makes the release commit, tags v0.1.0, moves v0, pushes the tags (not main)
```

What it does: it creates a commit that is **not on `main`** in which every `OWNER/tfpls/...@main` reference inside `.github/workflows` and `actions` becomes `@v0.1.0`, tags that commit `v0.1.0` (annotated, never moved afterwards) and moves the major tag `v0` to it. Only tags are pushed. The script refuses to run on a dirty tree, off `main`, when `HEAD` differs from `origin/main`, or when the exact tag already exists.

Bad release: `git push --delete origin v0.1.0`, then move the major tag back to the previous release: `git tag -f -a v0 -m v0 v0.1.9 && git push --force origin refs/tags/v0`.

Third-party actions are pinned by SHA (`tests/pinning.test.sh` fails CI otherwise); updating them is a normal pull request.
