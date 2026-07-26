FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway

FROM alpine:3.21
RUN addgroup -S -g 10001 gateway \
    && adduser -S -D -H -u 10001 -G gateway gateway \
    && mkdir -p /data/database /data/images /data/tmp /data/trash \
    && chown -R 10001:10001 /data \
    && chmod 0750 /data /data/database /data/images /data/tmp /data/trash
COPY --from=build /out/gateway /usr/local/bin/gateway
USER gateway
VOLUME ["/data"]
EXPOSE 15880 15881
ENTRYPOINT ["/usr/local/bin/gateway"]
