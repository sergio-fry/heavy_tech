# Сборка: чистый Go, CGO не нужен (modernc.org/sqlite — pure Go)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/heavytech .

# Рантайм: distroless static, не-root. Конфиг — через ENV (ADDR, BASE_URL,
# PODCAST_DIR, DB_PATH), данные — в volume /data
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/heavytech /heavytech
WORKDIR /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/heavytech"]
