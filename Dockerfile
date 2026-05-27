FROM golang:1.26.3-alpine AS builder
ARG VERSION=dev
WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /fx-rates .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chown=65534:65534 /fx-rates /fx-rates
USER 65534:65534
ENTRYPOINT ["/fx-rates"]
