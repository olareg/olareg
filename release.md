# Release v0.1.0

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
