package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"backend-business-permit-data/db"
	"backend-business-permit-data/schema"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func main() {
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	gormDB, err := db.Connect(dbURL)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}

	sqlDB, err := gormDB.DB()
	if err == nil {
		defer sqlDB.Close()
	}

	adminToken := os.Getenv("ADMIN_API_TOKEN")
	mux := http.NewServeMux()

	// Zero-DB keep-alive endpoint for UptimeRobot
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Admin: generate API key
	mux.HandleFunc("POST /admin/keys", func(w http.ResponseWriter, r *http.Request) {
		if adminToken == "" || r.Header.Get("X-Admin-Token") != adminToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var req struct {
			UserID       string `json:"user_id"`
			Name         string `json:"name"`
			MonthlyLimit int    `json:"monthly_limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.UserID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
			return
		}
		if req.MonthlyLimit <= 0 {
			req.MonthlyLimit = 250
		}

		rawKey, prefix, hash, err := generateAPIKey()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key generation failed"})
			return
		}

		keyRec := schema.APIKey{
			UserID:       strings.TrimSpace(req.UserID),
			Name:         strings.TrimSpace(req.Name),
			KeyPrefix:    prefix,
			KeyHash:      hash,
			MonthlyLimit: req.MonthlyLimit,
		}
		if err := gormDB.WithContext(r.Context()).Create(&keyRec).Error; err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save api key failed"})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"api_key":       rawKey,
			"key_prefix":    prefix,
			"monthly_limit": req.MonthlyLimit,
		})
	})

	// Admin: upsert document + chunks
	mux.HandleFunc("POST /admin/ingest", func(w http.ResponseWriter, r *http.Request) {
		if adminToken == "" || r.Header.Get("X-Admin-Token") != adminToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var doc schema.IngestDocumentInput
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if strings.TrimSpace(doc.Identifier) == "" || strings.TrimSpace(doc.Title) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "identifier and title are required"})
			return
		}

		var docID uint
		err := gormDB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			status := strings.TrimSpace(doc.Status)
			if status == "" {
				status = "in_force"
			}
			rec := schema.Document{
				Country:        strings.ToUpper(strings.TrimSpace(doc.Country)),
				Agency:         strings.ToUpper(strings.TrimSpace(doc.Agency)),
				DocType:        strings.TrimSpace(doc.DocType),
				Identifier:     strings.TrimSpace(doc.Identifier),
				Title:          strings.TrimSpace(doc.Title),
				SourceURL:      strings.TrimSpace(doc.SourceURL),
				Status:         status,
				RawHTMLHash:    strings.TrimSpace(doc.RawHTMLHash),
				LastVerifiedAt: time.Now().UTC(),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "country"}, {Name: "agency"}, {Name: "identifier"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"doc_type", "title", "source_url", "status", "raw_html_hash", "last_verified_at", "updated_at",
				}),
			}).Create(&rec).Error; err != nil {
				return err
			}
			docID = rec.ID

			for _, sec := range doc.Sections {
				chunk := schema.Chunk{
					DocumentID:    docID,
					SectionNumber: strings.TrimSpace(sec.SectionNumber),
					Heading:       strings.TrimSpace(sec.Heading),
					HierarchyPath: strings.TrimSpace(sec.HierarchyPath),
					VerbatimText:  sec.VerbatimText,
					ContentHash:   strings.TrimSpace(sec.ContentHash),
					RegConfig:     "english",
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "document_id"}, {Name: "section_number"}, {Name: "content_hash"}},
					DoNothing: true,
				}).Create(&chunk).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"document_id": docID, "status": "upserted"})
	})

	// Protected search endpoint with atomic monthly quota check
	mux.Handle("GET /v1/search", requireQuota(gormDB, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 15 {
			limit = 5
		}

		query := gormDB.WithContext(r.Context()).
			Table("chunks AS c").
			Select(`
				d.country, d.agency, d.identifier, d.title,
				c.section_number, c.heading, c.hierarchy_path, c.verbatim_text,
				d.status, d.source_url, d.last_verified_at::text AS last_verified_at
			`).
			Joins("JOIN documents d ON d.id = c.document_id").
			Where("c.search_tsv @@ websearch_to_tsquery(c.reg_config::regconfig, ?)", q)

		if c := strings.TrimSpace(r.URL.Query().Get("country")); c != "" {
			query = query.Where("d.country = ?", strings.ToUpper(c))
		}
		if a := strings.TrimSpace(r.URL.Query().Get("agency")); a != "" {
			query = query.Where("d.agency = ?", strings.ToUpper(a))
		}

		var matches []schema.LawMatch
		if err := query.
			Order(gorm.Expr("ts_rank_cd(c.search_tsv, websearch_to_tsquery(c.reg_config::regconfig, ?)) DESC", q)).
			Limit(limit).
			Scan(&matches).Error; err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
			return
		}

		for i := range matches {
			if matches[i].Country == "SG" {
				matches[i].CopyrightNotice = schema.SGCopyrightNotice
				matches[i].AuthoritativeNote = schema.SGAuthoritativeNote
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"results": matches})
	})))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:          withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("server running on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func requireQuota(db *gorm.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing Bearer API key"})
			return
		}
		rawKey := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if rawKey == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "empty API key"})
			return
		}

		errUnauthorized := errors.New("unauthorized")
		errQuota := errors.New("quota exceeded")

		err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			var keyRec schema.APIKey
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("key_hash = ? AND revoked_at IS NULL", hashKey(rawKey)).
				First(&keyRec).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errUnauthorized
				}
				return err
			}

			now := time.Now().UTC()
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

			var used int64
			if err := tx.Model(&schema.UsageLog{}).
				Where("api_key_id = ? AND created_at >= ?", keyRec.ID, monthStart).
				Count(&used).Error; err != nil {
				return err
			}
			if int(used) >= keyRec.MonthlyLimit {
				return errQuota
			}

			return tx.Create(&schema.UsageLog{
				APIKeyID:  keyRec.ID,
				Endpoint:  r.URL.Path,
				QueryText: r.URL.Query().Get("q"),
			}).Error
		})

		if err != nil {
			switch {
			case errors.Is(err, errUnauthorized):
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked API key"})
			case errors.Is(err, errQuota):
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "monthly quota exceeded"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "quota check failed"})
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func generateAPIKey() (raw, prefix, hash string, err error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("rand: %w", err)
	}
	raw = "rk_live_" + hex.EncodeToString(buf)
	prefix = raw[:16]
	hash = hashKey(raw)
	return raw, prefix, hash, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
