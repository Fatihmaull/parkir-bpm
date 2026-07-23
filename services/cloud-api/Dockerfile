FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/cloud-api ./cmd/cloud-api

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/cloud-api /usr/local/bin/cloud-api
EXPOSE 9090
ENTRYPOINT ["cloud-api"]
