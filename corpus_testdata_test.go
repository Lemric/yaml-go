package yaml

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibilityTestdataIsComplete(t *testing.T) {
	entries, err := os.ReadDir("testdata/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 26 {
		t.Fatalf("fixture count = %d; want 26", len(entries))
	}

	index, err := os.ReadFile("testdata/fixtures/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(index), "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if name == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join("testdata", "fixtures", name+".yaml")); err != nil {
			t.Fatalf("indexed fixture %q is missing: %v", name, err)
		}
	}
}

func TestBinaryFixtureIntegrity(t *testing.T) {
	data, err := os.ReadFile("testdata/fixtures/arrow.gif")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	want := "0dd8f84d24840a21a56495526e5b227911d13389109c62194a64b6ccbf3b1400"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("binary fixture hash = %s; want %s", got, want)
	}
}
