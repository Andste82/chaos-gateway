# Releasing

How a release of Chaos Gateway is made. It is for the maintainers; users read the [installation guide](install.md). A release is a tag
`vMAJOR.MINOR.PATCH` on a commit of `main`. Pushing the tag starts `.github/workflows/image.yml`, which builds the multi-arch image
(linux/amd64 and linux/arm64), pushes it to `ghcr.io/andste82/chaos-gateway` and makes the GitHub release with the files of the deployment.
Nothing else is needed: the workflow uses the `GITHUB_TOKEN` of the job and has no secret.

## Before the tag

1. `main` is green in CI at the commit you tag: every job of the `CI` workflow, the level 1 and level 1b jobs included.
2. The last scheduled **Nightly** is green, or a manual run on that commit is: it runs the level 2 tests on clean Ubuntu 24.04 and 26.04
   hosts (`gh workflow run nightly.yml`, add `-f fuzz=true` for the fuzz job). A release that was never installed on a clean host is not tested.
3. `docs/release-notes/vMAJOR.MINOR.PATCH.md` exists and is true: it names what the release contains and what is not validated. The release
   uses that file as its text; without it the text is generated from the commits and the workflow says so as a warning. Release candidates
   need no notes.
4. `docs/plan.md` and `docs/open-items.md` say what the release is (the status line, the open items that stay open).

## The first release, and every release after it: a candidate first

The workflow has run only on branches so far; a tag is its first real run. Publish a **release candidate** first. The candidate takes the
same path as the release, but it is marked as a pre-release on GitHub and does not move the tag `latest`:

```sh
git switch main && git pull --ff-only
git tag -a v1.0.0-rc.1 -m "Chaos Gateway 1.0.0-rc.1"
git push origin v1.0.0-rc.1
```

What the workflow does for a tag, in this order, and what stops it:

| Step | Stops the release when |
|---|---|
| `deploy/release-check.sh` | the tag is not `vMAJOR.MINOR.PATCH` (with an optional `-rc.1`), or its commit is not on the history of `main` |
| smoke test of the amd64 image and of the arm64 image (under qemu-user) | a binary or a tool does not start, a pinned version is not the pinned one, Kea does not accept its configuration |
| build and push of the multi-arch image | the build fails |
| check of the manifest | the pushed image does not hold both platforms |
| release and upload | none: the release is made if it does not exist, and the files are replaced if they do |

It pushes `:1.0.0-rc.1` for the candidate. For a release `v1.2.3` it pushes `:1.2.3`, `:1.2`, `:1` (no `:0` for a `v0.x`) and `:latest`.
The files `compose.yaml`, `host-setup.sh`, `install.sh`, `update.sh`, `uninstall.sh`, `env.example` and `SHA256SUMS` are attached to the release.

### The package must be made public once

The first push creates the package `chaos-gateway` on ghcr.io as **private**. The one-line installation of the guide pulls without a login, so
make the package public once: on GitHub, the repository's *Packages* > `chaos-gateway` > *Package settings* > *Change visibility* > *Public*.
Until then `docker pull` needs `docker login ghcr.io` with a token that has `read:packages`. The image carries the label
`org.opencontainers.image.source`, which links the package to the repository.

### Check the candidate

```sh
gh run list --workflow image.yml --limit 1                          # the run of the tag is green
docker buildx imagetools inspect ghcr.io/andste82/chaos-gateway:1.0.0-rc.1      # linux/amd64 and linux/arm64
gh release view v1.0.0-rc.1                                          # a pre-release, with the seven files
```

Then install it on a clean Ubuntu host as a user would, and run through the user guide's first steps:

```sh
curl -fsSL https://github.com/Andste82/chaos-gateway/releases/download/v1.0.0-rc.1/install.sh | sudo sh -s -- --version 1.0.0-rc.1 --test-interface lan0
```

When the candidate is good, tag the release on the same commit (or on a later commit of `main` with the fixes):

```sh
git tag -a v1.0.0 -m "Chaos Gateway 1.0.0" v1.0.0-rc.1^{commit}
git push origin v1.0.0
```

## A release that is wrong

A tag that was published is never moved or reused: people have pulled the image. Withdraw it and publish the next patch version.

```sh
gh release delete v1.0.1 --yes               # the release and its files; the tag stays until you delete it
git push origin :refs/tags/v1.0.1
```

The image versions are deleted on GitHub, under the package's *Package settings* (or with the REST API `DELETE /user/packages/container/chaos-gateway/versions/ID`);
`:latest` is moved by the next release, and until then a pull of `latest` gives the version you withdrew unless you re-run the workflow of the
previous tag (*Actions* > *Image* > *Re-run*). Hosts that have the bad version go back with
`sudo /opt/chaosgw/update.sh --version OLD`, as long as the configuration was not migrated to a newer schema (the update script refuses a
configuration that the old version cannot read).

A run of the workflow that failed in the middle is started again with *Re-run failed jobs*: the release is made only if it does not exist and its
files are replaced.
