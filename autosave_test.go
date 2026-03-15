package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"filippo.io/age"
)

func TestAutosaveInterval(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "age-edit-autosave-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	writeFileInTempDir := func(name, content string) (string, error) {
		path := filepath.Join(tempDir, name)
		return path, os.WriteFile(path, []byte(content), 0o600)
	}

	buildInTempDir := func(pkg, output string) (string, error) {
		if runtime.GOOS == "windows" {
			output += ".exe"
		}

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

	testEditorPath, err := buildInTempDir("./test/edit", "test-editor")
	if err != nil {
		t.Fatalf("failed to build ./test/edit binary: %v", err)
	}

	// Run age-edit with autosave enabled.
	// The test editor will modify the file, and autosave should encrypt it.
	errChan := make(chan error)
	go func() {
		cmd := exec.Command(
			ageEditPath,
			"--editor", testEditorPath,
			"--autosave", "100ms",
			"--no-lock",
			"--no-memlock",
			"--temp-dir", tempDir,
			idFilePath,
			encFilePath,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		errChan <- cmd.Run()
	}()

	// Poll the encrypted file to detect autosave.
	// The test editor sleeps for 100ms, so autosave should trigger during that time.
	success := false
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)

		// Try to decrypt and check if the file has been modified.
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

			// The test editor appends "edit\n" to the file.
			if bytes.Contains(content, []byte("edit")) {
				success = true
				break
			}
		}
	}

	if !success {
		t.Error("Did not detect autosave encryption")
	}

	// Wait for age-edit to finish.
	err = <-errChan
	if err != nil {
		t.Errorf("age-edit failed: %v", err)
	}

	// Verify the final state of the file.
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

	if !bytes.Contains(content, []byte("edit")) {
		t.Error("Final file does not contain expected edits")
	}
}

func TestAutosaveDisabled(t *testing.T) {
	t.Parallel()

	// Test that autosave with zero duration is a no-op.
	stop := handleAutosave(0, func() error {
		t.Error("save function should not be called with zero duration")
		return nil
	})
	defer stop()

	time.Sleep(100 * time.Millisecond)

	// Test that autosave with negative duration is a no-op.
	stop = handleAutosave(-1*time.Second, func() error {
		t.Error("save function should not be called with negative duration")
		return nil
	})
	defer stop()

	time.Sleep(100 * time.Millisecond)
}
