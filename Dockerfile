# SPDX-License-Identifier: AGPL-3.0-or-later
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS TARGETARCH
ARG VERSION=dev REVISION=unknown BUILD_TIME=0 COVER=
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      ${COVER:+-cover -covermode=atomic -coverpkg=./...} \
      -ldflags "-s -w \
        -X github.com/motwok/ImageAdvisor/internal/app.Version=$VERSION \
        -X github.com/motwok/ImageAdvisor/internal/app.Revision=$REVISION \
        -X github.com/motwok/ImageAdvisor/internal/app.BuildTime=$BUILD_TIME" \
      -o /out/imageadvisor ./cmd/imageadvisor

FROM scratch
COPY --from=build /out/imageadvisor /imageadvisor
LABEL org.opencontainers.image.source="https://github.com/motwok/ImageAdvisor" \
      org.opencontainers.image.description="Prometheus exporter that reports which containers have a newer image in their registry." \
      org.opencontainers.image.licenses="AGPL-3.0-or-later"
EXPOSE 8080
ENTRYPOINT ["/imageadvisor"]
CMD ["--config.file=/data/config.yml", "--storage.path=/data"]
