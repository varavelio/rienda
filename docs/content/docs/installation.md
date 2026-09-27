---
title: "Installation"
description: "Install Rienda with the shell and PowerShell installers, Homebrew, the Docker image, prebuilt binaries, or build it from source."
icon: "download"
weight: 10
---

# Installation

Rienda ships as a single self-contained executable: the terminal interface, the
non-interactive commands, and the embedded JavaScript runtime are all inside it.
There is nothing else to install at runtime, no service to connect and no
database to run.

Pick the flavor that fits your machine:

- **The installers** are the quickest path on Linux, macOS and Windows.
- **The Docker image** is useful when you want Rienda isolated from the host.
- **The source** is there when you want to build it yourself or follow the
  project.

## Install with the shell installer

On Linux and macOS, the installer downloads the right binary for your platform,
verifies its SHA-256 checksum, and installs it into `/usr/local/bin`:

```bash
curl -fsSL https://get.varavel.com/rienda | sh
```

Verify the installation with `rienda --help`, which prints the command usage.

The installer accepts a few options through environment variables:

```bash
# Install a specific version
curl -fsSL https://get.varavel.com/rienda | VERSION=vx.x.x sh

# Install into a user directory, without sudo
curl -fsSL https://get.varavel.com/rienda | INSTALL_DIR=$HOME/.local/bin sh

# Suppress all output
curl -fsSL https://get.varavel.com/rienda | QUIET=true sh
```

When `VERSION` is not set, the installer resolves the latest release. If the
target directory is not writable and a terminal is available, the installer
falls back to `sudo`; point `INSTALL_DIR` somewhere writable to avoid that
entirely.

## Install with Homebrew

On macOS or Linux with Homebrew, install the formula from the Varavel tap:

```bash
brew install varavelio/tap/rienda
```

Upgrading follows the usual Homebrew flow:

```bash
brew update && brew upgrade rienda
```

Stable releases also publish a pinned versioned formula, so you can hold an
exact version with `brew install varavelio/tap/rienda@x.x.x` style commands when
you need to. Pre-releases are only reachable through the moving `rienda-next`
formula.

## Install on Windows

The PowerShell installer downloads the Windows binary, verifies its SHA-256
checksum, installs it into your local programs directory, and adds it to your
user `PATH`:

```powershell
irm https://get.varavel.com/rienda.ps1 | iex
```

Verify the installation by opening a new terminal and running `rienda --help`.

The installer accepts the same `VERSION`, `INSTALL_DIR`, and `QUIET` options as
the shell installer, set as environment variables before the command. If your
execution policy blocks the one-liner, run it with an explicit bypass:

```powershell
powershell -ExecutionPolicy ByPass -Command "irm https://get.varavel.com/rienda.ps1 | iex"
```

## Download the binaries directly

Every release publishes prebuilt archives for `linux/amd64`, `linux/arm64`,
`darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, plus
`checksums.txt` and `manifest.json`:

```text
https://github.com/varavelio/rienda/releases
```

Each archive contains the binary, the readme, and the license. Verify a download
against the release checksums before trusting it:

```bash
sha256sum --check checksums.txt --ignore-missing
```

## Run with Docker

The same image is published to two registries:

```text
docker.io/varavel/rienda:<version>
ghcr.io/varavelio/rienda:<version>
```

Both are multi-arch manifests covering `linux/amd64` and `linux/arm64`. Version
tags follow the releases, and the `latest` tag only tracks stable releases, so
pinning an exact version keeps your upgrades deliberate.

The image runs as an unprivileged user with UID and GID 65532, sessions run in
`/workspace`, and the state lives under `/home/nonroot/.rienda`, which is
declared as a volume. Mount it to keep your configuration, agents, and sessions
across containers:

```bash
docker run -it --rm \
  -v rienda-home:/home/nonroot/.rienda \
  -v "$PWD:/workspace" \
  varavel/rienda
```

The container needs an interactive terminal, so run it with `-it`. The
non-interactive command is the same binary with an argument:

```bash
docker run --rm \
  -v rienda-home:/home/nonroot/.rienda \
  -v "$PWD:/workspace" \
  varavel/rienda run -a my-agent -p "summarize this repository"
```

Rienda reads the provider credentials from `config.yaml`, so mount the same
configuration you use on the host, or build derived images that bake it in. The
container needs network access to reach the providers, and the home volume keeps
the API keys out of the image itself.

<vara-alert
title="Extensions run with the privileges of the container user"
description="Tools and hooks are JavaScript that runs unsandboxed. In a container that is the unprivileged user, but the scripts can still read everything the volume mounts expose. Install only extensions you trust."
color="warning"
/>

## Build from source

If you prefer to build the binary yourself, all you need is Go:

```bash
git clone https://github.com/varavelio/rienda.git
cd rienda
go build -o ./dist/rienda ./cmd/rienda/.
```

To install it straight into your `GOPATH/bin` (or `GOBIN`) with a single
command, from any directory, use `go install`:

```bash
go install github.com/varavelio/rienda/cmd/rienda@latest
```

The repository also defines a Taskfile with the same commands plus its
development checks. Use the production task to get the same optimized binary the
official artifacts ship:

```bash
task build:prod
```

## First steps

Rienda reads its configuration from `~/.rienda/config.yaml` and its agent
definitions from `~/.rienda/agents`. The configuration file must declare at
least one provider, and an agent must declare a model that the configuration
holds. `--config` and the `RIENDA_CONFIG` environment variable point Rienda at a
configuration file somewhere else.

Open the interactive interface by running the binary with no arguments:

```bash
rienda
```

The interface starts a new session or continues a previous one of the workspace,
and it is the default mode. The non-interactive `rienda run` command runs an
agent once and prints its answer, which is what scripts and CI use. Continue
with [Advanced](/docs/advanced/) when you want to add your own tools and hooks.

## Upgrades

Installing a newer version is replacing the binary or the image tag. The state
under `~/.rienda` carries forward, so your configuration, agents, and sessions
survive an upgrade untouched. To install a specific version with the installers,
set `VERSION=vx.x.x` as shown above; with Homebrew, upgrade as usual; with
Docker, change the tag.
