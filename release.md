# Release v0.1.1

Security updates:

- The update to Go 1.22.3 fixed CVE-2024-24788. ([PR 102][pr-102])
- The update to Go 1.22.4 fixed CVE-2024-24790. ([PR 112][pr-112])  

Breaking:

- Disable delete APIs by default. ([PR 106][pr-106])

Experimental features:

- Add fields to manifest put and blob create to facilitate changing the digest algorithm. ([PR 118][pr-118])

Features:

- Add an `olareg version` sub-command. ([PR 89][pr-89])
- Include OCI Conformance tests in GitHub actions and the Makefile. ([PR 91][pr-91])
- Add prune callback to cache package. ([PR 92][pr-92])
- Add a configurable limit on the number of concurrent uploads to a repository. ([PR 97][pr-97])
- Refactor uploads in the dir store to use the cache package, allowing limits per repo. ([PR 98][pr-98])
- Add SIGUSR1 handler to output a stack trace. ([PR 100][pr-100])
- Use caching for repositories in dir store. ([PR 103][pr-103])
- Add a context on RepoGet. ([PR 105][pr-105])
- Debug logs on store changes. ([PR 107][pr-107])
- Improve locking of uploads. ([PR 109][pr-109])
- Remove debug response on 404. ([PR 111][pr-111])
- Allow changing the digest algorithm on blob push. ([PR 119][pr-119])
- Add option for warning headers. ([PR 126][pr-126])
- Add support for rate limits. ([PR 127][pr-127])

Fixes:

- Override the Go version used by the OSV Scanner. ([PR 84][pr-84])
- Set `workdir` and `volume` in Dockerfile. ([PR 85][pr-85])
- Logging of delete by tag and copying descriptors. ([PR 108][pr-108])
- Improve support for changing the digest algorithm and validate the digest to prevent panics. ([PR 118][pr-118])
- Verify digest algorithm is available before attempting to use it. ([PR 120][pr-120])
- Include missing imports needed for go-digest. ([PR 121][pr-121])

Other changes:

- Add examples of `olareg.New` in Go docs. ([PR 86][pr-86])
- Update readme with installation details. ([PR 87][pr-87])
- Limit token permission on the coverage action. ([PR 88][pr-88])
- Add example text for `olareg serve` command. ([PR 90][pr-90])
- Refactor blob upload in the mem store to use the cache package. ([PR 93][pr-93])
- Reenable weekly OSV Scanner check in GitHub Actions. ([PR 95][pr-95])
- Remove broken coverage report from GitHub actions. ([PR 96][pr-96])
- Refactor dir storage of repos to use the cache package. ([PR 99][pr-99])
- Improve error handling. ([PR 110][pr-110])
- Fix Dockerfile linter warnings. ([PR 117][pr-117])
- Upgrade staticcheck for Go 1.23 support and fix lint warnings. ([PR 125][pr-125])

Contributors:

- @sudo-bmitch

[pr-84]: https://github.com/olareg/olareg/pull/84
[pr-85]: https://github.com/olareg/olareg/pull/85
[pr-86]: https://github.com/olareg/olareg/pull/86
[pr-87]: https://github.com/olareg/olareg/pull/87
[pr-88]: https://github.com/olareg/olareg/pull/88
[pr-89]: https://github.com/olareg/olareg/pull/89
[pr-90]: https://github.com/olareg/olareg/pull/90
[pr-91]: https://github.com/olareg/olareg/pull/91
[pr-92]: https://github.com/olareg/olareg/pull/92
[pr-93]: https://github.com/olareg/olareg/pull/93
[pr-95]: https://github.com/olareg/olareg/pull/95
[pr-96]: https://github.com/olareg/olareg/pull/96
[pr-97]: https://github.com/olareg/olareg/pull/97
[pr-98]: https://github.com/olareg/olareg/pull/98
[pr-99]: https://github.com/olareg/olareg/pull/99
[pr-100]: https://github.com/olareg/olareg/pull/100
[pr-102]: https://github.com/olareg/olareg/pull/102
[pr-103]: https://github.com/olareg/olareg/pull/103
[pr-105]: https://github.com/olareg/olareg/pull/105
[pr-106]: https://github.com/olareg/olareg/pull/106
[pr-107]: https://github.com/olareg/olareg/pull/107
[pr-109]: https://github.com/olareg/olareg/pull/109
[pr-108]: https://github.com/olareg/olareg/pull/108
[pr-110]: https://github.com/olareg/olareg/pull/110
[pr-111]: https://github.com/olareg/olareg/pull/111
[pr-112]: https://github.com/olareg/olareg/pull/112
[pr-117]: https://github.com/olareg/olareg/pull/117
[pr-118]: https://github.com/olareg/olareg/pull/118
[pr-119]: https://github.com/olareg/olareg/pull/119
[pr-120]: https://github.com/olareg/olareg/pull/120
[pr-121]: https://github.com/olareg/olareg/pull/121
[pr-125]: https://github.com/olareg/olareg/pull/125
[pr-126]: https://github.com/olareg/olareg/pull/126
[pr-127]: https://github.com/olareg/olareg/pull/127
