package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ManifestName is the reserved name of the adoption manifest that lives beside
// the payload in an adopted experiment directory. It is never part of the
// payload inventory (a manifest cannot hash itself) and a benchmark must not
// produce it: the runner fails a run whose output directory contains it.
const ManifestName = "manifest.json"

// PayloadFile is one regular file of the artifact payload.
type PayloadFile struct {
	Path   string `json:"path"` // slash-separated, relative to the payload root
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ArtifactPayload lists the payload of an artifact directory: every regular
// file below dir except the reserved manifest name, sorted by path. Symlinks
// and other non-regular entries are rejected, so a swapped file cannot hide.
func ArtifactPayload(dir string) ([]PayloadFile, error) {
	files, err := artifactPayload(dir, "", nil)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// overridePath/overrideBytes let a caller substitute the bytes of one file:
// the preview server reads the file it is about to return and then hashes the
// payload with those exact bytes, so the response cannot contain bytes that
// were not part of the verified digest.
func artifactPayload(dir, overridePath string, overrideBytes []byte) ([]PayloadFile, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("provenance: open payload root %s: %w", dir, err)
	}
	defer root.Close()

	var files []PayloadFile
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("provenance: %s is not a regular file (symlinks and specials are rejected)", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ManifestName {
			return nil
		}
		var (
			size int64
			sum  string
		)
		if overrideBytes != nil && rel == overridePath {
			size = int64(len(overrideBytes))
			sum = SHA256Hex(overrideBytes)
		} else {
			f, err := root.Open(filepath.FromSlash(rel))
			if err != nil {
				return err
			}
			h := sha256.New()
			if _, err := io.Copy(h, f); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			size = info.Size()
			sum = hex.EncodeToString(h.Sum(nil))
		}
		files = append(files, PayloadFile{Path: rel, Size: size, SHA256: sum})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("provenance: payload %s: %w", dir, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// PayloadDigest hashes a payload inventory with the canonical encoding of the
// design: a sorted "path \0 size \0 sha256 \n" concatenation. The manifest is
// excluded by ArtifactPayload.
func PayloadDigest(files []PayloadFile) string {
	sorted := append([]PayloadFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", f.Path, f.Size, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ArtifactDigest is the single definition of artifact identity (docs/architecture.md
// §4.4). Preview, review, adopt and adopted verify must all call it instead of
// re-implementing a hashing rule.
func ArtifactDigest(dir string) (string, error) {
	files, err := ArtifactPayload(dir)
	if err != nil {
		return "", err
	}
	return PayloadDigest(files), nil
}

// ArtifactDigestWithOverride computes the digest while substituting the bytes
// of one file. The preview server uses it so the digest it verifies covers the
// exact bytes it is about to return.
func ArtifactDigestWithOverride(dir, overridePath string, overrideBytes []byte) (string, error) {
	files, err := artifactPayload(dir, overridePath, overrideBytes)
	if err != nil {
		return "", err
	}
	// The substituted file must be part of the payload, otherwise the digest
	// would not cover the bytes the caller is about to hand out.
	covered := false
	for _, f := range files {
		if f.Path == overridePath {
			covered = true
			break
		}
	}
	if !covered {
		return "", fmt.Errorf("provenance: %s is not part of the artifact payload", overridePath)
	}
	return PayloadDigest(files), nil
}

// ValidateSingleFilePayload enforces the v1.6 single-file artifact contract:
// the payload is exactly index.html. Returned otherwise so a benchmark that
// produces extra files fails at seal time instead of producing an artifact the
// preview and the published site cannot render.
func ValidateSingleFilePayload(dir string) ([]PayloadFile, error) {
	// The manifest is excluded from the payload, so check for it explicitly: a
	// benchmark that produced output/manifest.json must fail the run rather
	// than silently turning into an adopted manifest.
	if _, err := os.Lstat(filepath.Join(dir, ManifestName)); err == nil {
		return nil, fmt.Errorf("provenance: %s is a reserved artifact name", ManifestName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files, err := ArtifactPayload(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("provenance: artifact payload is empty (index.html is required)")
	}
	if len(files) != 1 || files[0].Path != "index.html" {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.Path)
		}
		return nil, fmt.Errorf("provenance: artifact payload must be exactly index.html, found %v", names)
	}
	return files, nil
}
