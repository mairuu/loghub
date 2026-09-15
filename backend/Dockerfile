FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loghub ./cmd/loghub

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/loghub /loghub
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/loghub"]
CMD ["serve"]

