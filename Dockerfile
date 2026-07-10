FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway

FROM alpine:3.21
RUN addgroup -S -g 10001 gateway && adduser -S -D -H -u 10001 -G gateway gateway && mkdir -p /data && chown 10001:10001 /data
COPY --from=build /out/gateway /usr/local/bin/gateway
USER gateway
VOLUME ["/data"]
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/gateway"]
