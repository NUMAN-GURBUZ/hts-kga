# K9 ölçekleme altyapısı (ADR-16, ADR-34/4).
#
# Bu imaj yalnızca K9 ölçümü için vardır — günlük geliştirme akışı `go run`
# ile devam eder (health/log/debugger davranışı değişmez). `docker-compose.yml`
# içindeki `analysis-engine` servisi `profiles: ["scale-test"]` ile
# işaretlidir: varsayılan `docker compose up`'a dahil olmaz.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/analysis-engine ./cmd/analysis-engine

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/analysis-engine /usr/local/bin/analysis-engine
ENTRYPOINT ["/usr/local/bin/analysis-engine"]
