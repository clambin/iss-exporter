FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.24 AS builder

ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ENV VERSION=$VERSION

WORKDIR /app/
ADD . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
    -ldflags="-X main.version=$VERSION" \
    -o iss_exporter \
    .

FROM alpine

RUN apk update && \
    apk add --no-cache tzdata

WORKDIR /app
COPY --from=builder /app/iss_exporter /app/iss_exporter

RUN /usr/sbin/addgroup app
RUN /usr/sbin/adduser app -G app -D
USER app

ENTRYPOINT ["/app/iss_exporter"]
CMD []
