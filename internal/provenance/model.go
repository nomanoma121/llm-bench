// Model identity: hashing of the model directory that is mounted into a run.
// The same manifest format is produced locally (direct directory scan) and
// remotely (python3 script executed inside a sandbox), so digests are
// comparable across execution kinds.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ModelFile is one regular file of the model directory.
type ModelFile struct {
	Path   string `json:"path"` // slash-separated, relative to the model root
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ModelIdentity is the manifest recorded as output/model-identity.json.
type ModelIdentity struct {
	Root       string      `json:"root"`
	TreeDigest string      `json:"tree_digest"`
	Files      []ModelFile `json:"files"`
}

// TreeDigest computes the digest over a sorted "path \0 size \0 sha256 \n"
// concatenation of the manifest's files.
func (m ModelIdentity) ComputeTreeDigest() string {
	files := append([]ModelFile(nil), m.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", f.Path, f.Size, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HashTreeLocal hashes every regular file under dir. Symlinks and other
// non-regular entries are rejected: a swapped model must fail loudly.
func HashTreeLocal(dir string) (ModelIdentity, error) {
	var files []ModelFile
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("provenance: %s is not a regular file (symlinks and specials are rejected)", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, ModelFile{
			Path:   filepath.ToSlash(rel),
			Size:   info.Size(),
			SHA256: hex.EncodeToString(h.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return ModelIdentity{}, fmt.Errorf("provenance: hash tree %s: %w", dir, err)
	}
	identity := ModelIdentity{Root: dir, Files: files}
	identity.TreeDigest = identity.ComputeTreeDigest()
	return identity, nil
}

// HashTreeScript returns the python3 program executed inside a sandbox to
// produce the same manifest for /models/<model-id>. The dev image therefore
// requires python3. Output is the ModelIdentity JSON.
func HashTreeScript(modelRoot string) string {
	return fmt.Sprintf(`import hashlib, json, os, stat, sys
root = %q
rst = os.lstat(root)
if stat.S_ISLNK(rst.st_mode) or not stat.S_ISDIR(rst.st_mode):
    sys.stderr.write("model root is not a directory: " + root + "\n")
    sys.exit(3)
files = []
for dirpath, dirnames, filenames in os.walk(root):
    dirnames.sort()
    for name in list(dirnames):
        p = os.path.join(dirpath, name)
        st = os.lstat(p)
        if stat.S_ISLNK(st.st_mode) or not stat.S_ISDIR(st.st_mode):
            sys.stderr.write("not a directory: " + p + "\n")
            sys.exit(3)
    for name in sorted(filenames):
        path = os.path.join(dirpath, name)
        st = os.lstat(path)
        if not stat.S_ISREG(st.st_mode):
            sys.stderr.write("not a regular file: " + path + "\n")
            sys.exit(3)
        h = hashlib.sha256()
        with open(path, "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
        files.append({
            "path": os.path.relpath(path, root).replace(os.sep, "/"),
            "size": st.st_size,
            "sha256": h.hexdigest(),
        })
print(json.dumps({"root": root, "tree_digest": "", "files": files}))
`, modelRoot)
}

// ParseModelIdentity decodes a manifest (local or remote) and (re)computes
// its tree digest so the recorded value cannot disagree with the file list.
func ParseModelIdentity(b []byte) (ModelIdentity, error) {
	var m ModelIdentity
	if err := json.Unmarshal(b, &m); err != nil {
		return ModelIdentity{}, fmt.Errorf("provenance: decode model identity: %w", err)
	}
	if len(m.Files) == 0 {
		return ModelIdentity{}, errors.New("provenance: model identity has no files")
	}
	m.TreeDigest = m.ComputeTreeDigest()
	return m, nil
}
