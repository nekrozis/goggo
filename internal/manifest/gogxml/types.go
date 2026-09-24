package gogxml

import (
	"encoding/xml"
	"errors"
	"fmt"
)

// FileXML represents the root <file> element in a GOG checksum document.
type FileXML struct {
	XMLName   xml.Name   `xml:"file" json:"-"`
	Name      string     `xml:"name,attr" json:"file"`
	MD5       string     `xml:"md5,attr" json:"md5"` // Mandatory 32-hex lowercase/uppercase
	ChunkList []ChunkXML `xml:"chunk" json:"chunk_list"`
	Chunks    int        `xml:"chunks,attr" json:"chunks"`
	TotalSize int64      `xml:"total_size,attr" json:"total_size"`
}

// ChunkXML represents one <chunk> element in a GOG checksum document.
type ChunkXML struct {
	Method string `xml:"method,attr" json:"method"`
	Hash   string `xml:",chardata" json:"hash"`
	ID     int    `xml:"id,attr" json:"id"`
	From   int64  `xml:"from,attr" json:"from"`
	To     int64  `xml:"to,attr" json:"to"`
}

// ChunkPlan describes the byte range of one chunk slice.
type ChunkPlan struct {
	ID   int
	From int64
	To   int64
}

// VerifyStatus models the verification conclusion for an existing file.
type VerifyStatus string

const (
	StatusChunkVerified VerifyStatus = "CHUNK_VERIFIED"
	StatusChunkMismatch VerifyStatus = "CHUNK_MISMATCH"
	StatusSizeMismatch  VerifyStatus = "SIZE_MISMATCH"
	StatusSizeOnly      VerifyStatus = "SIZE_ONLY"
)

// ChunkResult records the result of verifying an individual chunk.
type ChunkResult struct {
	ExpectedMD5 string `json:"expected_md5"`
	ActualMD5   string `json:"actual_md5"`
	ID          int    `json:"id"`
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	OK          bool   `json:"ok"`
}

// VerifyReport is the complete conclusion returned by Verify.
type VerifyReport struct {
	TargetFile    string        `json:"target_file"`
	ManifestFile  string        `json:"manifest_file,omitempty"`
	Status        VerifyStatus  `json:"status"`
	ExpectedMD5   string        `json:"expected_md5"`
	ActualMD5     string        `json:"actual_md5,omitempty"`
	Chunks        []ChunkResult `json:"chunks,omitempty"`
	ExpectedSize  int64         `json:"expected_size"`
	ActualSize    int64         `json:"actual_size"`
	CorruptChunks int           `json:"corrupt_chunks"`
	TotalChunks   int           `json:"total_chunks"`
	FileMD5Match  bool          `json:"file_md5_match"`
}

// Domain errors.
var (
	ErrNegativeSize         = errors.New("gogxml: total_size cannot be negative")
	ErrInvalidChunkSize     = errors.New("gogxml: chunk size must be positive")
	ErrStreamLengthMismatch = errors.New("gogxml: stream length does not match declared size")
	ErrSizeMismatch         = errors.New("gogxml: actual file size does not match manifest total_size")
	ErrManifestNotFound     = errors.New("gogxml: manifest xml not found")
)

// SemanticValidationError records a failure of manifest domain invariants.
type SemanticValidationError struct {
	Message string
	Rule    int
}

func (e *SemanticValidationError) Error() string {
	return fmt.Sprintf("gogxml semantic error (rule %d): %s", e.Rule, e.Message)
}
