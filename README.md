# olareg

[![Go Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/go.yml?branch=main&label=Go%20build)](https://github.com/olareg/olareg/actions/workflows/go.yml)
[![Docker Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/docker.yml?branch=main&label=Docker%20build)](https://github.com/olareg/olareg/actions/workflows/docker.yml)
[![Dependency Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/version-check.yml?branch=main&label=Dependency%20check)](https://github.com/olareg/olareg/actions/workflows/version-check.yml)
[![Vulnerability Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/vulnscans.yml?branch=main&label=Vulnerability%20check)](https://github.com/olareg/olareg/actions/workflows/vulnscans.yml)

[![Go Reference](https://pkg.go.dev/badge/github.com/olareg/olareg.svg)](https://pkg.go.dev/github.com/olareg/olareg)
![License](https://img.shields.io/github/license/olareg/olareg)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/olareg/olareg/badge)](https://securityscorecards.dev/viewer/?uri=github.com/olareg/olareg)

Pronounced: oh-la-reg

olareg (named from being an OCI Layout based Registry) is a minimal OCI conformant container registry.
The minimal nature includes avoiding external dependencies, making the project easy to embed in unit tests or deployed in an edge environment as a cache.
