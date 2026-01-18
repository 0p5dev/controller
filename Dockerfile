FROM golang:1.25.3-trixie AS development
WORKDIR /app
COPY go.mod go.sum sakey.json ./
ENV GOOGLE_APPLICATION_CREDENTIALS=/app/sakey.json
RUN go mod download && curl -fsSL https://get.pulumi.com | sh && /root/.pulumi/bin/pulumi login gs://${PULUMI_STATE_BUCKET}
ENV PATH="/root/.pulumi/bin:${PATH}" 
COPY . .

FROM development AS build
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -ldflags "-s -w" -o /app/controller ./cmd/main.go

FROM alpine:3.22.2 AS production
WORKDIR /app
COPY --from=build /app/controller .
COPY --from=build /root/.pulumi /root/.pulumi
ENV GOOGLE_APPLICATION_CREDENTIALS=/app/sakey.json
ENV PATH="/root/.pulumi/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT [ "sh", "-c" ]
CMD ["pulumi login gs://${PULUMI_STATE_BUCKET} && ./controller" ]