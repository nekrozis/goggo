package gogxml

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"strings"
)

// Verify checks the content of r against manifest.
//
// The caller provides size, the true physical length of the content.
// If size != manifest.TotalSize, Verify immediately returns a VerifyReport with
// StatusSizeMismatch without performing chunk reads.
//
// If sizes match, each chunk is verified sequentially; corrupt chunks are recorded
// and checking continues until all chunks have been inspected.
func Verify(r io.ReaderAt, size int64, manifest *FileXML) (*VerifyReport, error) {
	if manifest == nil {
		return nil, ErrManifestNotFound
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}

	report := &VerifyReport{
		ExpectedSize: manifest.TotalSize,
		ActualSize:   size,
		ExpectedMD5:  manifest.MD5,
		TotalChunks:  manifest.Chunks,
	}

	// Size guard: if sizes mismatch, do not read any chunk data
	if size != manifest.TotalSize {
		report.Status = StatusSizeMismatch
		report.ActualMD5 = ""
		report.CorruptChunks = 0
		return report, nil
	}

	// Edge case: 0-byte file
	if size == 0 {
		report.Status = StatusChunkVerified
		report.ActualMD5 = emptyFileMD5
		report.FileMD5Match = strings.EqualFold(manifest.MD5, emptyFileMD5)
		report.CorruptChunks = 0
		report.Chunks = []ChunkResult{}
		return report, nil
	}

	fileHasher := md5.New()
	chunkResults := make([]ChunkResult, len(manifest.ChunkList))
	corruptCount := 0
	buf := make([]byte, defaultBufferSize)

	for i := range manifest.ChunkList {
		c := &manifest.ChunkList[i]
		chunkLen := c.To - c.From + 1
		chunkHasher := md5.New()
		secReader := io.NewSectionReader(r, c.From, chunkLen)
		var chunkRead int64

		for chunkRead < chunkLen {
			toRead := int64(len(buf))
			if remaining := chunkLen - chunkRead; remaining < toRead {
				toRead = remaining
			}
			n, err := io.ReadFull(secReader, buf[:toRead])
			if n > 0 {
				chunkHasher.Write(buf[:n])
				fileHasher.Write(buf[:n])
				chunkRead += int64(n)
			}
			if err != nil {
				return nil, err
			}
		}

		actualChunkMD5 := hex.EncodeToString(chunkHasher.Sum(nil))
		expectedChunkMD5 := strings.ToLower(strings.TrimSpace(c.Hash))
		chunkOK := strings.EqualFold(actualChunkMD5, expectedChunkMD5)

		if !chunkOK {
			corruptCount++
		}

		chunkResults[i] = ChunkResult{
			ID:          c.ID,
			From:        c.From,
			To:          c.To,
			ExpectedMD5: expectedChunkMD5,
			ActualMD5:   actualChunkMD5,
			OK:          chunkOK,
		}
	}

	actualFileMD5 := hex.EncodeToString(fileHasher.Sum(nil))
	fileMD5Match := strings.EqualFold(actualFileMD5, strings.TrimSpace(manifest.MD5))

	report.ActualMD5 = actualFileMD5
	report.FileMD5Match = fileMD5Match
	report.CorruptChunks = corruptCount
	report.Chunks = chunkResults

	if corruptCount == 0 && fileMD5Match {
		report.Status = StatusChunkVerified
	} else {
		report.Status = StatusChunkMismatch
	}

	return report, nil
}
