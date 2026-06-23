# Release Notes

## Release v0.2.2-rc1

Features:

- Generate linux/riscv64 artifacts. ([PR 241][pr-241])
- Add make clean target. ([PR 248][pr-248])
- Permissive handling of manifest media type accept header. ([PR 249][pr-249])
- Allow conformance to be manually submitted. ([PR 252][pr-252])

Fixes:

- Validate tag values on push. ([PR 243][pr-243])

Other Changes:

- Moving documentation to the website. ([PR 247][pr-247])
- Update conformance variables for OCI refactor. ([PR 253][pr-253])
- Update release process. ([PR 255][pr-255])

Contributors:

- @sudo-bmitch

[pr-241]: https://github.com/olareg/olareg/pull/241
[pr-243]: https://github.com/olareg/olareg/pull/243
[pr-247]: https://github.com/olareg/olareg/pull/247
[pr-248]: https://github.com/olareg/olareg/pull/248
[pr-249]: https://github.com/olareg/olareg/pull/249
[pr-252]: https://github.com/olareg/olareg/pull/252
[pr-253]: https://github.com/olareg/olareg/pull/253
[pr-255]: https://github.com/olareg/olareg/pull/255

## Release v0.2.1

Changes:

- Update from go-yaml/yaml to goccy/go-yaml. ([PR 239][pr-239])

Contributors:

- @sudo-bmitch

[pr-239]: https://github.com/olareg/olareg/pull/239

## Release v0.2.0

Security:

- Go 1.25.5 fixes CVE-2025-61729 ([PR 210][pr-210])
- Go 1.25.5 fixes CVE-2025-61727 ([PR 210][pr-210])
- Go upgrade fixes CVE-2025-68121, govulncheck indicates this project is not vulnerable. ([PR 224][pr-224])
- Go 1.26.2 release fixes CVE-2026-32280 ([PR 237][pr-237])
- Go 1.26.2 release fixes CVE-2026-32281 ([PR 237][pr-237])
- Go 1.26.2 release fixes CVE-2026-32283 ([PR 237][pr-237])
- Go 1.26.2 release fixes CVE-2026-33810 ([PR 237][pr-237])

Breaking Changes:

- Change digest implementation. ([PR 178][pr-178])
- Dropping support for the third Go release due to the upstream Go policy. ([PR 181][pr-181])

Features:

- Add static basic auth. ([PR 174][pr-174])
- Adding config for auth. ([PR 181][pr-181])
- Add opaque token auth. ([PR 183][pr-183])
- Add a json error message on auth failures. ([PR 186][pr-186])
- Add a wildcard access that will match on unknown methods. ([PR 186][pr-186])
- Support user/pass being explicitly set to empty strings as an anonymous login. ([PR 186][pr-186])
- Allow pushing content with foreign layers. ([PR 209][pr-209])
- Allow foreign and non-distributable layers. ([PR 213][pr-213])
- Support sparse manifests. ([PR 215][pr-215])
- Add support for cosign v3 bundles. ([PR 220][pr-220])
- Add support for pushing digest with tags. ([PR 233][pr-233])

Fixes:

- Fix: Remove set-output from GHA. ([PR 165][pr-165])
- Fix: Expires time is int64. ([PR 185][pr-185])
- Fix: Handle auth on disabled methods. ([PR 186][pr-186])
- Fix: Add content-type header to JSON error messages. ([PR 186][pr-186])
- Fix: Add Docker-Content-Digest header on blob put. ([PR 211][pr-211])
- Fix: Blob upload cancel status is 204. ([PR 214][pr-214])
- Fix: Empty referrer list should return empty list. ([PR 218][pr-218])
- Fix: Apply Go modernizations with `go fix` from 1.26.0. ([PR 226][pr-226])
- Fix: Prevent a race between closing a directory and GC. ([PR 232][pr-232])

Miscellaneous:

- Modernize for Go 1.22. ([PR 167][pr-167])
- Add gofumpt to the build. ([PR 197][pr-197])
- Update opaque auth testing to verify header. ([PR 198][pr-198])
- Add a policy for LLM generated contributions. ([PR 207][pr-207])

Contributors:

- @sudo-bmitch

[pr-165]: https://github.com/olareg/olareg/pull/165
[pr-167]: https://github.com/olareg/olareg/pull/167
[pr-174]: https://github.com/olareg/olareg/pull/174
[pr-178]: https://github.com/olareg/olareg/pull/178
[pr-181]: https://github.com/olareg/olareg/pull/181
[pr-183]: https://github.com/olareg/olareg/pull/183
[pr-185]: https://github.com/olareg/olareg/pull/185
[pr-186]: https://github.com/olareg/olareg/pull/186
[pr-197]: https://github.com/olareg/olareg/pull/197
[pr-198]: https://github.com/olareg/olareg/pull/198
[pr-207]: https://github.com/olareg/olareg/pull/207
[pr-209]: https://github.com/olareg/olareg/pull/209
[pr-210]: https://github.com/olareg/olareg/pull/210
[pr-211]: https://github.com/olareg/olareg/pull/211
[pr-213]: https://github.com/olareg/olareg/pull/213
[pr-214]: https://github.com/olareg/olareg/pull/214
[pr-215]: https://github.com/olareg/olareg/pull/215
[pr-218]: https://github.com/olareg/olareg/pull/218
[pr-220]: https://github.com/olareg/olareg/pull/220
[pr-224]: https://github.com/olareg/olareg/pull/224
[pr-226]: https://github.com/olareg/olareg/pull/226
[pr-232]: https://github.com/olareg/olareg/pull/232
[pr-233]: https://github.com/olareg/olareg/pull/233
[pr-237]: https://github.com/olareg/olareg/pull/237

## Release v0.1.2

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

## Release v0.1.1

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

## Release v0.1.0

Initial release of olareg!

Changes:

- Initial read-only implementation. ([PR 1][pr-1])
- Support older versions of Go (1.19 and 1.20). ([PR 3][pr-3])
- Add docker image build. ([PR 4][pr-4])
- Add version-check utility to manage dependencies and version pins. ([PR 6][pr-6])
- Chore: Add OpenSSF Scorecard. ([PR 7][pr-7])
- Chore: Refactor store and olareg packages. ([PR 8][pr-8])
- Initial support for blob post and put. ([PR 9][pr-9])
- Adding support for manifest put. ([PR 12][pr-12])
- Detect manifest media type. ([PR 13][pr-13])
- Validate manifest content. ([PR 13][pr-13])
- Clean child manifests from index.json when index is pushed. ([PR 13][pr-13])
- Fix: Reduce verbosity in logs for blob head requests. ([PR 14][pr-14])
- Fix: Reduce verbosity in logs for manifest head requests. ([PR 15][pr-15])
- Feature: Add an in-memory store. ([PR 16][pr-16])
- Reserves the OCI Layout filenames from being used in a repository name. ([PR 17][pr-17])
- Add blob patch methods. ([PR 18][pr-18])
- Add manifest delete API. ([PR 19][pr-19])
- Add support for the referrers API. ([PR 20][pr-20])
- Fix: add new config package to `.dockerignore` exclusions. ([PR 21][pr-21])
- Fix: update the status code on the blob upload get request. ([PR 22][pr-22])
- Support blob delete API, disabled by default. ([PR 23][pr-23])
- Fix: Add docker-content-digest header on manifest put for docker support. ([PR 25][pr-25])
- Feature: allow push and/or delete APIs to be disabled. ([PR 26][pr-26])
- Fix referrer conversion. ([PR 27][pr-27])
- Fix: panic on Index.AddDesc accessing entry beyond end of slice. ([PR 29][pr-29])
- Chore: Add tests to `Index` methods. ([PR 30][pr-30])
- Fix: Deleting entries by tag or subject would delete unrelated entries from the Index. ([PR 30][pr-30])
- Chore: include vendor folder in docker image builds. ([PR 32][pr-32])
- Feature: Add support for blob mount. ([PR 33][pr-33])
- Feature: Handle existing blobs in the store. ([PR 33][pr-33])
- Fix: Add lock for data race in mem.BlobCreate. ([PR 34][pr-34])
- Fix a data race on `IndexGet`. ([PR 35][pr-35])
- Feature: Add `Copy` methods to `Index` and child types. ([PR 36][pr-36])
- Fix: Return a `Index.Copy` in `IndexGet` to prevent data races in nested structures. ([PR 36][pr-36])
- Chore: Add tests to olareg package. ([PR 37][pr-37])
- Add ability to configure memory with initialization from directory. ([PR 38][pr-38])
- Chore: improve testing of olareg package. ([PR 39][pr-39])
- Chore: add more testing for olareg package. ([PR 40][pr-40])
- Fix: Split the media type header by comma. ([PR 41][pr-41])
- Chore: Consolidate location of config default values. ([PR 42][pr-42])
- Feature: Support read-only store. ([PR 43][pr-43])
- Chore: Add blobMeta method. ([PR 44][pr-44])
- Feature: add garbage collection to the memory store. ([PR 46][pr-46])
- Add garbage collection for the directory store. ([PR 47][pr-47])
- Fix a race in the store test. ([PR 49][pr-49])
- Fix uploads with anonymous blob mount digests. ([PR 50][pr-50])
- Fix blob upload error handling to delete failed upload. ([PR 50][pr-50])
- Add garbage collection CLI options to olareg command. ([PR 51][pr-51])
- Fix: tests close the server and store when finished. ([PR 52][pr-52])
- Fix: prevent race conditions on server shutdown with active API calls running. ([PR 53][pr-53])
- Fix: Load and save index when running a garbage collection. ([PR 55][pr-55])
- Chore: Consolidate GC implementations. ([PR 56][pr-56])
- Fix: Improve handling of a corrupt repository with missing blobs. ([PR 57][pr-57])
- Move session tracking into `store.Repo`. ([PR 58][pr-58])
- Chore: Update OSV Scanner CLI syntax. ([PR 60][pr-60])
- Cancel remaining uploads when store is closed. ([PR 61][pr-61])
- Cancel and cleanup uploads after grace period. ([PR 62][pr-62])
- Delete empty upload directory during GC. ([PR 63][pr-63])
- Prune empty an repository as part of GC. ([PR 64][pr-64])
- Go 1.22 support added and 1.19 support dropped. ([PR 65][pr-65])
- Chore: Check for allowed licenses with OSV scanner. ([PR 66][pr-66])
- Feature: Support blob upload delete API. ([PR 67][pr-67])
- Chore: Update GitHub Actions badges to link to workflow history. ([PR 69][pr-69])
- Chore: Update `syft packages` CLI to `syft scan`. ([PR 70][pr-70])
- Feature: Support referrer artifact type filtering. ([PR 72][pr-72])
- Feature: Add support for referrers response pagination. ([PR 73][pr-73])
- Chore: Fix unused parameter warning. ([PR 74][pr-74])
- Feature: Include Go test coverage report in GitHub Actions. ([PR 75][pr-75])
- Chore: Use project specific annotations for referrers. ([PR 76][pr-76])
- Fix: Fix coverage report generator. ([PR 77][pr-77])
- Chore: Add more testing to referrers pagination. ([PR 78][pr-78])
- Chore: Adjust docker image build to set annotations from promoted labels. ([PR 81][pr-81])
- Chore: Release artifacts individually. ([PR 82][pr-82])
- Add release script. ([PR 83][pr-83])

Contributors:

- @sudo-bmitch

[pr-1]: https://github.com/olareg/olareg/pull/1
[pr-3]: https://github.com/olareg/olareg/pull/3
[pr-4]: https://github.com/olareg/olareg/pull/4
[pr-6]: https://github.com/olareg/olareg/pull/6
[pr-7]: https://github.com/olareg/olareg/pull/7
[pr-8]: https://github.com/olareg/olareg/pull/8
[pr-9]: https://github.com/olareg/olareg/pull/9
[pr-12]: https://github.com/olareg/olareg/pull/12
[pr-13]: https://github.com/olareg/olareg/pull/13
[pr-14]: https://github.com/olareg/olareg/pull/14
[pr-15]: https://github.com/olareg/olareg/pull/15
[pr-16]: https://github.com/olareg/olareg/pull/16
[pr-17]: https://github.com/olareg/olareg/pull/17
[pr-18]: https://github.com/olareg/olareg/pull/18
[pr-19]: https://github.com/olareg/olareg/pull/19
[pr-20]: https://github.com/olareg/olareg/pull/20
[pr-21]: https://github.com/olareg/olareg/pull/21
[pr-22]: https://github.com/olareg/olareg/pull/22
[pr-23]: https://github.com/olareg/olareg/pull/23
[pr-25]: https://github.com/olareg/olareg/pull/25
[pr-26]: https://github.com/olareg/olareg/pull/26
[pr-27]: https://github.com/olareg/olareg/pull/27
[pr-29]: https://github.com/olareg/olareg/pull/29
[pr-30]: https://github.com/olareg/olareg/pull/30
[pr-32]: https://github.com/olareg/olareg/pull/32
[pr-33]: https://github.com/olareg/olareg/pull/33
[pr-34]: https://github.com/olareg/olareg/pull/34
[pr-35]: https://github.com/olareg/olareg/pull/35
[pr-36]: https://github.com/olareg/olareg/pull/36
[pr-37]: https://github.com/olareg/olareg/pull/37
[pr-38]: https://github.com/olareg/olareg/pull/38
[pr-39]: https://github.com/olareg/olareg/pull/39
[pr-40]: https://github.com/olareg/olareg/pull/40
[pr-41]: https://github.com/olareg/olareg/pull/41
[pr-42]: https://github.com/olareg/olareg/pull/42
[pr-43]: https://github.com/olareg/olareg/pull/43
[pr-44]: https://github.com/olareg/olareg/pull/44
[pr-46]: https://github.com/olareg/olareg/pull/46
[pr-47]: https://github.com/olareg/olareg/pull/47
[pr-49]: https://github.com/olareg/olareg/pull/49
[pr-50]: https://github.com/olareg/olareg/pull/50
[pr-51]: https://github.com/olareg/olareg/pull/51
[pr-52]: https://github.com/olareg/olareg/pull/52
[pr-53]: https://github.com/olareg/olareg/pull/53
[pr-55]: https://github.com/olareg/olareg/pull/55
[pr-56]: https://github.com/olareg/olareg/pull/56
[pr-57]: https://github.com/olareg/olareg/pull/57
[pr-58]: https://github.com/olareg/olareg/pull/58
[pr-60]: https://github.com/olareg/olareg/pull/60
[pr-61]: https://github.com/olareg/olareg/pull/61
[pr-62]: https://github.com/olareg/olareg/pull/62
[pr-63]: https://github.com/olareg/olareg/pull/63
[pr-64]: https://github.com/olareg/olareg/pull/64
[pr-65]: https://github.com/olareg/olareg/pull/65
[pr-66]: https://github.com/olareg/olareg/pull/66
[pr-67]: https://github.com/olareg/olareg/pull/67
[pr-69]: https://github.com/olareg/olareg/pull/69
[pr-70]: https://github.com/olareg/olareg/pull/70
[pr-72]: https://github.com/olareg/olareg/pull/72
[pr-73]: https://github.com/olareg/olareg/pull/73
[pr-74]: https://github.com/olareg/olareg/pull/74
[pr-75]: https://github.com/olareg/olareg/pull/75
[pr-76]: https://github.com/olareg/olareg/pull/76
[pr-77]: https://github.com/olareg/olareg/pull/77
[pr-78]: https://github.com/olareg/olareg/pull/78
[pr-81]: https://github.com/olareg/olareg/pull/81
[pr-82]: https://github.com/olareg/olareg/pull/82
[pr-83]: https://github.com/olareg/olareg/pull/83
