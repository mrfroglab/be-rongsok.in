# Rongsok.in API (Go). Image akhir = satu binary statis di distroless (tanpa shell).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rongsokin-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/rongsokin-api /app/rongsokin-api
ENV PORT=3000
EXPOSE 3000
USER nonroot:nonroot
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=6 \
  CMD ["/app/rongsokin-api", "healthcheck"]
# Start: migrasi skema → seed (hanya bila DB kosong) → server.
ENTRYPOINT ["/app/rongsokin-api"]
