# Release v0.1.2

Security:

- Go v1.23.6 fixes CVE-2025-22866. ([PR 164][pr-164])

Features:

- Handle blobs in an index for GC. ([PR 131][pr-131])
- Switch to stdlib slog. ([PR 144][pr-144])
- Add http req/resp trace logs. ([PR 145][pr-145])
- Add cobra command for documentation. ([PR 161][pr-161])

Fixes:

- Handle a referrer exceeding the size limit. ([PR 138][pr-138])
- Avoid race with GC in tests. ([PR 163][pr-163])

Chores:

- Update version-bump configuration to new processor syntax. ([PR 135][pr-135])
- Remove OpenSSF Scorecard workflow and badge. ([PR 137][pr-137])
- Add tests for referrers delete. ([PR 141][pr-141])
- Reduce testing overhead. ([PR 142][pr-142])
- Improve test logging. ([PR 147][pr-147])
- Add project logo. ([PR 159][pr-159])

Contributors:

- @sudo-bmitch

[pr-131]: https://github.com/olareg/olareg/pull/131
[pr-135]: https://github.com/olareg/olareg/pull/135
[pr-137]: https://github.com/olareg/olareg/pull/137
[pr-138]: https://github.com/olareg/olareg/pull/138
[pr-141]: https://github.com/olareg/olareg/pull/141
[pr-142]: https://github.com/olareg/olareg/pull/142
[pr-144]: https://github.com/olareg/olareg/pull/144
[pr-145]: https://github.com/olareg/olareg/pull/145
[pr-147]: https://github.com/olareg/olareg/pull/147
[pr-159]: https://github.com/olareg/olareg/pull/159
[pr-161]: https://github.com/olareg/olareg/pull/161
[pr-163]: https://github.com/olareg/olareg/pull/163
[pr-164]: https://github.com/olareg/olareg/pull/164
