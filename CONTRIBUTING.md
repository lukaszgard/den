# Contributing to den

Thanks for helping. This page is the short version; [AGENTS.md](AGENTS.md) is the full
guide to the code, its conventions and its tests, and it is written for people as well
as for coding agents.

## Set up

```sh
git clone https://github.com/lukaszgard/den.git
cd den
make check-fast        # build, vet and the tests that do not start slow lifecycles
make check             # what CI runs: build, vet, lint, race tests, govulncheck
```

Go is pinned in `go.mod`; an older `go` (1.21 or newer) downloads the toolchain by
itself. Run den from the checkout with `go run ./cmd/den -c den.yaml`. The repository's
`den.yaml` is an annotated example for a fictional company; its AWS profiles do not exist
on your machine, so point a copy of it at your own account to connect something.

## Changes

- Open an issue first for anything bigger than a fix, so the direction is agreed before
  you write it.
- Commit messages are conventional: `<type>: <summary>` with type `feat`, `fix`,
  `refactor`, `perf`, `test`, `docs`, `build` or `ci`, and `!` after the type for a
  breaking change. The versions den is released with follow from these types
  (`make release-next` prints the next one).
- Tests go with the change: lifecycles run real processes against fakes on `PATH`, the TUI
  is driven through `AppModel.Update`, and `make cover` holds the per-package coverage
  floors. Raise a floor when you add tests; never lower one.
- A new feature ships with its documentation: `docs/<feature>/README.md`, its in-app Help
  topic in `docs/help/` and a VHS tape. A `den.yaml` change also needs `make generate`
  (CI fails on a stale schema, configuration reference or command reference).
- Never commit anything that names a real account, host or person: the examples use
  `acme`, `example.com` and the AWS documentation's `111122223333`.

## Reporting a security problem

Not here: see [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
