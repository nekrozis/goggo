package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nekrozis/goggo/internal/manifest/gogxml"
)

// openKind classifies an open/stat failure under the frozen error taxonomy:
// MISSING_* means the file is not there, IO_ERROR means it may well be there
// and could not be reached. Collapsing the two would tell a user their
// manifest is absent when the real answer is "unreadable".
func openKind(err error, missing string) string {
	if errors.Is(err, fs.ErrNotExist) {
		return missing
	}
	return "IO_ERROR"
}

type manifestErrorJSON struct {
	TargetFile   string `json:"target_file,omitempty"`
	ManifestFile string `json:"manifest_file,omitempty"`
	Status       string `json:"status"`
	ErrorKind    string `json:"error_kind"`
	Message      string `json:"message"`
}

// verifyResultJSON renders a VerifyReport under the frozen JSON status names.
// The outer Status shadows the embedded one: the domain reports what was
// verified (CHUNK_VERIFIED / CHUNK_MISMATCH), the contract says OK / CORRUPT.
type verifyResultJSON struct {
	*gogxml.VerifyReport
	Status string `json:"status"`
}

func jsonVerifyStatus(s gogxml.VerifyStatus) string {
	if s == gogxml.StatusChunkVerified {
		return "OK"
	}
	return "CORRUPT"
}

func writeJSONError(w io.Writer, targetFile, manifestFile, errorKind, message string) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(manifestErrorJSON{
		TargetFile:   targetFile,
		ManifestFile: manifestFile,
		Status:       "ERROR",
		ErrorKind:    errorKind,
		Message:      message,
	})
}

func runManifestInspect(inv invocation, stdout, stderr io.Writer) outcome {
	target := inv.target.Product
	if target == "" {
		fmt.Fprintln(stderr, "Error: manifest inspect requires a manifest file path")
		return outcomeUsageFailure
	}

	f, err := os.Open(target)
	if err != nil {
		kind := openKind(err, "MISSING_MANIFEST")
		if inv.json {
			writeJSONError(stdout, "", target, kind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: cannot open manifest %s: %v\n", target, err)
		}
		return outcomeUsageFailure
	}
	defer f.Close()

	manifest, err := gogxml.Parse(f)
	if err != nil {
		errorKind := "XML_PARSE_ERROR"
		var semErr *gogxml.SemanticValidationError
		if errors.As(err, &semErr) {
			errorKind = "MANIFEST_SEMANTIC_ERROR"
		}
		if inv.json {
			writeJSONError(stdout, "", target, errorKind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: %s: %v\n", errorKind, err)
		}
		return outcomeUsageFailure
	}

	if inv.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(manifest)
		return outcomeOK
	}

	fmt.Fprintf(stdout, "Manifest: %s\n", target)
	fmt.Fprintf(stdout, "  File:        %s\n", manifest.Name)
	fmt.Fprintf(stdout, "  Total Size:  %d bytes\n", manifest.TotalSize)
	fmt.Fprintf(stdout, "  Total Chunks: %d\n", manifest.Chunks)
	fmt.Fprintf(stdout, "  File MD5:    %s\n", manifest.MD5)
	if len(manifest.ChunkList) > 0 {
		fmt.Fprintf(stdout, "Chunks:\n")
		for _, c := range manifest.ChunkList {
			fmt.Fprintf(stdout, "  [%3d] range: %10d - %10d (%8d bytes) md5: %s\n",
				c.ID, c.From, c.To, c.To-c.From+1, c.Hash)
		}
	}
	return outcomeOK
}

func resolveManifestPath(targetFile, explicitXML, gameName, xmlDir string) (string, error) {
	// Step 1: Explicit --xml flag. A miss is reported with the stat error
	// attached, so the caller can tell "not there" from "unreachable"; a
	// present-but-unreachable explicit path must not fall through to a search.
	if explicitXML != "" {
		if _, err := os.Stat(explicitXML); err != nil {
			return "", fmt.Errorf("explicit manifest %s unavailable: %w", explicitXML, err)
		}
		return explicitXML, nil
	}

	baseName := filepath.Base(targetFile) + ".xml"

	// statCandidate distinguishes "absent, keep searching" from "something is
	// wrong, stop and say so".
	statCandidate := func(candidate string) (keepSearching bool, err error) {
		_, err = os.Stat(candidate)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, fs.ErrNotExist):
			return true, nil
		default:
			return false, err
		}
	}

	// Step 2: Game cache directory
	if gameName != "" {
		candidate := filepath.Join(xmlDir, gameName, baseName)
		cont, err := statCandidate(candidate)
		if err != nil {
			return "", err
		}
		if !cont {
			return candidate, nil
		}
	}

	// Step 3: Global cache directory
	candidate := filepath.Join(xmlDir, baseName)
	if cont, err := statCandidate(candidate); err != nil {
		return "", err
	} else if !cont {
		return candidate, nil
	}

	// Step 4: Sibling file in same directory
	candidate = filepath.Join(filepath.Dir(targetFile), baseName)
	if cont, err := statCandidate(candidate); err != nil {
		return "", err
	} else if !cont {
		return candidate, nil
	}

	return "", fmt.Errorf("no manifest xml found for %s (searched canonical paths)", filepath.Base(targetFile))
}

func runManifestVerify(inv invocation, stdout, stderr io.Writer) outcome {
	target := inv.target.Product
	if target == "" {
		fmt.Fprintln(stderr, "Error: manifest verify requires a target file path")
		return outcomeUsageFailure
	}

	targetF, err := os.Open(target)
	if err != nil {
		kind := openKind(err, "MISSING_FILE")
		if inv.json {
			writeJSONError(stdout, target, inv.xmlPath, kind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: cannot open target file %s: %v\n", target, err)
		}
		return outcomeUsageFailure
	}
	defer targetF.Close()

	fi, err := targetF.Stat()
	if err != nil {
		if inv.json {
			writeJSONError(stdout, target, inv.xmlPath, "IO_ERROR", err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: cannot stat target file %s: %v\n", target, err)
		}
		return outcomeUsageFailure
	}

	resolvedXML, err := resolveManifestPath(target, inv.xmlPath, inv.cfg.GameRegex, inv.cfg.XMLDirectory)
	if err != nil {
		kind := openKind(err, "MISSING_MANIFEST")
		if inv.json {
			writeJSONError(stdout, target, inv.xmlPath, kind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: %v\n", err)
		}
		return outcomeUsageFailure
	}

	xmlF, err := os.Open(resolvedXML)
	if err != nil {
		kind := openKind(err, "MISSING_MANIFEST")
		if inv.json {
			writeJSONError(stdout, target, resolvedXML, kind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: cannot open manifest %s: %v\n", resolvedXML, err)
		}
		return outcomeUsageFailure
	}
	defer xmlF.Close()

	manifest, err := gogxml.Parse(xmlF)
	if err != nil {
		errorKind := "XML_PARSE_ERROR"
		var semErr *gogxml.SemanticValidationError
		if errors.As(err, &semErr) {
			errorKind = "MANIFEST_SEMANTIC_ERROR"
		}
		if inv.json {
			writeJSONError(stdout, target, resolvedXML, errorKind, err.Error())
		} else {
			fmt.Fprintf(stderr, "Error: %s in %s: %v\n", errorKind, resolvedXML, err)
		}
		return outcomeUsageFailure
	}

	// Manifest applicability check
	if inv.xmlPath != "" && filepath.Base(target) != manifest.Name {
		if !inv.json {
			fmt.Fprintf(stderr, "warning: manifest name %q does not match target file name %q\n", manifest.Name, filepath.Base(target))
		}
	}

	report, err := gogxml.Verify(targetF, fi.Size(), manifest)
	if err != nil {
		if inv.json {
			writeJSONError(stdout, target, resolvedXML, "IO_ERROR", err.Error())
		} else {
			fmt.Fprintf(stderr, "Error during verification: %v\n", err)
		}
		return outcomeOperationFailure
	}

	report.TargetFile = target
	report.ManifestFile = resolvedXML

	if inv.json {
		// The JSON contract names its status OK / CORRUPT / ERROR; the domain
		// names it after what was verified. A size mismatch is an ERROR here —
		// the manifest never applied — while a corrupt file is CORRUPT.
		switch report.Status {
		case gogxml.StatusSizeMismatch:
			writeJSONError(stdout, target, resolvedXML, "SIZE_MISMATCH",
				fmt.Sprintf("file is %d bytes, manifest declares %d", report.ActualSize, report.ExpectedSize))
		default:
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(verifyResultJSON{
				VerifyReport: report,
				Status:       jsonVerifyStatus(report.Status),
			})
		}
		if report.Status == gogxml.StatusChunkVerified {
			return outcomeOK
		}
		return outcomeOperationFailure
	}

	fmt.Fprintf(stdout, "Target:   %s (%d bytes)\n", target, fi.Size())
	fmt.Fprintf(stdout, "Manifest: %s\n", resolvedXML)
	fmt.Fprintf(stdout, "Status:   %s\n", report.Status)

	if report.Status == gogxml.StatusSizeMismatch {
		fmt.Fprintf(stdout, "Error: size mismatch (expected %d bytes, got %d bytes)\n", report.ExpectedSize, report.ActualSize)
		return outcomeOperationFailure
	}

	fmt.Fprintf(stdout, "Chunks:   %d total, %d corrupt\n", report.TotalChunks, report.CorruptChunks)
	fmt.Fprintf(stdout, "File MD5: expected %s, actual %s (match: %v)\n", report.ExpectedMD5, report.ActualMD5, report.FileMD5Match)

	if report.CorruptChunks > 0 {
		fmt.Fprintf(stdout, "Corrupt Chunks:\n")
		for _, c := range report.Chunks {
			if !c.OK {
				fmt.Fprintf(stdout, "  [%3d] [%10d, %10d] expected %s, actual %s\n", c.ID, c.From, c.To, c.ExpectedMD5, c.ActualMD5)
			}
		}
	}

	if report.Status == gogxml.StatusChunkVerified {
		return outcomeOK
	}
	return outcomeOperationFailure
}

func runManifestCreate(inv invocation, stdout, stderr io.Writer) outcome {
	target := inv.target.Product
	if target == "" {
		fmt.Fprintln(stderr, "Error: manifest create requires a target file path")
		return outcomeUsageFailure
	}

	f, err := os.Open(target)
	if err != nil {
		fmt.Fprintf(stderr, "Error: cannot open %s: %v\n", target, err)
		return outcomeUsageFailure
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		fmt.Fprintf(stderr, "Error: cannot stat %s: %v\n", target, err)
		return outcomeUsageFailure
	}

	chunkSize := inv.cfg.DownloadConfig.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 10 * 1024 * 1024
	}

	manifest, err := gogxml.Generate(f, fi.Size(), chunkSize, filepath.Base(target))
	if err != nil {
		fmt.Fprintf(stderr, "Error generating manifest: %v\n", err)
		return outcomeOperationFailure
	}

	data, err := gogxml.Marshal(manifest)
	if err != nil {
		fmt.Fprintf(stderr, "Error marshaling manifest: %v\n", err)
		return outcomeOperationFailure
	}

	// Output to stdout if -o -. A write failure — including a short write — is
	// an operation failure: a half-emitted manifest must not exit 0.
	if inv.outputFile == "-" {
		n, err := stdout.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			fmt.Fprintf(stderr, "Error writing manifest to stdout: %v\n", err)
			return outcomeOperationFailure
		}
		return outcomeOK
	}

	outputPath := inv.outputFile
	if outputPath == "" {
		baseName := filepath.Base(target) + ".xml"
		if inv.cfg.GameRegex != "" {
			outputPath = filepath.Join(inv.cfg.XMLDirectory, inv.cfg.GameRegex, baseName)
		} else {
			outputPath = filepath.Join(inv.cfg.XMLDirectory, baseName)
		}
	}

	if err := gogxml.WriteAtomic(outputPath, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "Error writing manifest to %s: %v\n", outputPath, err)
		return outcomeOperationFailure
	}

	fmt.Fprintf(stdout, "Created XML manifest: %s\n", outputPath)
	return outcomeOK
}
