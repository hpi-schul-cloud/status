FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/statusd ./cmd/statusd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=nonroot:nonroot /out/statusd /statusd
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/statusd"]
