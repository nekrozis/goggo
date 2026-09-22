package gogxml

import (
	"fmt"
	"strings"
)

const emptyFileMD5 = "d41d8cd98f00b204e9800998ecf8427e"

// Validate verifies that the FileXML manifest satisfies all 12 domain invariants.
func (f *FileXML) Validate() error {
	// Rule 1: TotalSize cannot be negative
	if f.TotalSize < 0 {
		return &SemanticValidationError{Rule: 1, Message: "total_size cannot be negative"}
	}

	// Rule 2: Chunks attribute must equal the number of chunk elements
	if f.Chunks != len(f.ChunkList) {
		return &SemanticValidationError{
			Rule:    2,
			Message: fmt.Sprintf("chunks attribute (%d) does not match chunk element count (%d)", f.Chunks, len(f.ChunkList)),
		}
	}

	// Rule 12: File MD5 is always mandatory and must be a valid 32-hex string
	fileMD5 := strings.TrimSpace(f.MD5)
	if fileMD5 == "" || !isHex32(fileMD5) {
		return &SemanticValidationError{Rule: 12, Message: "file md5 is mandatory and must be a valid 32-hex string"}
	}

	// Rule 3: Empty file (0 bytes) must have 0 chunks, 0 chunk elements, and standard empty MD5
	if f.TotalSize == 0 {
		if f.Chunks != 0 || len(f.ChunkList) != 0 {
			return &SemanticValidationError{Rule: 3, Message: "empty file must have 0 chunks and 0 chunk elements"}
		}
		if !strings.EqualFold(fileMD5, emptyFileMD5) {
			return &SemanticValidationError{
				Rule:    3,
				Message: fmt.Sprintf("empty file md5 must be %s, got %s", emptyFileMD5, fileMD5),
			}
		}
		return nil
	}

	// Rule 4: Non-empty file must have Chunks > 0 and len(ChunkList) > 0
	if f.Chunks <= 0 || len(f.ChunkList) == 0 {
		return &SemanticValidationError{Rule: 4, Message: "non-empty file must contain at least 1 chunk"}
	}

	// Iterate through chunks to check Rules 5, 6, 7, 8, 10, 11
	for i := range f.ChunkList {
		c := &f.ChunkList[i]

		// Rule 5: Chunk IDs must be 0-indexed and strictly sequential
		if c.ID != i {
			return &SemanticValidationError{
				Rule:    5,
				Message: fmt.Sprintf("chunk id mismatch at index %d: got %d, want %d", i, c.ID, i),
			}
		}

		// Rule 6: from >= 0 and to >= from
		if c.From < 0 || c.To < c.From {
			return &SemanticValidationError{
				Rule:    6,
				Message: fmt.Sprintf("chunk %d has invalid range [%d, %d]", i, c.From, c.To),
			}
		}

		// Rule 7: First chunk must start at byte 0
		if i == 0 && c.From != 0 {
			return &SemanticValidationError{
				Rule:    7,
				Message: fmt.Sprintf("first chunk must start at byte 0, starts at %d", c.From),
			}
		}

		// Rule 8: Chunk ranges must be contiguous without gaps or overlaps
		if i > 0 {
			prevTo := f.ChunkList[i-1].To
			expectedFrom := prevTo + 1
			if c.From != expectedFrom {
				return &SemanticValidationError{
					Rule:    8,
					Message: fmt.Sprintf("gap or overlap detected between chunk %d and %d: chunk %d ends at %d, chunk %d begins at %d", i-1, i, i-1, prevTo, i, c.From),
				}
			}
		}

		// Rule 10: Method must be md5 (case-insensitive)
		if !strings.EqualFold(strings.TrimSpace(c.Method), "md5") {
			return &SemanticValidationError{
				Rule:    10,
				Message: fmt.Sprintf("chunk %d has unsupported method %q, only \"md5\" is supported", i, c.Method),
			}
		}

		// Rule 11: Chunk hash must be valid 32-hex string
		hash := strings.TrimSpace(c.Hash)
		if !isHex32(hash) {
			return &SemanticValidationError{
				Rule:    11,
				Message: fmt.Sprintf("chunk %d hash %q is not a valid 32-character hex md5", i, c.Hash),
			}
		}
	}

	// Rule 9: Last chunk must end exactly at TotalSize - 1
	lastChunk := &f.ChunkList[len(f.ChunkList)-1]
	expectedLastTo := f.TotalSize - 1
	if lastChunk.To != expectedLastTo {
		return &SemanticValidationError{
			Rule:    9,
			Message: fmt.Sprintf("last chunk does not cover file end: ends at %d, expected %d (total_size %d - 1)", lastChunk.To, expectedLastTo, f.TotalSize),
		}
	}

	return nil
}

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			continue
		}
		return false
	}
	return true
}
