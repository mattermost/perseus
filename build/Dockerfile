# This dockerfile is used to build Mattermost perseus
# A multi stage build, with golang used as a builder
# and gcr.io/distroless/static as runner
ARG GO_IMAGE=golang:1.19.3@sha256:10e3c0f39f8e237baa5b66c5295c578cac42a99536cc9333d8505324a82407d9
# hadolint ignore=DL3006
FROM ${GO_IMAGE} as builder

#GO_BUILD_PLATFORMS holds the platforms that we will build the docker image against
ARG GO_BUILD_PLATFORMS=linux-amd64

# Setup directories structure and compile
COPY . /src
WORKDIR /src

RUN make go-build

# Shrink final image since we only need the perseus binary
# and use distroless container image as runner for security
FROM gcr.io/distroless/static-debian11@sha256:5759d194607e472ff80fff5833442d3991dd89b219c96552837a2c8f74058617 as runner
COPY --from=builder /src/dist/perseus-linux-amd64 /opt/perseus/bin/perseus

# Copy app configuration
COPY --from=builder /src/config/config.sample.json /opt/perseus/bin/config/config.json

# We should refrain from running as privileged user
# Run as UID for nobody
USER 65534

WORKDIR /opt/perseus/bin
ENTRYPOINT ["./perseus"]
