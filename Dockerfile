# The build stage runs natively on the build machine and cross-compiles for the
# target platform (linux/amd64, linux/arm64) — no emulation needed.
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags='-s -w' -o /filedeck ./cmd/filedeck
# Mount points only: world-writable with the sticky bit (like /tmp), so any
# FILEDECK_USER can create its private directories inside on first start.
RUN mkdir -p /out/data /out/files /out/spaces && chmod 1777 /out/data /out/files

FROM scratch
COPY --from=build /filedeck /filedeck
# Copy the parent so the directories keep their modes (COPY dir/ would not).
COPY --from=build /out/ /
ENV FILEDECK_STATE=/data/state \
    FILEDECK_ROOT=/files/own \
    FILEDECK_SPACES_DIR=/spaces \
    FILEDECK_LISTEN=:8443
USER 65532:65532
EXPOSE 8443
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 CMD ["/filedeck", "healthcheck"]
ENTRYPOINT ["/filedeck"]
CMD ["serve"]
