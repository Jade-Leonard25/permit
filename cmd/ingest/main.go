package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"backend-business-permit-data/schema"

	"github.com/joho/godotenv"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func inSGTSafeWindow(nowUTC time.Time) bool {
	sgt := time.FixedZone("SGT", 8*3600)
	t := nowUTC.In(sgt)
	minutes := t.Hour()*60 + t.Minute()
	// 03:00 SGT (180) to 06:50 SGT (410) buffer before 07:00 cutoff
	return minutes >= 180 && minutes <= 410
}

func parseSections(rawText string) []schema.IngestSectionInput {
	lines := strings.Split(strings.ReplaceAll(rawText, "\r\n", "\n"), "\n")
	secStartRe := regexp.MustCompile(`^([1-9][0-9]*)\.\s*—\s*(.+)$`)
	partRe := regexp.MustCompile(`^PART\s+([0-9]+)\b(.*)$`)

	currentPart := "Preliminary"
	var sections []schema.IngestSectionInput
	var curNum, curHeading string
	var curLines []string

	flush := func() {
		if curNum == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(curLines, "\n"))
		if text == "" {
			return
		}
		sections = append(sections, schema.IngestSectionInput{
			SectionNumber: curNum,
			Heading:       strings.TrimSpace(curHeading),
			HierarchyPath: fmt.Sprintf("%s > Section %s", currentPart, curNum),
			VerbatimText:  text,
			ContentHash:   sha256Hex(text),
		})
		curNum = ""
		curHeading = ""
		curLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if curNum != "" {
				curLines = append(curLines, "")
			}
			continue
		}

		if m := partRe.FindStringSubmatch(strings.ToUpper(trimmed)); len(m) >= 2 {
			flush()
			currentPart = "Part " + m[1]
			continue
		}

		if m := secStartRe.FindStringSubmatch(trimmed); len(m) == 3 {
			num, err := strconv.Atoi(m[1])
			if err == nil && num >= 1 && num <= 25 {
				flush()
				curNum = m[1]
				curHeading = m[2]
				curLines = []string{trimmed}
				continue
			}
		}

		if curNum != "" {
			curLines = append(curLines, trimmed)
		}
	}

	flush()
	return sections
}

func main() {
	_ = godotenv.Load()

	if os.Getenv("ENFORCE_SGT_WINDOW") == "true" && !inSGTSafeWindow(time.Now().UTC()) {
		log.Fatal("outside allowed Singapore SSO ingestion window (03:00-07:00 SGT)")
	}

	apiURL := os.Getenv("INGEST_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080/admin/ingest"
	}
	adminToken := os.Getenv("ADMIN_API_TOKEN")
	if adminToken == "" {
		log.Fatal("ADMIN_API_TOKEN is required")
	}

	textPath := "/tmp/asa2007.txt"
	if len(os.Args) > 1 {
		textPath = os.Args[1]
	}

	rawBytes, err := os.ReadFile(textPath)
	if err != nil {
		log.Fatalf("read %s (run: pdftotext \"Accounting Standards Act 2007.pdf\" /tmp/asa2007.txt): %v", textPath, err)
	}
	fullText := string(rawBytes)
	sections := parseSections(fullText)
	if len(sections) == 0 {
		log.Fatal("parsed 0 sections; check /tmp/asa2007.txt formatting")
	}

	payload := schema.IngestDocumentInput{
		Country:     "SG",
		Agency:      "AGC",
		DocType:     "Act",
		Identifier:  "ASA2007",
		Title:       "Accounting Standards Act 2007",
		SourceURL:   "https://sso.agc.gov.sg/Act/ASA2007?WholeDoc=1",
		Status:      "in_force",
		RawHTMLHash: sha256Hex(fullText),
		Sections:    sections,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("marshal payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		log.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", adminToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("post ingest: %v", err)
	}
	defer resp.Body.Close()

	log.Printf("Ingested %d sections -> %s", len(sections), resp.Status)
}
