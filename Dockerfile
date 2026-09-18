##################
# TOOLS (DEBIAN) #
##################

# Use golang (debian) as the base image
FROM golang:1.27-trixie as tools

# Install golangci-lint
COPY --from=golangci/golangci-lint:v2.13.2 /usr/bin/golangci-lint /usr/local/bin/golangci-lint

# Install veta
COPY --from=varavel/veta:0.1.1 /usr/local/bin/veta /usr/local/bin/veta

# Install task
COPY --from=ghcr.io/varavelio/container-tools/task:3.53.1 /usr/local/bin/task /usr/local/bin/task

# Install dprint
COPY --from=ghcr.io/varavelio/container-tools/dprint:0.57.4 /usr/local/bin/dprint /usr/local/bin/dprint

# Set environment variables
ENV \
  PIP_BREAK_SYSTEM_PACKAGES=1 \
  CGO_ENABLED=0

# Set build time variables
ARG DEBIAN_FRONTEND=noninteractive

RUN set -e && \
  # Install system dependencies
  apt-get update -qq && \
  apt-get install -yqq --no-install-recommends \
  ca-certificates wget curl zip unzip p7zip-full tzdata git tree ripgrep \
  python3 python3-pip && \
  rm -rf /var/lib/apt/lists/* && \
  # Git config
  git config --global --add safe.directory '*'

WORKDIR /workspaces/rienda

################
# DEVCONTAINER #
################

FROM tools AS devcontainer

CMD ["sleep", "infinity"]

###########
# BUILDER #
###########

FROM tools AS builder

# Set build time variables
ARG \
  RIENDA_VERSION="dev" \
  RIENDA_COMMIT="unknown" \
  RIENDA_DATE="unknown"

# Set environment variables
ENV \
  RIENDA_VERSION="${RIENDA_VERSION}" \
  RIENDA_COMMIT="${RIENDA_COMMIT}" \
  RIENDA_DATE="${RIENDA_DATE}"

# Cache the module download in its own layer
COPY Taskfile.yml go.mod go.sum ./
RUN task deps

COPY . .
RUN task build:prod

#######################
# PRODUCTION (DEBIAN) #
#######################

FROM debian:trixie-slim AS production

# Set build time variables
ARG DEBIAN_FRONTEND=noninteractive

RUN set -e && \
  # Install system dependencies
  apt-get update -qq && \
  apt-get install -yqq --no-install-recommends ca-certificates && \
  rm -rf /var/lib/apt/lists/* && \
  # Create group and user
  groupadd -g 65532 nonroot && \
  useradd -u 65532 -g nonroot -m -s /bin/sh nonroot && \
  # Create the directory sessions run in
  mkdir -p /workspace && \
  chown nonroot:nonroot /workspace

# Sessions run in the workspace directory by default
WORKDIR /workspace

COPY --from=builder --chown=nonroot:nonroot /workspaces/rienda/dist/rienda /usr/local/bin/rienda

USER nonroot

# The configuration and the sessions live under the home directory of the user
VOLUME ["/home/nonroot/.rienda"]

ENTRYPOINT ["/usr/local/bin/rienda"]

CMD ["--help"]
