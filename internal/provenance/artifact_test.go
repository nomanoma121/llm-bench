package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

func writePayload(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestArtifactDigestIsCanonicalAndStable(t *testing.T) {
	dir := writePayload(t, map[string]string{"index.html": "<html></html>"})
	digest, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Changing the bytes must change the digest.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>x</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if digest == other {
		t.Fatal("digest did not change with the payload")
	}
	// Re-running over the same bytes is stable.
	again, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != other {
		t.Fatalf("digest is not stable: %s != %s", other, again)
	}
}

func TestArtifactDigestExcludesManifest(t *testing.T) {
	dir := writePayload(t, map[string]string{"index.html": "x"})
	before, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("the manifest must not take part in the payload digest")
	}
}

func TestArtifactPayloadRejectsSymlinks(t *testing.T) {
	dir := writePayload(t, map[string]string{"index.html": "x"})
	if err := os.Symlink(filepath.Join(dir, "index.html"), filepath.Join(dir, "link.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactDigest(dir); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestArtifactDigestWithOverrideCoversReturnedBytes(t *testing.T) {
	dir := writePayload(t, map[string]string{"index.html": "original"})
	recorded, err := ArtifactDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded digest verifies the bytes the caller is about to return.
	if got, err := ArtifactDigestWithOverride(dir, "index.html", []byte("original")); err != nil || got != recorded {
		t.Fatalf("override digest = %s, %v", got, err)
	}
	// Substituted bytes cannot satisfy the recorded digest.
	if got, err := ArtifactDigestWithOverride(dir, "index.html", []byte("tampered")); err != nil || got == recorded {
		t.Fatalf("tampered bytes must not match: %s, %v", got, err)
	}
}

func TestValidateSingleFilePayload(t *testing.T) {
	ok := writePayload(t, map[string]string{"index.html": "x"})
	files, err := ValidateSingleFilePayload(ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "index.html" {
		t.Fatalf("payload = %+v", files)
	}

	for name, dir := range map[string]string{
		"extra file":    writePayload(t, map[string]string{"index.html": "x", "app.js": "y"}),
		"missing index": writePayload(t, map[string]string{"other.html": "x"}),
		"nested file":   writePayload(t, map[string]string{"index.html": "x", "assets/a.js": "y"}),
		"reserved name": writePayload(t, map[string]string{"index.html": "x", ManifestName: "{}"}),
	} {
		if _, err := ValidateSingleFilePayload(dir); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestArtifactDigestWithOverrideRejectsUncoveredPath(t *testing.T) {
	dir := writePayload(t, map[string]string{"index.html": "x", ManifestName: "{}"})
	if _, err := ArtifactDigestWithOverride(dir, ManifestName, []byte("{}")); err == nil {
		t.Fatal("a path outside the payload must not be digestable")
	}
	if _, err := ArtifactDigestWithOverride(dir, "missing.html", []byte("x")); err == nil {
		t.Fatal("a missing path must not be digestable")
	}
}
