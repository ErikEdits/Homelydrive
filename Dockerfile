# Homelydrive Server as a container (optional – the normal way is the
# installer / .deb). Use host networking so the server sees the Tailscale
# interface of the host; see docker-compose.yml and docs/docker.md.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/erikedits/homelydrive/internal/version.Version=${VERSION}" \
    -o /out/homelydrive-server ./cmd/homelydrive-server

FROM alpine:3.22
# ffmpeg: video thumbnails; tzdata: local times in the log
RUN apk add --no-cache ca-certificates tzdata ffmpeg
COPY --from=build /out/homelydrive-server /usr/local/bin/homelydrive-server
ENV HOMELYDRIVE_DATA=/data
VOLUME /data
EXPOSE 8420
ENTRYPOINT ["homelydrive-server"]
CMD ["run"]
