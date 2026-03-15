//go:build unix

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
)

func TestAutosaveWithSignalSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "age-edit-autosave-signal-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	writeFileInTempDir := func(name, content string) (string, error) {
		path := filepath.Join(tempDir, name)
		return path, os.WriteFile(path, []byte(content), 0o600)
	}

	buildInTempDir := func(pkg, output string) (string, error) {
		path := filepath.Join(tempDir, output)
		cmd := exec.Command("go", "build", "-o", path, pkg)
		return path, cmd.Run()
	}

	// Set up an identity and an identity file.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	idFilePath, err := writeFileInTempDir("id", identity.String())
	if err != nil {
		t.Fatal(err)
	}

	plainFilePath, err := writeFileInTempDir("foo", "initial")
	if err != nil {
		t.Fatal(err)
	}

	encFilePath, err := writeFileInTempDir("foo.age", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := encryptToFile(plainFilePath, encFilePath, true, "", []string{}, identity.Recipient()); err != nil {
		t.Fatal(err)
	}

	// Build the binaries.
	ageEditPath, err := buildInTempDir(".", "age-edit")
	if err != nil {
		t.Fatalf("failed to build age-edit binary: %v", err)
	}

	testEditorPath, err := buildInTempDir("./test/signal", "test-editor")
	if err != nil {
		t.Fatalf("failed to build ./test/signal binary: %v", err)
	}

	// Run age-edit with both autosave and signal handling.
	errChan := make(chan error)
	go func() {
		cmd := exec.Command(
			ageEditPath,
			"--editor", testEditorPath,
			"--autosave", "200ms",
			"--no-memlock",
			"--temp-dir", tempDir,
			idFilePath,
			encFilePath,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		errChan <- cmd.Run()
	}()

	// Poll the encrypted file for "phase1" (written by the signal handler).
	success := false
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)

		decFilePath, err := writeFileInTempDir("dec", "")
		if err != nil {
			t.Fatal(err)
		}

		err = decryptToFile(encFilePath, decFilePath, "", []string{}, identity)
		if err == nil {
			content, err := os.ReadFile(decFilePath)
			if err != nil {
				t.Fatal(err)
			}

			if bytes.Contains(content, []byte("phase1")) {
				success = true
				break
			}
		}
	}

	if !success {
		t.Error("Did not detect intermediate save triggered by signal")
	}

	// Wait for age-edit to finish.
	err = <-errChan
	if err != nil {
		t.Errorf("age-edit failed: %v", err)
	}

	// Verify the final state includes both "phase1" and "phase2".
	decFilePath, err := writeFileInTempDir("dec-final", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := decryptToFile(encFilePath, decFilePath, "", []string{}, identity); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(decFilePath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(content, []byte("phase1")) {
		t.Error(`Final file does not contain "phase1"`)
	}

	if !bytes.Contains(content, []byte("phase2")) {
		t.Error(`Final file does not contain "phase2"`)
	}
}
