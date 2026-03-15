package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
)

func TestCheckAccess(t *testing.T) {
	t.Parallel()

	// Create a temporary file to test against.
	tempFile, err := os.CreateTemp("", "test-file")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	tests := []struct {
		path     string
		readOnly bool
		expectOk bool
	}{
		// File exists and is readable.
		{tempFile.Name(), true, true},
		// File does not exist in read-only mode.
		{"nonexistent-file", true, false},
		// File does not exist, not read-only mode.
		{"nonexistent-file", false, true},
	}

	for _, tt := range tests {
		_, err := checkAccess(tt.path, tt.readOnly)
		if (err == nil) != tt.expectOk {
			t.Errorf("checkAccess(%q, readOnly=%v) = %v, expected %v", tt.path, tt.readOnly, err == nil, tt.expectOk)
		}
	}
}

func TestEncryptAndDecryptToFile(t *testing.T) {
	t.Parallel()

	testData := "Hello, world!\n"

	// Create a temporary file for the input.
	inputFile, err := os.CreateTemp("", "input")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputFile.Name())
	_, _ = inputFile.WriteString(testData)
	inputFile.Close()

	// Create a temporary file for the encrypted and decrypted the output.
	encryptedFile, err := os.CreateTemp("", "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encryptedFile.Name())

	decryptedFile, err := os.CreateTemp("", "decrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(decryptedFile.Name())

	// Generate an age key pair for testing.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	recipient := identity.Recipient()

	// Test encryption.
	err = encryptToFile(inputFile.Name(), encryptedFile.Name(), true, "", []string{}, recipient)
	if err != nil {
		t.Errorf("encryptToFile() failed: %v", err)
	}

	// Test decryption.
	err = decryptToFile(encryptedFile.Name(), decryptedFile.Name(), "", []string{}, identity)
	if err != nil {
		t.Errorf("decryptToFile() failed: %v", err)
	}

	// Compare decrypted content with the original.
	decryptedContent, _ := os.ReadFile(decryptedFile.Name())
	if string(decryptedContent) != testData {
		t.Errorf("Decrypted content mismatch: got %q, but expected %q", decryptedContent, testData)
	}
}

func TestEncryptAndDecryptToFileWithGzip(t *testing.T) {
	t.Parallel()

	// Build the test gzip binary.
	tempDir := t.TempDir()
	gzipPath := filepath.Join(tempDir, "gzip")
	if runtime.GOOS == "windows" {
		gzipPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", gzipPath, "./test/gzip")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build test/gzip binary: %v", err)
	}

	testData := "Hello, world!\n"

	inputFile, err := os.CreateTemp("", "input")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputFile.Name())
	_, _ = inputFile.WriteString(testData)
	inputFile.Close()

	// Create a temporary file for the encrypted and decrypted the output.
	encryptedFile, err := os.CreateTemp("", "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encryptedFile.Name())

	decryptedFile, err := os.CreateTemp("", "decrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(decryptedFile.Name())

	// Generate an age key pair for testing.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	recipient := identity.Recipient()

	// Test encryption with gzip compression.
	err = encryptToFile(inputFile.Name(), encryptedFile.Name(), true, gzipPath, []string{}, recipient)
	if err != nil {
		t.Errorf("encryptToFile() failed: %v", err)
	}

	// Test decryption with gzip decompression.
	err = decryptToFile(encryptedFile.Name(), decryptedFile.Name(), gzipPath, []string{"-d"}, identity)
	if err != nil {
		t.Errorf("decryptToFile() failed: %v", err)
	}

	// Compare decrypted content with the original.
	decryptedContent, _ := os.ReadFile(decryptedFile.Name())
	if string(decryptedContent) != testData {
		t.Errorf("Decrypted content mismatch: got %q, but expected %q", decryptedContent, testData)
	}
}

func TestGetRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt.age", "file.txt"},
		{"example.age", "example"},
		{"example.odt", "example.odt"},
		{"no-ext", "no-ext"},
	}

	for _, tt := range tests {
		result := getRoot(tt.input)

		if result != tt.expected {
			t.Errorf("getRoot(%q) is %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestLoadIdentities(t *testing.T) {
	t.Parallel()

	corruptedKey := "AGE-SECRET-KEY-1XXXXXXXXXX1234567890abcdefghijklmnopqrstuvwxyz"
	validKey := "AGE-SECRET-KEY-150E3TFLT765WC7X9E2Y6KAN2XA7NE4DN0XVCR4ATTFQK6GSXCGVS3KS7MS"

	tests := []struct {
		content  string
		expected int
		hasError bool
	}{
		// A single valid key.
		{validKey + "\n", 1, false},
		// A single valid key without a line feed.
		{validKey, 1, false},
		// Multiple valid keys.
		{validKey + "\n" + validKey + "\n", 2, false},
		// An obviously invalid key.
		{"invalid-key\n", 0, true},
		// A corrupted key.
		{corruptedKey + "\n", 0, true},
		// Ignore comments and blank lines.
		{"# Comment\n \n\n" + validKey + "\n", 1, false},
		// An indented comment.
		{"    # Comment\n" + validKey, 1, false},
		// An empty file.
		{"", 0, true},
	}

	for _, tt := range tests {
		tempFile, err := os.CreateTemp("", "identities")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tempFile.Name())

		_, err = tempFile.WriteString(tt.content)
		if err != nil {
			t.Fatal(err)
		}
		tempFile.Close()

		ids, recs, err := loadIdentities(tempFile.Name())

		if tt.hasError && err == nil {
			t.Errorf("loadIdentities(%q) expected error, got none", tt.content)
		}

		if !tt.hasError && len(ids) != tt.expected {
			t.Errorf("loadIdentities(%q) returned %d identities, expected %d", tt.content, len(ids), tt.expected)
		}

		if len(ids) != len(recs) {
			t.Errorf("loadIdentities(%q) returned mismatched identities and recipients", tt.content)
		}
	}
}

func TestEdit(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	buildInTempDir := func(pkg, output string) (string, error) {
		if runtime.GOOS == "windows" {
			output += ".exe"
		}

		path := filepath.Join(tempDir, output)
		cmd := exec.Command("go", "build", "-o", path, pkg)
		return path, cmd.Run()
	}

	testEditorPath, err := buildInTempDir("./test/edit", "test-editor")
	if err != nil {
		t.Fatalf("failed to build test/edit binary: %v", err)
	}

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("failed to generate identity: %v", err)
	}

	idFile, err := os.CreateTemp("", "identities")
	if err != nil {
		t.Fatalf("failed to create temp identity file: %v", err)
	}
	defer os.Remove(idFile.Name())
	_, _ = idFile.WriteString(identity.String())
	idFile.Close()

	tests := []struct {
		name            string
		lock            bool
		readOnly        bool
		force           bool
		checkFn         func(t *testing.T, tempDir string, encFilePath string, initialModTime time.Time)
		expectEditError bool
	}{
		{
			name:     "read-only mode",
			lock:     false,
			readOnly: true,
			checkFn: func(t *testing.T, tempDir string, encFilePath string, initialModTime time.Time) {
				files, err := os.ReadDir(tempDir)
				if err != nil {
					t.Fatalf("could not read temp dir: %v", err)
				}
				if len(files) != 1 {
					t.Fatalf("expected 1 file in temp dir, got %d", len(files))
				}
				tempFilePath := filepath.Join(tempDir, files[0].Name())
				info, err := os.Stat(tempFilePath)
				if err != nil {
					t.Fatalf("could not stat temp file: %v", err)
				}

				// The permissions should be read-only.
				perm := info.Mode().Perm()
				refPerm := os.FileMode(0o400)
				if perm != refPerm && !(runtime.GOOS == "windows" && perm&0o700 == refPerm) {
					t.Errorf("expected temp file permissions to be %o, got %o", refPerm, perm)
				}
			},
			expectEditError: false,
		},
		{
			name:     "force re-encryption",
			lock:     false,
			readOnly: false,
			force:    true,
			checkFn: func(t *testing.T, tempDir string, encFilePath string, initialModTime time.Time) {
				info, err := os.Stat(encFilePath)
				if err != nil {
					t.Fatalf("failed to stat encrypted file: %v", err)
				}
				if !info.ModTime().After(initialModTime) {
					t.Errorf("expected encrypted file modification time to change, but it did not. Initial: %v, Current: %v", initialModTime, info.ModTime())
				}
			},
			expectEditError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create encrypted file with some content.
			content := "secret content"
			plainFile, err := os.CreateTemp("", "plain")
			if err != nil {
				t.Fatalf("failed to create temp plain file: %v", err)
			}
			defer os.Remove(plainFile.Name())
			if _, err := plainFile.WriteString(content); err != nil {
				t.Fatalf("failed to write to plain file: %v", err)
			}
			plainFile.Close()

			encFile, err := os.CreateTemp("", "encrypted")
			if err != nil {
				t.Fatalf("failed to create temp encrypted file: %v", err)
			}
			defer os.Remove(encFile.Name())

			if err := encryptToFile(plainFile.Name(), encFile.Name(), false, "", []string{}, identity.Recipient()); err != nil {
				t.Fatalf("failed to encrypt file for test: %v", err)
			}

			// Get the initial modification time of the encrypted file.
			initialEncFileInfo, err := os.Stat(encFile.Name())
			if err != nil {
				t.Fatalf("failed to stat encrypted file: %v", err)
			}
			initialModTime := initialEncFileInfo.ModTime()

			// Create a temporary directory.
			tempDirBase := t.TempDir()

			// Call edit.
			editArgs := []string{}
			if tt.readOnly {
				editArgs = append(editArgs, "--read-only")
			}

			tempDir, err := edit(config{
				idsPath:     idFile.Name(),
				encPath:     encFile.Name(),
				tempDirBase: tempDirBase,

				armor:    false,
				lock:     tt.lock,
				readOnly: tt.readOnly,
				force:    tt.force,
				command:  testEditorPath,
				args:     editArgs,
			})
			if (err != nil) != tt.expectEditError {
				t.Fatalf("edit() error = %v, expectEditError %v", err, tt.expectEditError)
			}
			if err == nil && tempDir != "" {
				defer os.RemoveAll(tempDir)
			}

			if tt.checkFn != nil {
				tt.checkFn(t, tempDir, encFile.Name(), initialModTime)
			}
		})
	}
}

func TestParseWarn(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"5", 5 * time.Second, false},
		{"0", 0, false},
		{"10s", 10 * time.Second, false},
		{"1m", 1 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"invalid", 0, true},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var got time.Duration
			if tt.input != "" {
				d, err := time.ParseDuration(tt.input)
				if err != nil {
					seconds, errInt := strconv.Atoi(tt.input)
					if errInt != nil {
						if !tt.wantErr {
							t.Errorf("unexpected error for %q", tt.input)
						}
						return
					}
					d = time.Duration(seconds) * time.Second
				}
				got = d
			}

			if tt.wantErr {
				t.Errorf("expected error for %q, but got none", tt.input)
			} else if got != tt.expected {
				t.Errorf("parseWarn(%q) = %v, expected %v", tt.input, got, tt.expected)
			}
		})
	}
}
