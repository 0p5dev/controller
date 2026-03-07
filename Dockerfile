FROM golang:1.26.1-trixie AS development
WORKDIR /app
COPY go.mod go.sum ./
COPY . .

FROM development AS build
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -ldflags "-s -w" -o /app/controller ./cmd/main.go

FROM alpine:3.23 AS production
WORKDIR /app
COPY --from=build /app/controller .
EXPOSE 8080
ENTRYPOINT [ "sh", "-c" ]
CMD ["./controller" ]