# Build link-preview
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY link-preview/go.mod link-preview/go.sum* ./
RUN go mod download
COPY link-preview/main.go .
RUN go build -ldflags="-s -w" -o link-preview main.go

# Final image
FROM alpine:3.20

RUN apk add --no-cache supervisor nginx curl tzdata ca-certificates

RUN curl -L https://github.com/glanceapp/glance/releases/latest/download/glance-linux-amd64.tar.gz \
    | tar -xz -C /usr/local/bin/

COPY --from=builder /build/link-preview /usr/local/bin/

WORKDIR /app

COPY config/ /app/config/
COPY assets/ /app/assets/
COPY nginx.conf /etc/nginx/http.d/default.conf
COPY supervisord.conf /etc/supervisord.conf

ENV TZ=Europe/Amsterdam

EXPOSE 8080

CMD ["supervisord", "-c", "/etc/supervisord.conf"]
