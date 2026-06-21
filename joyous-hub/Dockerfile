FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc g++ musl-dev libheif-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=1 go build -o joyous-hub .

FROM alpine:3
RUN apk add --no-cache libheif ca-certificates tzdata
COPY --from=builder /app/joyous-hub /joyous-hub
EXPOSE 1883 8080
ENTRYPOINT ["/joyous-hub"]
