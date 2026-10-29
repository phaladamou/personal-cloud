# Personal Cloud API image (Phase 1: local backend, no external services).
#
# Build from the repository root:
#   docker build -f infra/docker/Dockerfile -t personal-cloud/api .
# Run:
#   docker run --rm -p 8080:8080 -v pc-data:/app/data personal-cloud/api

FROM golang:1.27-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api

FROM debian:bookworm-slim

WORKDIR /app
COPY --from=build /out/api /app/api

# JSON workspace store (see cmd/api -data flag).
VOLUME /app/data
EXPOSE 8080

ENTRYPOINT ["/app/api"]
CMD ["-addr", ":8080", "-data", "/app/data/workspaces.json"]
