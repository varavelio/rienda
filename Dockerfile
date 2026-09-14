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
