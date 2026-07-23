# edge-api — multi-stage build. NFR-5: berjalan dalam Docker.
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/edge-api ./cmd/edge-api

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/edge-api /usr/local/bin/edge-api
EXPOSE 8080
ENTRYPOINT ["edge-api"]
