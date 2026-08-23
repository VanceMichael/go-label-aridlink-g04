FROM golang:1.26.0-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/aridlink ./cmd/api

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S aridlink && adduser -S -G aridlink aridlink
WORKDIR /app
COPY --from=build /out/aridlink /usr/local/bin/aridlink
COPY migrations ./migrations
USER aridlink
EXPOSE 8080
ENV ARIDLINK_ADDRESS=:8080 ARIDLINK_MIGRATIONS=/app/migrations
ENTRYPOINT ["/usr/local/bin/aridlink"]
