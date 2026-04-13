# Release v0.2.0

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
