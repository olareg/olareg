# olareg

[![Go Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/go.yml?branch=main&label=Go%20build)](https://github.com/olareg/olareg/actions/workflows/go.yml)
[![Docker Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/docker.yml?branch=main&label=Docker%20build)](https://github.com/olareg/olareg/actions/workflows/docker.yml)
[![CI & Conformance Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/ci-conformance.yml?branch=main&label=CI%20%26%20Conformance)](https://github.com/olareg/olareg/actions/workflows/ci-conformance.yml)
[![Dependency Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/version-check.yml?branch=main&label=Dependency%20check)](https://github.com/olareg/olareg/actions/workflows/version-check.yml)
[![Vulnerability Workflow Status](https://img.shields.io/github/actions/workflow/status/olareg/olareg/vulnscans.yml?branch=main&label=Vulnerability%20check)](https://github.com/olareg/olareg/actions/workflows/vulnscans.yml)

[![Go Reference](https://pkg.go.dev/badge/github.com/olareg/olareg.svg)](https://pkg.go.dev/github.com/olareg/olareg)
![License](https://img.shields.io/github/license/olareg/olareg)
[![Go Report Card](https://goreportcard.com/badge/github.com/olareg/olareg)](https://goreportcard.com/report/github.com/olareg/olareg)
[![GitHub Downloads](https://img.shields.io/github/downloads/olareg/olareg/total?label=GitHub%20downloads)](https://github.com/olareg/olareg/releases)

olareg (pronounced oh-la-reg) is a minimal OCI conformant container registry.
It is designed around the OCI Layout structure for storing images in a directory.
The minimal nature includes avoiding external dependencies, making the project easy to embed in unit tests or deployed in an edge environment as a cache.

## Key Features

- OCI Conformant, frequently leveraged as an example registry to verify feasibility of future OCI specification changes.
- Runs as a container, a standalone binary, or embedded within a Go application.
- Storage uses the OCI Layout, making content portable and easy to preload.
- Ephemeral storage is also an option, saving all changes to memory, which is useful for CI and unit testing.
- Embed it in Go unit tests, with the option to preload content from a directory, to test OCI client projects.

## Quick Start

You can deploy olareg in a container with persistent storage with the following command:

```shell
docker run -p 5000:5000 \
  -v "olareg-data:/home/appuser/registry" \
  ghcr.io/olareg/olareg serve --dir /home/appuser/registry
```

Or for an ephemeral registry:

```shell
docker run -p 5000:5000 --rm \
  ghcr.io/olareg/olareg serve --store-type mem
```

## Installing and Configuring

Details on installing and configuring olareg can be found on <https://olareg.org/>.

<!-- markdownlint-disable-file MD033 -->
