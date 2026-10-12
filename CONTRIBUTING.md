# Contributing to Heartbeat

Thank you for helping. Heartbeat is licensed under the Apache License 2.0 and contributions are accepted under the same licence, with a Developer Certificate of Origin (DCO) sign-off instead of a contributor licence agreement.

## Sign off your commits (required)

Every commit must carry a `Signed-off-by` line with your real name and email address:

```
Signed-off-by: Jane Developer <jane@example.com>
```

Add it automatically with `git commit -s`. If you forget, fix the last commit with `git commit --amend -s`, or a series with `git rebase --signoff <base>`. Pull requests with unsigned commits cannot be merged.

By signing off you certify the Developer Certificate of Origin, version 1.1:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

## Before you start

- For anything larger than a small fix, open an issue first so the approach can be agreed.
- Security problems: do not file a public issue. Contact the maintainers privately (see [docs/security.md](docs/security.md)).
- Do not copy code from projects whose licence is incompatible with Apache-2.0 (GPL, AGPL, LGPL, SSPL, source-available licences).

## Development workflow

1. Read [docs/development.md](docs/development.md) for the layout, build and test commands.
2. Branch from `main`, keep each pull request to one logical change, and write commit messages that say why.
3. Run `go vet ./...`, `gofmt -l .` and `go test ./...`; for UI changes also `npm run typecheck` in `web/`.
4. Add tests with the change: Go tests next to the code, and for protocol or API changes extend `server/app/integration_test.go`.
5. Update the documentation under `docs/` when behaviour, settings, endpoints or defaults change. The REST API is described by `server/app/routes.go`; add every endpoint there.
6. Database changes need a new migration in `server/db/migrations/` (never edit an applied one). Protocol changes follow the rules in `proto/heartbeat/v1/agent.proto` (add fields, do not renumber; bump `SchemaVersion` for breaking changes) and regenerate `gen/`.

## Dependencies and licences

New dependencies must be under a permissive licence (MIT, BSD, ISC, Apache-2.0, Zlib and similar). MPL-2.0 is acceptable when it is listed in `NOTICE`. GPL, AGPL, LGPL, SSPL and unlicensed packages are not accepted. Update `NOTICE` when you add or upgrade a dependency (commands are at the end of that file).

## Security-sensitive areas

Changes to enrolment, agent authentication, job signing, the agent `priv` helper, the sudo rule, RBAC or tenant isolation get extra review. Keep the privileged helper's input validation strict, never widen the sudo rule, and add tests for the negative cases.

## Code style

Go code is `gofmt`-formatted and uses the standard library where reasonable. The UI is TypeScript with strict type checking. Keep comments for the non-obvious "why".
