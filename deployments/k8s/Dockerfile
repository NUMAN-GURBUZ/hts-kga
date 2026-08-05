# Genel amaçlı çok aşamalı derleme — ADR-16 (K8s teslimleri dağıtım
# belgesi düzeyindedir; performans kriteri K8s'e bağlanmaz, bkz. ADR-16/ADR-34).
#
# Kullanım (repo kökünden):
#   docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=analysis-engine -t hts-kga/analysis-engine .
#
# SERVICE: simulator | analysis-engine | validation | integrity | gateway | persister
ARG SERVICE=analysis-engine

FROM golang:1.26-alpine AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/service ./cmd/${SERVICE}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/service /usr/local/bin/service
ENTRYPOINT ["/usr/local/bin/service"]
