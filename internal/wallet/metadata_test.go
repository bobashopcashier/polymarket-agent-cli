package wallet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataRejectsBroadPermissionsSymlinkDuplicatesAndUnknownFields(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "broad permissions",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(`{"schemaVersion":"pmx.wallet-metadata/v1","version":1,"wallets":{}}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "duplicate key",
			setup: func(t *testing.T, path string) {
				t.Helper()
				data := []byte(`{"schemaVersion":"pmx.wallet-metadata/v1","version":1,"version":1,"wallets":{}}`)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unknown field",
			setup: func(t *testing.T, path string) {
				t.Helper()
				data := []byte(`{"schemaVersion":"pmx.wallet-metadata/v1","version":1,"wallets":{},"privateKey":"forbidden"}`)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				if err := os.WriteFile(target, []byte(`{"schemaVersion":"pmx.wallet-metadata/v1","version":1,"wallets":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wallets.json")
			test.setup(t, path)
			manager, err := NewManager(path, newMemorySecrets())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.List(context.Background()); err == nil {
				t.Fatal("unsafe metadata was accepted")
			}
		})
	}
}

func TestManagerRequiresAbsoluteCleanMetadataPath(t *testing.T) {
	if _, err := NewManager("relative/wallets.json", newMemorySecrets()); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("relative path returned %v", err)
	}
	path := t.TempDir() + string(filepath.Separator) + "a" + string(filepath.Separator) + ".." + string(filepath.Separator) + "wallets.json"
	if _, err := NewManager(path, newMemorySecrets()); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("unclean path returned %v", err)
	}
}
