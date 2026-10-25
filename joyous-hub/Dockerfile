FROM golang:1.27-alpine AS builder
RUN apk add --no-cache bash gcc g++ musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# The whole module (see .dockerignore): sub-packages plus the go:embed assets
# (web/ UI, calibration charts, fonts, wipes, the InkJoy Device Root CA).
COPY . .
ARG JOYOUS_VERSION=docker
RUN JOYOUS_VERSION="$JOYOUS_VERSION" ./scripts/build-binary.sh /out/joyous-hub

FROM alpine:3
# libstdc++: the CGO HEIC decoder (goheif) is C++.
RUN apk add --no-cache libstdc++ libgcc ca-certificates tzdata
COPY --from=builder /out/joyous-hub /joyous-hub
EXPOSE 1883 8080
ENTRYPOINT ["/joyous-hub"]
