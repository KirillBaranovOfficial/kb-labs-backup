package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestDistDigestV1GoldenVectors(t *testing.T) {
	type entry struct {
		Path    string `json:"path"`
		Type    string `json:"type"`
		Mode    string `json:"mode"`
		Content string `json:"content"`
		Target  string `json:"target"`
	}
	var fixture struct {
		Schema  string `json:"schema"`
		Vectors []struct {
			Name    string  `json:"name"`
			Entries []entry `json:"entries"`
			Digest  string  `json:"digest"`
		} `json:"vectors"`
		Pairs []struct {
			Name  string `json:"name"`
			Left  string `json:"left"`
			Right string `json:"right"`
		} `json:"pairs"`
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve golden vector fixture")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "testdata", "dist-digest-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "kb-dist-digest/v1" || len(fixture.Vectors) < 2 || len(fixture.Pairs) == 0 {
		t.Fatalf("invalid digest golden fixture: %#v", fixture)
	}
	digests := make(map[string]string, len(fixture.Vectors))
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			if vector.Digest == "" {
				t.Fatal("golden vector is missing its fixed digest")
			}
			root := t.TempDir()
			for _, item := range vector.Entries {
				path := filepath.Join(root, filepath.FromSlash(item.Path))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				switch item.Type {
				case "file":
					mode, err := strconv.ParseUint(item.Mode, 8, 32)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(item.Content), os.FileMode(mode)); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(item.Target, path); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatalf("unknown golden entry type %q", item.Type)
				}
			}
			digest, err := DistDigest(root)
			if err != nil {
				t.Fatal(err)
			}
			if digest != vector.Digest {
				t.Fatalf("golden digest = %s, want %s", digest, vector.Digest)
			}
			digests[vector.Name] = digest
		})
	}
	for _, pair := range fixture.Pairs {
		t.Run(pair.Name, func(t *testing.T) {
			left, leftExists := digests[pair.Left]
			right, rightExists := digests[pair.Right]
			if !leftExists || !rightExists {
				t.Fatalf("golden pair references missing vectors: %q, %q", pair.Left, pair.Right)
			}
			if left == right {
				t.Fatalf("golden pair %s did not change digest: %s", pair.Name, left)
			}
		})
	}
}

func TestDistDigestV1DistinguishesCanonicalEntryChanges(t *testing.T) {
	tests := []struct {
		name  string
		left  func(string) error
		right func(string) error
	}{
		{
			name: "a.js and a/index.js paths",
			left: func(root string) error {
				return os.WriteFile(filepath.Join(root, "a.js"), []byte("same bytes"), 0o644)
			},
			right: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(root, "a", "index.js"), []byte("same bytes"), 0o644)
			},
		},
		{
			name: "a and a.js paths",
			left: func(root string) error {
				return os.WriteFile(filepath.Join(root, "a"), []byte("same bytes"), 0o644)
			},
			right: func(root string) error {
				return os.WriteFile(filepath.Join(root, "a.js"), []byte("same bytes"), 0o644)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, right := filepath.Join(t.TempDir(), "left"), filepath.Join(t.TempDir(), "right")
			if err := os.MkdirAll(left, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(right, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := test.left(left); err != nil {
				t.Fatal(err)
			}
			if err := test.right(right); err != nil {
				t.Fatal(err)
			}
			leftDigest, err := DistDigest(left)
			if err != nil {
				t.Fatal(err)
			}
			rightDigest, err := DistDigest(right)
			if err != nil {
				t.Fatal(err)
			}
			if leftDigest == rightDigest {
				t.Fatalf("nested-prefix fixture did not change digest: %s", leftDigest)
			}
		})
	}
}

func TestDistDigestV1LengthBoundaryCollision(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for _, directory := range []string{left, right} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(left, "a"), []byte{'f', 'o', 'o', 1, 'b', 0, 'b', 'a', 'r'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "a"), []byte("foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "b"), []byte("bar"), 0o644); err != nil {
		t.Fatal(err)
	}
	leftDigest, err := DistDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := DistDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest == rightDigest {
		t.Fatalf("length-boundary collision: both layouts produced %s", leftDigest)
	}
}

func TestDistDigestV1BindsBytesNamesModesAndTypes(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "entry.js"), []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseDigest, err := DistDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name  string
		setup func(string) error
	}{
		{name: "byte flip", setup: func(path string) error { return os.WriteFile(path, []byte("same byteS"), 0o644) }},
		{name: "rename or move", setup: func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(filepath.Dir(path), "moved.js"), []byte("same bytes"), 0o644)
		}},
		{name: "executable bit", setup: func(path string) error { return os.Chmod(path, 0o755) }},
		{name: "symlink type and target", setup: func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "target.js"), []byte("same bytes"), 0o644); err != nil {
				return err
			}
			return os.Symlink("target.js", path)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			copyRoot := filepath.Join(t.TempDir(), "dist")
			if err := os.MkdirAll(copyRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(copyRoot, "entry.js")
			if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := mutation.setup(path); err != nil {
				t.Fatal(err)
			}
			digest, err := DistDigest(copyRoot)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseDigest {
				t.Fatalf("%s did not change dist digest %s", mutation.name, digest)
			}
		})
	}
}
