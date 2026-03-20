FROM golang:1.26.1-trixie AS base
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM base AS development
RUN go install github.com/air-verse/air@latest
ENTRYPOINT [ "air" ]
CMD [ "-c", ".air.toml" ]

FROM base AS build
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -ldflags "-s -w" -o /app/controller ./cmd/main.go

FROM alpine:3.23 AS production
WORKDIR /app
COPY --from=build /app/controller .
EXPOSE 8080
CMD ["./controller"]