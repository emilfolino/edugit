# Optional. The supported default is the systemd service in deploy/.
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /edugit ./cmd/edugit

FROM alpine:3
RUN apk add --no-cache git ca-certificates && adduser -D -u 10001 edugit
COPY --from=build /edugit /usr/local/bin/edugit
USER edugit
ENV EDUGIT_DATA_DIR=/data EDUGIT_ADDR=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/edugit"]
