FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /amap ./cmd/amap

FROM alpine:3.20
RUN apk add --no-cache ca-certificates git
COPY --from=builder /amap /usr/local/bin/amap
EXPOSE 8585
VOLUME /project
ENTRYPOINT ["amap"]
CMD ["dashboard", "--project", "/project", "--port", "8585"]
