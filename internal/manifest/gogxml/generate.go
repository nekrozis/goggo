package gogxml

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
)

const defaultBufferSize = 64 * 1024 // 64 KiB

// Generate calculates chunk MD5 hashes and the whole-file MD5 by streaming through r once.
//
// If the stream yields fewer or more bytes than the declared size, ErrStreamLengthMismatch is returned.
func Generate(r io.Reader, size int64, chunkSize int64, name string) (*FileXML, error) {
	if size < 0 {
		return nil, ErrNegativeSize
	}
	if chunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}

	// Edge case: 0-byte file
	if size == 0 {
		var probe [1]byte
		n, err := r.Read(probe[:])
		if n > 0 {
			return nil, ErrStreamLengthMismatch
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		f := &FileXML{
			Name:      name,
			Chunks:    0,
			TotalSize: 0,
			MD5:       emptyFileMD5,
			ChunkList: []ChunkXML{},
		}
		return f, f.Validate()
	}

	plans, err := SplitChunks(size, chunkSize)
	if err != nil {
		return nil, err
	}

	fileHasher := md5.New()
	chunks := make([]ChunkXML, len(plans))
	buf := make([]byte, defaultBufferSize)

	for i, plan := range plans {
		chunkLen := plan.To - plan.From + 1
		chunkHasher := md5.New()
		var chunkRead int64

		for chunkRead < chunkLen {
			toRead := int64(len(buf))
			if remaining := chunkLen - chunkRead; remaining < toRead {
				toRead = remaining
			}

			n, readErr := io.ReadFull(r, buf[:toRead])
			if n > 0 {
				chunkHasher.Write(buf[:n])
				fileHasher.Write(buf[:n])
				chunkRead += int64(n)
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
					return nil, ErrStreamLengthMismatch
				}
				return nil, readErr
			}
		}

		chunks[i] = ChunkXML{
			ID:     plan.ID,
			From:   plan.From,
			To:     plan.To,
			Method: "md5",
			Hash:   hex.EncodeToString(chunkHasher.Sum(nil)),
		}
	}

	// Verify stream has no excess bytes past the declared size
	var probe [1]byte
	n, err := r.Read(probe[:])
	if n > 0 {
		return nil, ErrStreamLengthMismatch
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	fileMD5 := hex.EncodeToString(fileHasher.Sum(nil))
	res := &FileXML{
		Name:      name,
		Chunks:    len(chunks),
		TotalSize: size,
		MD5:       fileMD5,
		ChunkList: chunks,
	}

	if err := res.Validate(); err != nil {
		return nil, err
	}
	return res, nil
}
