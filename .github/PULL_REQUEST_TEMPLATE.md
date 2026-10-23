## What and why

<!-- The change, and the problem it solves. Link the issue: Fixes #123 -->

## Checklist

- [ ] `make check` passes locally.
- [ ] A test fails without the change and passes with it; for a security control, the test also fails when the control is removed.
- [ ] `CHANGELOG.md` has an entry under `[Unreleased]` for user-visible changes.
- [ ] Docs are updated where behavior changed.
- [ ] Commit messages and the pull request title follow [Conventional Commits](https://www.conventionalcommits.org/). The title becomes the squash commit on `master`.
- [ ] No secrets, tokens or private code in the diff or the tests.
