FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o athlete-intelligence .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/athlete-intelligence .
COPY static/ ./static/
EXPOSE 8080
CMD ["./athlete-intelligence"]
