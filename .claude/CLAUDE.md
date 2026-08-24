# Repository Guidelines

## Project Structure & Module Organization

This Go module implements GARM's fixed-guest Hyper-V external provider. `main.go` starts the provider; `provider/` contains the lifecycle, Hyper-V client, locking, and platform-specific code. Files ending in `_windows.go` require Windows, while `_stub.go` provides non-Windows-compatible behavior. `config/` parses and validates provider TOML. Keep deployable examples in `examples/`, and host-readiness automation in `scripts/preflight.ps1`.

## Build, Test, and Development Commands

Use Go 1.26.2 or later. From the repository root:

```powershell
go test ./...                         # run all unit tests
go test ./provider -run TestCreate    # run a focused provider test
go vet ./...                          # check common Go mistakes
.\build.ps1 -Version v0.1.0           # build the Windows provider executable
.\scripts\preflight.ps1              # verify a configured Hyper-V host
```

`build.ps1` downloads modules and writes the executable to `C:\garm\providers\garm-provider-hyperv.exe` unless `-OutputPath` overrides it. Run it from an elevated PowerShell session on the Hyper-V host.

## Coding Style & Naming Conventions

Follow idiomatic Go and format every changed Go file with `gofmt`. Use tabs for indentation, package names in lowercase, exported Go identifiers in `PascalCase`, and unexported identifiers in `camelCase`. Keep OS-specific behavior behind Go build suffixes/tags; add a matching stub when a shared API must compile off Windows. Preserve the existing SPDX copyright header in new Go source files.

## Testing Guidelines

Tests use Go's standard `testing` package and live beside the code as `*_test.go`. Name tests `Test<Behavior>` and use table-driven subtests where multiple inputs share one rule. Cover success, validation/error, and guest lifecycle paths; use fakes instead of real Hyper-V calls. Run `go test ./...` before submitting. Windows-only tests use `//go:build windows`; run them on Windows when changing Hyper-V or mutex code.

## Security & Configuration

Do not commit credentials, private keys, `known_hosts`, generated executables, or provider configuration containing secrets. Start from `examples/garm-provider-hyperv.toml`; store secrets in files with restricted NTFS ACLs as described in `README.md`.
