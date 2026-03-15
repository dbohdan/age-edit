package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestFileLocking(t *testing.T) {
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

	// Set up an identity and an identity file.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	idFilePath := filepath.Join(tempDir, "id")
	if err := os.WriteFile(idFilePath, []byte(identity.String()), filePerm); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name        string
		lock        bool
		readOnly    bool
		args        []string
		expectError bool
	}{
		{
			name:        "concurrent edits with locking should fail",
			lock:        true,
			readOnly:    false,
			args:        []string{},
			expectError: true,
		},
		{
			name:        "concurrent edits without locking should succeed",
			lock:        false,
			readOnly:    false,
			args:        []string{},
			expectError: false,
		},
		{
			name:        "concurrent read-only edits with locking should succeed",
			lock:        true,
			readOnly:    true,
			args:        []string{"--read-only"},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create an encrypted file.
			plainFilePath := filepath.Join(tempDir, "plain")
			if err := os.WriteFile(plainFilePath, []byte("File-locking plain text."), filePerm); err != nil {
				t.Fatal(err)
			}

			encFilePath := filepath.Join(tempDir, "encrypted.age")
			if err := encryptToFile(plainFilePath, encFilePath, false, "", []string{}, identity.Recipient()); err != nil {
				t.Fatal(err)
			}

			// Run two concurrent edits.
			done := make(chan error, 2)
			editEncFile := func(lock, readOnly bool, arg ...string) {
				_, err = edit(config{
					idsPath:     idFilePath,
					encPath:     encFilePath,
					tempDirBase: tempDir,

					armor:    true,
					lock:     lock,
					readOnly: readOnly,

					command: testEditorPath,
					args:    arg,
				})
				done <- err
			}

			for i := 0; i < 2; i++ {
				go editEncFile(tc.lock, tc.readOnly, tc.args...)
			}

			// Check the results.
			err1 := <-done
			err2 := <-done

			if tc.expectError {
				// One edit should succeed; one should fail with a lock error.
				if err1 == nil && err2 == nil {
					t.Error("Expected one edit to fail due to locking, but both succeeded")
				}

				if runtime.GOOS != "windows" && err1 != nil && err2 != nil {
					t.Errorf("Expected one edit to fail due to locking, but both failed:\nedit1: %v\nedit2: %v", err1, err2)
				}

				if !strings.Contains(err1.Error(), "locked") && !strings.Contains(err2.Error(), "locked") {
					t.Errorf("Expected at least one lock error, got:\nedit1: %v\nedit2: %v", err1, err2)
				}

				return
			}

			// Both edits should succeed.
			if err1 != nil || err2 != nil {
				t.Errorf("Expected both edits to succeed, got:\nedit1: %v\nedit2: %v", err1, err2)
			}
		})
	}
}
