# ==============================================================================
# HTS-KGA — Makefile
# Sprint 0 / T-E01-01 · T-E01-12
# ==============================================================================

MODULE     := github.com/NUMAN-GURBUZ/hts-kga
GO         := go
GOFLAGS    :=

# Docker Compose kısayolu
DC         := docker compose -f deployments/compose/docker-compose.yml

# Veritabanı bağlantı bilgileri (.env'den yüklenir)
include .env
export

PGDSN      ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:5432/$(POSTGRES_DB)?sslmode=disable

# ==============================================================================
# Ana hedefler
# ==============================================================================

.PHONY: all build test lint clean

all: build

build: ## Tüm cmd/* ikilileri derle
	$(GO) build $(GOFLAGS) ./cmd/...

test: ## Tüm testleri çalıştır (birim + PBT); altyapı gerektirenler atlanır
	$(GO) test $(GOFLAGS) -race -count=1 ./...

test-integration: ## Altyapı gerektiren testleri çalıştır (T-E02-06) — önce: make setup
	HTS_TEST_PG_DSN="$(PGDSN)" \
	HTS_TEST_REDIS_ADDR="localhost:6379" \
	HTS_TEST_KAFKA_BROKERS="localhost:9092" \
	KAFKA_SASL_USER=svc_test \
	KAFKA_SASL_PASSWORD="$(KAFKA_PW_TEST)" \
	$(GO) test $(GOFLAGS) -count=1 -v ./tests/integration/...

# NOT (ADR-32): entegrasyon testleri hem üretir hem tüketir ve S3b rolünü de
# oynar (F.5 ground truth okur), bu yüzden `svc_test` kimliğiyle koşarlar.
# svc_test bir SERVİS kimliği değildir ve üretim topolojisinde hiçbir zaman
# koşmaz; K6'nın iddiası svc_analysis/svc_integrity üzerinedir ve
# make verify-isolation onu bu kimliklerle sınar.

lint: ## golangci-lint (CI'da zorunlu)
	golangci-lint run ./...

clean: ## Derlenmiş ikileri temizle
	rm -f bin/*

# ==============================================================================
# Altyapı kurulumu — T-E01-02..08
# ==============================================================================

.PHONY: infra-up infra-down infra-logs setup

infra-up: ## Docker Compose altyapısını başlat
	$(DC) up -d
	@echo "Servisler ayağa kalkıyor, sağlık kontrolü bekleniyor..."
	@$(DC) ps

infra-down: ## Altyapıyı durdur (volume'lar korunur)
	$(DC) down

infra-destroy: ## Altyapıyı ve tüm volume'ları sil — DİKKATLİ KUL
	$(DC) down -v --remove-orphans

infra-logs: ## Servis loglarını izle
	$(DC) logs -f

setup: infra-up migrate-up seed-kafka ## Tam altyapı kurulumu (T-E01-12)
	@echo ""
	@echo "✅ Sprint 0 altyapısı hazır."
	@echo "   PostgreSQL   : localhost:5432"
	@echo "   Kafka        : localhost:9092"
	@echo "   Redis        : localhost:6379"

# ==============================================================================
# Veritabanı migration — T-E01-05..07
# ==============================================================================

.PHONY: migrate-up migrate-down migrate-status

migrate-up: ## Tüm migration'ları uygula
	@echo "Migration uygulanıyor..."
	@for f in internal/storage/migrations/*.sql; do \
		echo "  → $$f"; \
		PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U $(POSTGRES_USER) -d $(POSTGRES_DB) -f "$$f" -v ON_ERROR_STOP=1; \
	done
	@echo "✅ Migration tamamlandı."

migrate-down: ## Şemayı sıfırla (DROP SCHEMA public CASCADE — DİKKATLİ)
	@echo "⚠️  Şema sıfırlanıyor..."
	@PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-c "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"

migrate-status: ## Mevcut tablo listesini göster
	@PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-c "\dt public.*"

# ==============================================================================
# Kafka topic + ACL kurulumu — T-E01-08
# ==============================================================================

seed-kafka: ## Kafka topic'lerini ve ACL'leri oluşturur
	@bash scripts/kafka-setup.sh

kafka-list: ## Topic listesini göster
	@$(DC) exec -T kafka /opt/kafka/bin/kafka-topics.sh \
		--bootstrap-server localhost:9092 --list


# ==============================================================================
# Simülasyon ve doğrulama koşuları — Sprint 5
# ==============================================================================

.PHONY: simulate

CONFIG ?= configs/urban_ta.yaml

simulate: ## Bir senaryo koşusu başlat (CONFIG=configs/<senaryo>.yaml)
	HTS_CONFIG=$(CONFIG) $(GO) run ./cmd/simulator

# ==============================================================================
# Bütünlük ve izolasyon doğrulama — T-E01-12 / ADR-01, ADR-09
# ==============================================================================

.PHONY: verify-integrity verify-isolation

verify-integrity: ## ADR-01: hts_records ↔ ground_truth eşleşme kontrolü
	@echo "=== verify-integrity ==="
	@PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-t -A -c "\
		SELECT CASE WHEN count(*) = 0 THEN '✅ OK: eşleşmeyen kayıt yok' \
		       ELSE '❌ HATA: ' || count(*) || ' eşleşmeyen kayıt' END \
		FROM hts_records h \
		LEFT JOIN ground_truth g USING (run_id, event_id) \
		WHERE g.event_id IS NULL;"

verify-isolation: ## K6: S2/S4 rollerinin ground_truth'a erişemediğini doğrula (gerçek ACL testi)
	@bash scripts/verify-isolation.sh

# ==============================================================================
# Yardım
# ==============================================================================

.PHONY: help
help: ## Bu yardım metnini göster
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help

# ==============================================================================
# Bütünlük denetimi — Sprint 6 (E05, ADR-27..31)
# ==============================================================================

.PHONY: integrity-stream integrity-batch verify-k7

integrity-stream: ## Bütünlük akış fazı (kural 1, 3) — RUN_ID=... CONFIG=...
	HTS_INTEGRITY_MODE=stream HTS_CONFIG=$(CONFIG) HTS_RUN_ID=$(RUN_ID) \
		$(GO) run ./cmd/integrity

integrity-batch: ## Bütünlük toplu fazı (kural 5, 2) — RUN_ID=... CONFIG=...
	HTS_INTEGRITY_MODE=batch HTS_CONFIG=$(CONFIG) HTS_RUN_ID=$(RUN_ID) \
		$(GO) run ./cmd/integrity

verify-k7: ## K7 ölçümünü göster (integrity_metrics) — RUN_ID=...
	@PGPASSWORD=$(POSTGRES_PASSWORD) psql -h localhost -U $(POSTGRES_USER) -d $(POSTGRES_DB) -c "\
		SELECT rule_id, rule_name, findings, true_positives, injected, \
		       round(precision::numeric,4) AS precision, \
		       round(recall::numeric,4)    AS recall, \
		       sufficient, \
		       CASE WHEN precision IS NULL THEN 'ölçülemedi' \
		            WHEN NOT sufficient    THEN 'yetersiz' \
		            WHEN precision >= 0.90 THEN 'geçti' \
		            ELSE 'tutmadı' END AS k7 \
		  FROM integrity_metrics WHERE run_id = '$(RUN_ID)' ORDER BY rule_id;"
