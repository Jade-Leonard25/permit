package schema

import "time"

const (
	SGCopyrightNotice   = "Singapore legislation is subject to copyright of the Singapore Government and is reproduced with the permission of the Attorney-General's Chambers of Singapore. Check Singapore Statutes Online (sso.agc.gov.sg) for the latest version."
	SGAuthoritativeNote = "Unofficial consolidation sourced from Singapore Statutes Online; refer to the Singapore Government Gazette for the authoritative text."
)

type Document struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	Country        string     `gorm:"size:16;not null;uniqueIndex:idx_doc_unique,priority:1" json:"country"`
	Agency         string     `gorm:"size:32;not null;uniqueIndex:idx_doc_unique,priority:2" json:"agency"`
	DocType        string     `gorm:"size:64;not null" json:"doc_type"`
	Identifier     string     `gorm:"size:128;not null;uniqueIndex:idx_doc_unique,priority:3" json:"identifier"`
	Title          string     `gorm:"type:text;not null" json:"title"`
	SourceURL      string     `gorm:"type:text;not null" json:"source_url"`
	Status         string     `gorm:"size:32;not null;default:in_force" json:"status"`
	EffectiveDate  *time.Time `gorm:"type:date" json:"effective_date,omitempty"`
	SupersededAt   *time.Time `gorm:"type:date" json:"superseded_at,omitempty"`
	RawHTMLHash    string     `gorm:"size:128;not null" json:"raw_html_hash"`
	LastVerifiedAt time.Time  `gorm:"not null;autoCreateTime" json:"last_verified_at"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	Chunks         []Chunk    `gorm:"constraint:OnDelete:CASCADE" json:"chunks,omitempty"`
}

type Chunk struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	DocumentID    uint   `gorm:"not null;index;uniqueIndex:idx_chunk_unique,priority:1" json:"document_id"`
	SectionNumber string `gorm:"size:64;not null;default:'';uniqueIndex:idx_chunk_unique,priority:2" json:"section_number"`
	Heading       string `gorm:"type:text;not null;default:''" json:"heading"`
	HierarchyPath string `gorm:"type:text;not null;default:''" json:"hierarchy_path"`
	VerbatimText  string `gorm:"type:text;not null" json:"verbatim_text"`
	ContentHash   string `gorm:"size:128;not null;uniqueIndex:idx_chunk_unique,priority:3" json:"content_hash"`
	RegConfig     string `gorm:"size:32;not null;default:english" json:"reg_config"`
}

type APIKey struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	UserID       string     `gorm:"size:128;not null;index" json:"user_id"`
	Name         string     `gorm:"size:128;not null" json:"name"`
	KeyPrefix    string     `gorm:"size:32;not null" json:"key_prefix"`
	KeyHash      string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	MonthlyLimit int        `gorm:"not null;default:250" json:"monthly_limit"`
	RevokedAt    *time.Time `gorm:"index" json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

type UsageLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	APIKeyID    uint      `gorm:"not null;index:idx_usage_key_created,priority:1" json:"api_key_id"`
	Endpoint    string    `gorm:"size:128;not null" json:"endpoint"`
	QueryText   string    `gorm:"type:text;not null;default:''" json:"query_text"`
	ResultCount int       `gorm:"not null;default:0" json:"result_count"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index:idx_usage_key_created,priority:2" json:"created_at"`
}

type LawMatch struct {
	Country           string `json:"country"`
	Agency            string `json:"agency"`
	Identifier        string `json:"identifier"`
	Title             string `json:"title"`
	SectionNumber     string `json:"section_number"`
	Heading           string `json:"heading"`
	HierarchyPath     string `json:"hierarchy_path"`
	VerbatimText      string `json:"verbatim_text"`
	Status            string `json:"status"`
	SourceURL         string `json:"source_url"`
	LastVerifiedAt    string `json:"last_verified_at"`
	CopyrightNotice   string `json:"copyright_notice,omitempty"`
	AuthoritativeNote string `json:"authoritative_note,omitempty"`
}

type IngestSectionInput struct {
	SectionNumber string `json:"section_number"`
	Heading       string `json:"heading"`
	HierarchyPath string `json:"hierarchy_path"`
	VerbatimText  string `json:"verbatim_text"`
	ContentHash   string `json:"content_hash"`
}

type IngestDocumentInput struct {
	Country     string               `json:"country"`
	Agency      string               `json:"agency"`
	DocType     string               `json:"doc_type"`
	Identifier  string               `json:"identifier"`
	Title       string               `json:"title"`
	SourceURL   string               `json:"source_url"`
	Status      string               `json:"status"`
	RawHTMLHash string               `json:"raw_html_hash"`
	Sections    []IngestSectionInput `json:"sections"`
}
